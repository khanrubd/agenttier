/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package terminal

import (
	"strings"
	"testing"
)

// buildShellCommand is the part of the bridge that decides how to launch
// the shell inside the sandbox. It needs to:
//
//  1. Always return /bin/sh -c <wrapper> (so we can include the tmux
//     presence check in the command line itself, no Go-side detection).
//  2. Branch on `command -v tmux` so existing pods without tmux fall back
//     cleanly.
//  3. Use a stable per-sandbox tmux session name so reconnects re-attach
//     instead of spawning fresh shells.
//  4. Pass `-l` to the shell in both branches so login-shell semantics
//     (/etc/profile etc.) match the pre-change behavior.
//  5. Refuse to be shell-injectable if the sandbox ID or shell path ever
//     contains a single quote.
//
// The cases below cover all of the above.
func TestBuildShellCommand_DefaultBash(t *testing.T) {
	s := &Session{Shell: "/bin/bash", SandboxID: "sb-abc123"}
	got := buildShellCommand(s)

	if len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-c" {
		t.Fatalf("expected /bin/sh -c <script>, got %q", got)
	}
	wrapper := got[2]

	// Must include both branches.
	if !strings.Contains(wrapper, "command -v tmux") {
		t.Errorf("wrapper missing tmux presence check: %s", wrapper)
	}
	if !strings.Contains(wrapper, "exec tmux -u -2 -f /tmp/.agenttier-tmux.conf new-session -A -s 'agenttier-sb-abc123' -- '/bin/bash' -l") {
		t.Errorf("wrapper missing tmux command for sb-abc123: %s", wrapper)
	}
	if !strings.Contains(wrapper, "set -g status off") {
		t.Errorf("wrapper not writing status-off tmux config: %s", wrapper)
	}
	if !strings.Contains(wrapper, "exec '/bin/bash' -l") {
		t.Errorf("wrapper missing fallback shell: %s", wrapper)
	}
}

func TestBuildShellCommand_EmptyShellDefaultsToBash(t *testing.T) {
	s := &Session{Shell: "", SandboxID: "sb-1"}
	got := buildShellCommand(s)
	if !strings.Contains(got[2], "'/bin/bash' -l") {
		t.Errorf("expected default /bin/bash, got: %s", got[2])
	}
}

func TestBuildShellCommand_CustomShell(t *testing.T) {
	s := &Session{Shell: "/usr/bin/zsh", SandboxID: "sb-1"}
	got := buildShellCommand(s)
	if !strings.Contains(got[2], "'/usr/bin/zsh' -l") {
		t.Errorf("expected /usr/bin/zsh, got: %s", got[2])
	}
}

func TestBuildShellCommand_PerSandboxSessionName(t *testing.T) {
	// Two different sandboxes must produce two different tmux session
	// names — otherwise reconnects on sandbox A would attach to sandbox
	// B's tmux server, which is impossible since they're separate pods,
	// but testing that the name includes the sandbox ID guards against
	// a regression where someone "simplifies" the name.
	s1 := buildShellCommand(&Session{Shell: "/bin/bash", SandboxID: "sb-aaa"})
	s2 := buildShellCommand(&Session{Shell: "/bin/bash", SandboxID: "sb-bbb"})
	if s1[2] == s2[2] {
		t.Error("expected different wrappers for different sandboxes")
	}
	if !strings.Contains(s1[2], "'agenttier-sb-aaa'") {
		t.Errorf("s1 missing sandbox name: %s", s1[2])
	}
	if !strings.Contains(s2[2], "'agenttier-sb-bbb'") {
		t.Errorf("s2 missing sandbox name: %s", s2[2])
	}
}

// Shell injection guard: a malicious sandbox ID with a single quote
// must not break out of the quoting and inject extra commands.
func TestShellQuote_EscapesSingleQuotes(t *testing.T) {
	cases := map[string]string{
		"":                 "''",
		"plain":            "'plain'",
		"with space":       "'with space'",
		`O'Brien`:          `'O'\''Brien'`,
		`'`:                `''\'''`,
		`a'b'c`:            `'a'\''b'\''c'`,
		"/bin/bash":        "'/bin/bash'",
		"sb-with$dollar":   "'sb-with$dollar'",
		"sb-with;semi":     "'sb-with;semi'",
		"sb-with`backtick": "'sb-with`backtick'",
	}
	for in, want := range cases {
		got := shellQuote(in)
		if got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// Even with a hostile sandbox ID, the resulting wrapper must still parse
// as a single tmux session-name argument, not as multiple commands.
func TestBuildShellCommand_RefusesInjection(t *testing.T) {
	hostile := "sb-1'; rm -rf /; #"
	s := &Session{Shell: "/bin/bash", SandboxID: hostile}
	got := buildShellCommand(s)
	wrapper := got[2]
	// The escape sequence \'\' must appear, never an unescaped semicolon
	// outside the quoted region. The cheapest way to verify is: the
	// wrapper should NOT contain `; rm -rf /` outside of being inside the
	// escaped tmux session name. Easier check: confirm it contains the
	// fully-escaped form and the dangerous unescaped string never appears
	// at the top level of the command.
	if !strings.Contains(wrapper, `'\''`) {
		t.Errorf("expected escaped single quotes in wrapper: %s", wrapper)
	}
	// `rm -rf /` should only appear inside a quoted tmux session name.
	// If it appeared as a separate command, the wrapper would contain
	// `; rm -rf /; ` outside any quotes — easy to spot by counting
	// occurrences of the bare semicolon-rm pattern.
	if strings.Contains(wrapper, "; rm -rf /; ") && !strings.Contains(wrapper, `'\''; rm -rf /; #'`) {
		t.Errorf("possible shell injection: %s", wrapper)
	}
}
