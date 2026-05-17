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

package sandboxruntime

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// hasPTYSupport returns true on platforms where the PTY tests can run.
// macOS and Linux both work; Windows doesn't have /dev/ptmx and the
// creack/pty package returns ENOTTY there. CI runs on Linux runners
// so this gate primarily helps local development on Windows.
func hasPTYSupport() bool {
	// /dev/ptmx is the canonical signal — present on Linux and
	// (sort-of) on macOS via syscalls. The creack/pty library
	// abstracts this; we just check we're not on Windows.
	if _, err := os.Stat("/dev/ptmx"); err == nil {
		return true
	}
	// macOS doesn't expose /dev/ptmx as a stat-able path but the
	// library still works via posix_openpt(3) under the hood.
	return os.Getenv("GOOS") != "windows"
}

// helper: spin up the runtime in a goroutine and return a ws://
// dial URL plus a cleanup func. AuthToken is empty so tests don't
// have to deal with the Bearer header.
func startTestRuntime(t *testing.T) (string, func()) {
	t.Helper()

	server := New(Config{
		// Listen on a random localhost port. httptest.NewServer would
		// be cleaner but it doesn't expose the underlying *http.Server
		// for us to install our own mux.
		ListenAddr: "127.0.0.1:0",
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	// Use httptest.NewServer for the actual networking — easier than
	// reproducing port binding + URL construction manually. We swap
	// the server's handler in by reaching into its mux.
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", server.handleHealthz)
	mux.HandleFunc("/pty", server.handlePTY)
	mux.HandleFunc("/exec", server.handleExec)
	httpSrv := httptest.NewServer(mux)

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")
	return wsURL, func() {
		httpSrv.Close()
	}
}

// dialPTY connects to /pty with the given query params and returns the
// websocket connection. Test helper.
func dialPTY(t *testing.T, baseWSURL string, params url.Values) *websocket.Conn {
	t.Helper()
	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.Dial(baseWSURL+"/pty?"+params.Encode(), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial /pty: %v (status=%s)", err, resp.Status)
		}
		t.Fatalf("dial /pty: %v", err)
	}
	return conn
}

func TestPTY_SpawnsShellAndEchoesInput(t *testing.T) {
	if !hasPTYSupport() {
		t.Skip("requires /dev/ptmx-style PTY support")
	}

	baseURL, cleanup := startTestRuntime(t)
	defer cleanup()

	conn := dialPTY(t, baseURL, url.Values{
		"shell": []string{"/bin/sh"},
		"cols":  []string{"80"},
		"rows":  []string{"24"},
	})
	defer conn.Close()

	// Send a simple command. /bin/sh's prompt eats the early bytes
	// before the kernel's TTY echo settles, so we look for distinctive
	// output in the response stream rather than asserting on the full
	// transcript.
	deadline := time.Now().Add(5 * time.Second)
	_ = conn.SetWriteDeadline(deadline)
	body, _ := json.Marshal(ptyMessage{Type: ptyMsgTypeInput, Data: "echo PTYWORKS_PROBE\n"})
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatalf("write input: %v", err)
	}

	// Read frames until we see the probe text or hit a 3-second budget.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var sawProbe bool
deadlineLoop:
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var msg ptyMessage
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		if msg.Type == ptyMsgTypeOutput && strings.Contains(msg.Data, "PTYWORKS_PROBE") {
			sawProbe = true
			break deadlineLoop
		}
	}
	if !sawProbe {
		t.Error("expected to see PTYWORKS_PROBE in shell output, got none in 3s")
	}
}

func TestPTY_RejectsRelativeShellPath(t *testing.T) {
	if !hasPTYSupport() {
		t.Skip("requires /dev/ptmx-style PTY support")
	}

	baseURL, cleanup := startTestRuntime(t)
	defer cleanup()

	dialer := websocket.DefaultDialer
	_, resp, err := dialer.Dial(baseURL+"/pty?shell=bash", nil)
	if err == nil {
		t.Fatal("expected dial failure for relative shell path")
	}
	if resp == nil {
		t.Fatalf("expected HTTP response with error, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", resp.StatusCode)
	}
}

func TestPTY_HeartbeatFrames(t *testing.T) {
	if !hasPTYSupport() {
		t.Skip("requires /dev/ptmx-style PTY support")
	}

	// We can't easily drop the keepalive interval to a test-friendly
	// value without exposing internals; instead this test verifies
	// the existence of the keepalive code path by checking that the
	// connection stays open past 1 second of idle and produces no
	// errors. A more rigorous test would mock the ticker; that's a
	// bigger refactor for limited additional confidence.
	baseURL, cleanup := startTestRuntime(t)
	defer cleanup()

	conn := dialPTY(t, baseURL, url.Values{"shell": []string{"/bin/sh"}})
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			// Read deadline fired without an error frame — connection
			// is still open, which is what we wanted to confirm.
			ne, ok := err.(interface{ Timeout() bool })
			if ok && ne.Timeout() {
				return
			}
			t.Fatalf("unexpected read error: %v", err)
		}
	}
}

func TestParsePTYRequest_Defaults(t *testing.T) {
	cases := []struct {
		name     string
		queryStr string
		want     PTYRequest
		wantErr  bool
	}{
		{
			name:     "all defaults",
			queryStr: "",
			want:     PTYRequest{Shell: "/bin/bash", Cols: 120, Rows: 40},
		},
		{
			name:     "explicit shell + size",
			queryStr: "shell=/bin/zsh&cols=80&rows=24",
			want:     PTYRequest{Shell: "/bin/zsh", Cols: 80, Rows: 24},
		},
		{
			name:     "with cwd",
			queryStr: "shell=/bin/bash&cwd=/workspace",
			want:     PTYRequest{Shell: "/bin/bash", Cwd: "/workspace", Cols: 120, Rows: 40},
		},
		{
			name:     "junk dimensions fall back",
			queryStr: "shell=/bin/bash&cols=abc&rows=xyz",
			want:     PTYRequest{Shell: "/bin/bash", Cols: 120, Rows: 40},
		},
		{
			name:     "negative dimensions clamped",
			queryStr: "shell=/bin/bash&cols=-5&rows=-9",
			want:     PTYRequest{Shell: "/bin/bash", Cols: 1, Rows: 1},
		},
		{
			name:     "huge dimensions clamped to uint16-1",
			queryStr: "shell=/bin/bash&cols=999999&rows=999999",
			want:     PTYRequest{Shell: "/bin/bash", Cols: 65535, Rows: 65535},
		},
		{
			name:     "relative shell path rejected",
			queryStr: "shell=bash",
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/pty?"+tc.queryStr, nil)
			got, err := parsePTYRequest(r)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Auth: the /pty endpoint must reject unauthenticated upgrades when
// AuthToken is configured. (The startTestRuntime helper uses an empty
// token, so we run a separate server here with auth enabled.)
func TestPTY_AuthRequiredWhenTokenSet(t *testing.T) {
	if !hasPTYSupport() {
		t.Skip("requires /dev/ptmx-style PTY support")
	}

	server := New(Config{
		ListenAddr: "127.0.0.1:0",
		AuthToken:  "secret-token-xyz",
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/pty", server.requireAuth(server.handlePTY))
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/pty?shell=/bin/sh"

	// No Authorization header: reject.
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial failure for unauthenticated request")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}

	// Wrong token: reject.
	headers := http.Header{"Authorization": []string{"Bearer not-the-right-one"}}
	_, resp, err = websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		t.Fatal("expected dial failure for wrong token")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", resp.StatusCode)
	}

	// Correct token: succeed.
	headers = http.Header{"Authorization": []string{"Bearer secret-token-xyz"}}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("expected success with valid token, got: %v", err)
	}
	defer conn.Close()

	// Briefly confirm the upgrade succeeded by trying to send.
	deadline := time.Now().Add(2 * time.Second)
	_ = conn.SetWriteDeadline(deadline)
	body, _ := json.Marshal(ptyMessage{Type: ptyMsgTypeInput, Data: "echo ok\n"})
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Errorf("expected to write to authenticated conn: %v", err)
	}
}

// Window resize: send a resize frame and verify the runtime accepts it
// (the actual ioctl outcome isn't directly observable through the WS,
// but we can confirm no error frame comes back and the connection stays
// open).
func TestPTY_AcceptsResizeFrame(t *testing.T) {
	if !hasPTYSupport() {
		t.Skip("requires /dev/ptmx-style PTY support")
	}

	baseURL, cleanup := startTestRuntime(t)
	defer cleanup()

	conn := dialPTY(t, baseURL, url.Values{"shell": []string{"/bin/sh"}})
	defer conn.Close()

	resize, _ := json.Marshal(ptyMessage{Type: ptyMsgTypeResize, Cols: 200, Rows: 50})
	if err := conn.WriteMessage(websocket.TextMessage, resize); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	// If the resize panicked or errored on the runtime side, the
	// connection would be closed within a few ms. Wait a beat then
	// confirm we can still write.
	time.Sleep(100 * time.Millisecond)
	probe, _ := json.Marshal(ptyMessage{Type: ptyMsgTypeInput, Data: "true\n"})
	if err := conn.WriteMessage(websocket.TextMessage, probe); err != nil {
		t.Errorf("conn closed after resize: %v", err)
	}
}

// Context cancel: when the request context cancels, the runtime cleans
// up and the WS read returns. We model this by closing the connection
// from the client side and confirming the runtime drops cleanly.
func TestPTY_ClientCloseTearsDown(t *testing.T) {
	if !hasPTYSupport() {
		t.Skip("requires /dev/ptmx-style PTY support")
	}

	baseURL, cleanup := startTestRuntime(t)
	defer cleanup()

	conn := dialPTY(t, baseURL, url.Values{"shell": []string{"/bin/sh"}})

	// Close immediately. A clean teardown means no dangling shell
	// process or pty file descriptor — we can't easily assert that
	// from here, but if the runtime panics or hangs, the test
	// process would too and CI would catch it.
	_ = conn.Close()

	// Brief settle so the runtime's deferred cleanup runs.
	time.Sleep(200 * time.Millisecond)

	// Quickest sanity check: runtime is still alive and accepting
	// new connections.
	_ = context.Background()
	conn2, _, err := websocket.DefaultDialer.Dial(baseURL+"/pty?shell=/bin/sh", nil)
	if err != nil {
		t.Fatalf("runtime crashed after first session close: %v", err)
	}
	conn2.Close()
}

// buildPTYCommand decides whether to spawn the shell directly or wrap
// it in tmux. Mirrors the Router-side bridge.go's buildShellCommand
// behavior so both transports produce the same resume property.
func TestBuildPTYCommand_NoSessionRunsShellDirectly(t *testing.T) {
	got := buildPTYCommand(PTYRequest{Shell: "/bin/bash"})
	if len(got) != 2 || got[0] != "/bin/bash" || got[1] != "-l" {
		t.Errorf("expected [/bin/bash -l], got %q", got)
	}
}

func TestBuildPTYCommand_EmptyShellDefaultsToBash(t *testing.T) {
	got := buildPTYCommand(PTYRequest{})
	if got[0] != "/bin/bash" {
		t.Errorf("expected /bin/bash default, got %q", got)
	}
}

func TestBuildPTYCommand_WithSessionUsesTmuxWrap(t *testing.T) {
	got := buildPTYCommand(PTYRequest{Shell: "/bin/bash", Session: "agenttier-sb-1"})
	if len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-c" {
		t.Fatalf("expected /bin/sh -c <wrapper>, got %q", got)
	}
	wrapper := got[2]
	if !strings.Contains(wrapper, "command -v tmux") {
		t.Errorf("wrapper missing tmux check: %s", wrapper)
	}
	if !strings.Contains(wrapper, "exec tmux new-session -A -s 'agenttier-sb-1' -- '/bin/bash' -l") {
		t.Errorf("wrapper missing tmux wrap: %s", wrapper)
	}
	if !strings.Contains(wrapper, "exec '/bin/bash' -l") {
		t.Errorf("wrapper missing fallback: %s", wrapper)
	}
}

func TestBuildPTYCommand_HostileSessionEscaped(t *testing.T) {
	// Same hostile-input check as the Router-side bridge_test.go's
	// TestBuildShellCommand_RefusesInjection. The wrapper must
	// contain the escaped form of the single quote, never an
	// unescaped one outside of a quoted region.
	got := buildPTYCommand(PTYRequest{
		Shell:   "/bin/bash",
		Session: `sb-1'; rm -rf /; #`,
	})
	wrapper := got[2]
	if !strings.Contains(wrapper, `'\''`) {
		t.Errorf("expected escaped single quotes: %s", wrapper)
	}
}

func TestPTYShellQuote(t *testing.T) {
	cases := map[string]string{
		"":          "''",
		"plain":     "'plain'",
		"O'Brien":   `'O'\''Brien'`,
		"/bin/bash": "'/bin/bash'",
	}
	for in, want := range cases {
		got := ptyShellQuote(in)
		if got != want {
			t.Errorf("ptyShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
