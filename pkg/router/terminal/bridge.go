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
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"
)

// Bridge connects a terminal Session (WebSocket) to a Kubernetes pod exec stream (SPDY).
type Bridge struct {
	clientset  kubernetes.Interface
	restConfig *rest.Config
	logger     *slog.Logger
}

// NewBridge creates a new PTY bridge.
func NewBridge(clientset kubernetes.Interface, restConfig *rest.Config, logger *slog.Logger) *Bridge {
	return &Bridge{
		clientset:  clientset,
		restConfig: restConfig,
		logger:     logger,
	}
}

// Connect establishes the exec stream and bridges it to the session's WebSocket.
// This blocks until the session ends (disconnect, sandbox stop, or error).
//
// Shell command construction:
// We try to wrap the shell in `tmux new-session -A -s agenttier-<sandboxId>`
// so the bash process survives SPDY drops and apiserver-side stream churn.
// tmux daemonizes itself, decoupling the shell lifetime from the kubectl exec
// parent. When the WebSocket reconnects (every 20-60 minutes is typical on
// EKS), the new exec re-attaches the same tmux session — the user's shell
// state, environment, and any running command (long-running downloads, build
// processes) keep going.
//
// If tmux isn't installed in the image (older sandboxes, custom images), we
// fall back to plain shell silently. This keeps existing pods working without
// a forced rebuild — the only regression for those is the same session-loss
// behavior they had before this change.
func (b *Bridge) Connect(ctx context.Context, session *Session) error {
	b.logger.Info("establishing exec stream",
		"sessionId", session.ID,
		"pod", session.PodName,
		"namespace", session.Namespace,
		"shell", session.Shell,
	)

	command := buildShellCommand(session)

	// Build the exec request
	req := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(session.PodName).
		Namespace(session.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "sandbox",
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	// Create SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	// Stream bridges the WebSocket (session) to the SPDY exec stream.
	// session implements io.Reader (reads from WebSocket → stdin)
	// session implements io.Writer (writes from stdout → WebSocket)
	// session.sizeQueue implements TerminalSizeQueue (resize events)
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             session,
		Stdout:            session,
		Stderr:            session, // stderr merged with stdout for TTY
		Tty:               true,
		TerminalSizeQueue: session,
	})

	if err != nil {
		b.logger.Error("exec stream ended with error",
			"sessionId", session.ID,
			"error", err,
		)
		return fmt.Errorf("exec stream error: %w", err)
	}

	b.logger.Info("exec stream ended normally", "sessionId", session.ID)
	return nil
}

// ExecCommand runs a non-interactive command in a pod and returns the output.
func (b *Bridge) ExecCommand(ctx context.Context, namespace, podName, container string, command []string, timeout int) (*CommandResult, error) {
	req := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	stdout := &limitedBuffer{maxSize: 1 << 20} // 1MB max
	stderr := &limitedBuffer{maxSize: 1 << 20}

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	})

	result := &CommandResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: 0,
	}

	if err != nil {
		result.ExitCode = extractExitCode(err)
		// extractExitCode returns -1 for non-CodeExitError failures. Map
		// those to 1 so callers that just check `ExitCode != 0` keep working.
		if result.ExitCode == -1 {
			result.ExitCode = 1
		}
	}

	return result, nil
}

// CommandResult holds the output of a non-interactive command execution.
type CommandResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

// ExecCommandStream runs a non-interactive command in a pod and pipes
// stdout / stderr live to the supplied writers. Used by the agent /configure
// (install logs) and /invoke (entrypoint output) endpoints to emit SSE chunks
// as the command produces them rather than buffering to completion.
//
// Closing ctx cancels the exec — the SPDY layer drops the stream, which makes
// kubelet send SIGTERM (then SIGKILL) to the in-pod process. Callers tie ctx
// to the request context so a client disconnect terminates the agent.
//
// Returns the exit code on clean termination. On context cancel returns
// ctx.Err() and the exit code is undefined (the caller already knows the
// stream was aborted).
func (b *Bridge) ExecCommandStream(ctx context.Context, namespace, podName, container string, command []string, stdout, stderr io.Writer) (int, error) {
	return b.ExecCommandStreamWithStdin(ctx, namespace, podName, container, command, nil, stdout, stderr)
}

// ExecCommandStreamWithStdin is like ExecCommandStream but also pipes the
// supplied stdin reader into the in-pod process. Pass nil for stdin to
// behave exactly like ExecCommandStream. Used by /invoke so the request
// body reaches the entrypoint without going through `printf | base64 -d`
// (which hits ARG_MAX on payloads above ~128 KB).
func (b *Bridge) ExecCommandStreamWithStdin(ctx context.Context, namespace, podName, container string, command []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	req := b.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   command,
			Stdin:     stdin != nil,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(b.restConfig, "POST", req.URL())
	if err != nil {
		return -1, fmt.Errorf("failed to create executor: %w", err)
	}

	streamOpts := remotecommand.StreamOptions{
		Stdout: stdout,
		Stderr: stderr,
	}
	if stdin != nil {
		streamOpts.Stdin = stdin
	}
	err = exec.StreamWithContext(ctx, streamOpts)

	// Treat context cancel as the canonical "client disconnected" signal.
	// We intentionally don't try to recover the exit code here — the caller
	// will surface the cancel reason as the SSE exit event.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return -1, ctxErr
	}

	if err != nil {
		return extractExitCode(err), nil
	}
	return 0, nil
}

// limitedBuffer is a bytes.Buffer with a maximum size to prevent OOM.
type limitedBuffer struct {
	data    []byte
	maxSize int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.maxSize - len(b.data)
	if remaining <= 0 {
		return len(p), nil // Silently discard excess
	}
	if len(p) > remaining {
		b.data = append(b.data, p[:remaining]...)
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	return string(b.data)
}

// extractExitCode pulls the exit code from a remotecommand exec error. The
// SPDY layer wraps non-zero exits in `exec.CodeExitError`. Anything else
// (network blip, auth failure, ctx cancel) is treated as exit -1 so callers
// can distinguish from clean exit 0.
func extractExitCode(err error) int {
	if err == nil {
		return 0
	}
	var codeErr exec.CodeExitError
	if errors.As(err, &codeErr) {
		return codeErr.Code
	}
	return -1
}

// buildShellCommand decides how to invoke the shell inside the sandbox.
//
// When tmux is available, we wrap the shell in:
//
//	sh -c 'command -v tmux >/dev/null && exec tmux new-session -A -s agenttier-<sandbox> <shell> -l || exec <shell> -l'
//
// The `-A` flag tells tmux "attach if a session named agenttier-<sandbox>
// already exists, otherwise create a new one." That's exactly what we want
// for reconnect: the first WS connection creates the tmux session and runs
// the user's shell as the only window; every subsequent reconnect attaches
// to the same window and the user picks up exactly where they left off.
//
// `exec` replaces the wrapper sh in both branches so the user's shell is
// PID-2 with no extra layer hanging around. `-l` makes the shell a login
// shell so /etc/profile and ~/.profile are read — same behavior as the
// pre-tmux path.
//
// The presence-check is necessary because we still support older sandbox
// images that don't bake tmux in. When tmux is missing the wrapper falls
// back to `exec <shell> -l`, giving identical pre-change behavior. There's
// no flag-day, no migration, just an opportunistic upgrade per pod.
//
// Why a per-sandbox session name and not a per-user one: a sandbox is the
// security boundary. All terminal sessions on the same sandbox already
// share the same pod and PVC, so attaching to the same tmux session is
// not a privilege escalation — it's the equivalent of two SSH sessions
// `tmux attach`-ing to the same daemon, which is exactly the existing
// "share a sandbox URL" UX. Per-user sessions would defeat the resume use
// case entirely (a new browser tab would get a fresh shell, not the one
// running gdownload).
func buildShellCommand(session *Session) []string {
	shell := session.Shell
	if shell == "" {
		shell = "/bin/bash"
	}

	// Quote the shell path defensively. session.Shell is currently always
	// hardcoded to /bin/bash by the handler but if that ever becomes
	// user-configurable we don't want a shell-injection escape hatch.
	shellQuoted := shellQuote(shell)
	sessionName := "agenttier-" + session.SandboxID

	// `tmux new-session -A` attaches if the named session exists, else
	// creates it. `-u` forces UTF-8 mode so Unicode glyphs (Claude Code's
	// status badges, CJK, emoji, box-drawing chars) render correctly
	// instead of decaying to underscores. `-2` forces a 256-color
	// capability flag so colored TUIs render correctly even when the
	// inherited TERM is generic. `--` separates flags from the shell
	// argument. We pass -l so the shell behaves like a login shell.
	//
	// Status bar: tmux's default green footer is visual noise for users
	// who came in expecting a plain shell. We write a minimal config
	// file to /tmp (mounted as a writable tmpfs in the Pod) and pass it
	// via -f. Doing it this way avoids the `\;` quoting dance that
	// `tmux ... \; set-option ...` otherwise requires through `/bin/sh`.
	// The `tee` redirect is idempotent — we overwrite the file on every
	// connect since it's tiny and the pod's /tmp is per-Pod ephemeral.
	tmuxConfigPath := "/tmp/.agenttier-tmux.conf"
	tmuxConfig := "set -g status off\n" +
		"set -g default-terminal \"tmux-256color\"\n"
	writeConfig := "printf '%s' " + shellQuote(tmuxConfig) + " > " + tmuxConfigPath
	tmuxCmd := "exec tmux -u -2 -f " + tmuxConfigPath + " new-session -A -s " + shellQuote(sessionName) + " -- " + shellQuoted + " -l"
	fallbackCmd := "exec " + shellQuoted + " -l"

	// `command -v tmux` is POSIX and works in both bash and ash (Alpine).
	// Redirect to /dev/null so the result code is the only thing we use.
	wrapper := "command -v tmux >/dev/null 2>&1 && { " + writeConfig + "; " + tmuxCmd + "; } || " + fallbackCmd

	return []string{"/bin/sh", "-c", wrapper}
}

// shellQuote wraps a string in single quotes for safe interpolation into a
// /bin/sh -c command line. Internal single quotes are escaped using the
// canonical '\” break-out-and-back-in idiom. We avoid pulling in a full
// shell-escape library because we control the inputs (shell path, sandbox
// ID) and want zero new dependencies in the terminal hot path.
func shellQuote(s string) string {
	const sq = "'"
	const escapedSQ = `'\''`
	out := make([]byte, 0, len(s)+2)
	out = append(out, sq...)
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, escapedSQ...)
			continue
		}
		out = append(out, s[i])
	}
	out = append(out, sq...)
	return string(out)
}
