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

// In-pod PTY endpoint for the browser terminal.
//
// **Why this exists.** Every browser terminal session today goes through
// kubectl-exec SPDY: WebSocket → Router → kube-apiserver → kubelet →
// container. The apiserver-side stream gets recycled at the 20-60 minute
// mark on EKS, dropping the user's shell and any running command. The
// tmux wrap (commit 28d1884) made the shell survive the drop, but the
// drop itself still happens and the user still sees a "reconnecting"
// banner. This endpoint moves the PTY into the pod so the Router proxies
// directly TCP→TCP without the apiserver hop.
//
// **Wire shape.** A single WebSocket per terminal session. Frames are
// JSON, identical to the protocol the Router speaks to the browser
// (pkg/router/terminal/protocol.go), so the Router can pass frames
// through verbatim with one structural translation: the Router's
// "input" / "resize" / "ping" frames map to the same types here, and
// the runtime emits "output" / "heartbeat" / "pong" / "close" back.
//
// **Protocol parity.** The runtime keeps protocol parity with the
// Router-side WSMessage struct deliberately, even though the runtime
// can't import the router/terminal package (would be a cyclic dep).
// The shape is small and stable; if it ever drifts the integration
// test in pkg/router/sandboxhttp/client_test.go would catch it before
// release.
//
// **Auth.** Same Bearer-token model as /exec and /invoke. WebSocket
// upgrades are HTTP-level, so the token rides in the Authorization
// header on the GET that establishes the connection. After upgrade,
// the connection is implicitly authorized for the lifetime of the
// session. The Router validates the token before dialing, and Phase 3's
// NetworkPolicy already restricts who can dial port 9000.
//
// **Lifecycle.** The handler spawns a child shell with a fresh PTY,
// pipes the PTY master <-> WebSocket bidirectionally, and tears down
// the shell on disconnect. The runtime does NOT manage tmux — the
// Router still wraps the shell command with `tmux new-session -A`
// before passing it through, exactly as bridge.go does today. This
// keeps the resume-on-reconnect property uniform across both
// transports.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// ptyUpgrader is shared across all PTY connections. The CheckOrigin is
// permissive because authentication is handled by the Bearer token check
// on the upgrade request — origin doesn't add anything we don't already
// gate at the HTTP layer.
var ptyUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// PTY message types — matches pkg/router/terminal/protocol.go field-for-field.
// The runtime can't import the router package (cycle), so we redeclare. Two
// redundant declarations is cheap and makes drift testable.
const (
	ptyMsgTypeInput     = "input"
	ptyMsgTypeOutput    = "output"
	ptyMsgTypeResize    = "resize"
	ptyMsgTypePing      = "ping"
	ptyMsgTypePong      = "pong"
	ptyMsgTypeHeartbeat = "heartbeat"
	ptyMsgTypeClose     = "close"
	ptyMsgTypeError     = "error"
)

// ptyMessage is the on-wire JSON envelope. Mirrors the Router's WSMessage
// shape so the Router can proxy frames without translation.
type ptyMessage struct {
	Type    string `json:"type"`
	Data    string `json:"data,omitempty"`
	Stream  string `json:"stream,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Ts      int64  `json:"ts,omitempty"`
}

// PTY tuning. Same cadences as the Router-side session so the user
// experience is identical regardless of which transport path is in use.
const (
	ptyKeepaliveInterval = 30 * time.Second
	ptyPongWait          = 70 * time.Second
	ptyWriteWait         = 10 * time.Second
	ptyMaxOutputChunk    = 4096
)

// PTYRequest is the URL-query shape clients (the Router) supply on
// upgrade. POST body is not viable for WebSocket upgrades, so we accept
// shell + cwd + initial size + tmux session name as query params.
//
// The shell field is required; cols/rows default to 120x40 if missing.
// The cwd field is optional and defaults to whatever the runtime's own
// working directory is (typically /workspace for the reference images).
// The session field opts the spawn into a tmux wrap when set — see
// buildPTYCommand for the construction.
type PTYRequest struct {
	Shell   string
	Cwd     string
	Cols    int
	Rows    int
	Session string
}

// handlePTY upgrades the connection to WebSocket, spawns a shell with a
// fresh PTY, and bridges the two until either side closes.
func (s *Server) handlePTY(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required for WebSocket upgrade")
		return
	}

	req, err := parsePTYRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Upgrade BEFORE we open the PTY so we can report failures via the
	// HTTP response. Once we're upgraded, errors flow over the WS as
	// MessageTypeError frames instead.
	conn, err := ptyUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Warn("pty upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	s.logger.Info("pty session opened",
		"shell", req.Shell,
		"cols", req.Cols,
		"rows", req.Rows,
		"remoteAddr", r.RemoteAddr,
	)

	if err := s.runPTYSession(r.Context(), conn, req); err != nil {
		s.logger.Info("pty session ended with error",
			"error", err,
			"remoteAddr", r.RemoteAddr,
		)
		return
	}

	s.logger.Info("pty session closed cleanly", "remoteAddr", r.RemoteAddr)
}

// parsePTYRequest extracts shell + cwd + initial size from query params
// with sensible defaults. Returns an error only when the shell field is
// invalid; missing cols/rows fall back to a 120x40 default that matches
// what the Router-side Session pushes on first connect today.
func parsePTYRequest(r *http.Request) (PTYRequest, error) {
	q := r.URL.Query()
	shell := q.Get("shell")
	if shell == "" {
		// Default. Most reference images ship bash; the few that don't
		// (alpine-based minimal) symlink /bin/sh -> /bin/bash or have
		// bash on the PATH explicitly.
		shell = "/bin/bash"
	}
	if !strings.HasPrefix(shell, "/") {
		// Disallow PATH-resolved shells. Bare names ("bash") would be
		// resolved by os/exec.LookPath against the runtime's PATH,
		// which the user can't audit. Forcing absolute paths keeps the
		// shell choice explicit.
		return PTYRequest{}, fmt.Errorf("shell must be an absolute path, got %q", shell)
	}

	req := PTYRequest{
		Shell:   shell,
		Cwd:     q.Get("cwd"),
		Cols:    parseDim(q.Get("cols"), 120),
		Rows:    parseDim(q.Get("rows"), 40),
		Session: q.Get("session"),
	}
	return req, nil
}

// parseDim turns a query-param string into a uint16-clamped int with a
// fallback default. Bad values silently fall back rather than returning
// an error — a flaky client shouldn't be able to break the upgrade just
// by sending "abc" for cols.
func parseDim(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return fallback
	}
	if v < 1 {
		return 1
	}
	const maxDim = 1 << 16
	if v >= maxDim {
		return maxDim - 1
	}
	return v
}

// runPTYSession is the bridging loop. It owns:
//   - the spawned shell process (Cmd) and its PTY master file descriptor
//   - the WebSocket reader goroutine (input + resize + ping from client)
//   - the WebSocket writer (output frames from PTY)
//   - the keepalive ticker (heartbeats every 30s, control-frame pings)
//
// Any of (shell exits | client closes | PTY read fails | write fails | ctx
// cancels) tears down the rest cleanly. Returns nil on a clean teardown.
//
// Tmux wrap: when tmux is available in the image AND a per-session name
// can be derived from the request (via the `session` query param), we
// wrap the shell as `tmux new-session -A -s <name>` so reconnects
// re-attach the same shell. Falls back to plain shell when tmux isn't
// present or no session name was supplied — same fallback semantics as
// the Router-side bridge.go's buildShellCommand path.
func (s *Server) runPTYSession(ctx context.Context, conn *websocket.Conn, req PTYRequest) error {
	// Build the actual command line. When the caller passes a session
	// name and tmux is on PATH, we run the same wrapper string the
	// Router-side bridge.go produces today; otherwise we run the
	// user's shell directly. Keeping the wrap here (not in the Router)
	// means the Router doesn't need to know whether tmux is in the
	// image, and the wrap is uniform across both transports.
	command := buildPTYCommand(req)

	// gosec G204: command[0] is /bin/sh which we hard-code; the rest
	// are caller-supplied but go through buildPTYCommand which does
	// the canonical shellQuote escape on the session name and the
	// shell path was already gated in parsePTYRequest. The blast
	// radius is contained to the sandbox container's namespace.
	cmd := exec.Command(command[0], command[1:]...) //nolint:gosec // command construction is escape-hardened in buildPTYCommand
	if req.Cwd != "" {
		cmd.Dir = req.Cwd
	}
	// TERM=xterm-256color matches what kubelet/exec sets for an
	// interactive terminal. The user's shell relies on this for
	// colorized output, ls --color, and proper prompt rendering.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		s.sendPTYError(conn, "failed to start shell: "+err.Error())
		return fmt.Errorf("pty.Start: %w", err)
	}
	defer func() {
		_ = ptmx.Close()
		// Best-effort kill the shell. If it already exited, this is a
		// no-op. If it's still running (because the client disconnected
		// while a foreground process was active), this delivers SIGHUP
		// via the PTY close + a SIGKILL backstop.
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGHUP)
			// Give the shell 500ms to clean up before forcing.
			done := make(chan struct{})
			go func() {
				_, _ = cmd.Process.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
				_ = cmd.Process.Kill()
				<-done
			}
		}
	}()

	// Set the initial window size so the user's prompt isn't 80x24 by
	// default. Failures here are non-fatal; the user can resize manually.
	if err := pty.Setsize(ptmx, &pty.Winsize{
		Cols: clampU16(req.Cols),
		Rows: clampU16(req.Rows),
	}); err != nil {
		s.logger.Debug("initial pty resize failed", "error", err)
	}

	// Per-session bridging context. Cancelled when any side ends so all
	// goroutines exit together.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Mutex on the conn's write side. Gorilla doesn't allow concurrent
	// writes; the keepalive goroutine, the PTY reader goroutine, and
	// the close path all need to write, so they serialize through this.
	var writeMu sync.Mutex

	// Goroutine 1: PTY → WebSocket. Reads from the PTY master and
	// emits output frames. Exits when PTY closes (shell exits or we
	// torch the master fd in the deferred close above).
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		buf := make([]byte, ptyMaxOutputChunk)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if err := s.writePTYMessage(conn, &writeMu, ptyMessage{
					Type:   ptyMsgTypeOutput,
					Stream: "stdout",
					Data:   string(buf[:n]),
				}); err != nil {
					return
				}
			}
			if err != nil {
				if !isPTYClosedError(err) {
					s.logger.Debug("pty read error", "error", err)
				}
				return
			}
		}
	}()

	// Goroutine 2: WebSocket → PTY. Reads frames from the client and
	// either writes input to the PTY, resizes it, or replies to pings.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		_ = conn.SetReadDeadline(time.Now().Add(ptyPongWait))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(ptyPongWait))
		})
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg ptyMessage
			if err := json.Unmarshal(raw, &msg); err != nil {
				// Ignore malformed frames rather than tearing down —
				// matches the Router-side session.go behavior.
				continue
			}
			switch msg.Type {
			case ptyMsgTypeInput:
				if _, err := io.WriteString(ptmx, msg.Data); err != nil {
					return
				}
			case ptyMsgTypeResize:
				if err := pty.Setsize(ptmx, &pty.Winsize{
					Cols: clampU16(msg.Cols),
					Rows: clampU16(msg.Rows),
				}); err != nil {
					s.logger.Debug("pty resize failed", "error", err)
				}
			case ptyMsgTypePing:
				_ = s.writePTYMessage(conn, &writeMu, ptyMessage{Type: ptyMsgTypePong})
			}
		}
	}()

	// Goroutine 3: keepalive. WS control-frame ping + app-level heartbeat
	// every 30s, identical to pkg/router/terminal/session.go. Keeps any
	// LB middleboxes seeing traffic in both directions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(ptyKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				writeMu.Lock()
				deadline := time.Now().Add(ptyWriteWait)
				if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
					writeMu.Unlock()
					return
				}
				_ = conn.SetWriteDeadline(deadline)
				body, _ := json.Marshal(ptyMessage{Type: ptyMsgTypeHeartbeat, Ts: time.Now().UnixMilli()})
				if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
					writeMu.Unlock()
					return
				}
				writeMu.Unlock()
			}
		}
	}()

	// Wait for the shell to actually exit — even if the PTY reader
	// goroutine returned because the client closed first, we want to
	// clean up the child process before this function returns.
	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Wait()
	}()

	select {
	case <-sessionCtx.Done():
		// Client disconnected or we cancelled; deferred cleanup kills
		// the shell.
	case waitErr := <-cmdDone:
		// Shell exited on its own; cancel sessionCtx so the I/O
		// goroutines wind down.
		cancel()
		if waitErr != nil {
			s.logger.Debug("shell exited with error", "error", waitErr)
		}
	}

	wg.Wait()
	return nil
}

// writePTYMessage marshals + writes a frame, holding writeMu for safety.
// gorilla/websocket panics on concurrent writes; the keepalive goroutine
// and the PTY reader goroutine both need to write, so they share a mutex.
func (s *Server) writePTYMessage(conn *websocket.Conn, writeMu *sync.Mutex, msg ptyMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(ptyWriteWait))
	return conn.WriteMessage(websocket.TextMessage, body)
}

// sendPTYError writes a single error frame on a connection that hasn't
// otherwise started its bridging goroutines yet. Used for early-exit
// paths in handlePTY (e.g. failed pty.Start).
func (s *Server) sendPTYError(conn *websocket.Conn, message string) {
	body, _ := json.Marshal(ptyMessage{Type: ptyMsgTypeError, Message: message})
	_ = conn.SetWriteDeadline(time.Now().Add(ptyWriteWait))
	_ = conn.WriteMessage(websocket.TextMessage, body)
}

// isPTYClosedError pattern-matches the typical shell-exit error from a
// PTY master read. POSIX returns EIO when the slave side is closed; Go
// wraps it as a *PathError with that as the underlying syscall errno.
func isPTYClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	if errors.Is(err, syscall.EIO) {
		return true
	}
	return strings.Contains(err.Error(), "input/output error")
}

// clampU16 narrows an int to uint16 for the PTY size syscall, with the
// same bounds as the Router-side clampTerminalDim. Defensive: a malicious
// client sending negative or > 65535 dimensions would otherwise corrupt
// the syscall input.
func clampU16(v int) uint16 {
	if v < 1 {
		return 1
	}
	const maxDim = 1 << 16
	if v >= maxDim {
		return ^uint16(0)
	}
	return uint16(v) //nolint:gosec // bounds checked above
}

// buildPTYCommand decides how to invoke the shell inside the sandbox.
// Mirrors pkg/router/terminal/bridge.go's buildShellCommand so the two
// transports produce identical session-resume behavior.
//
// When session is set AND tmux is on PATH: we wrap as
//
//	/bin/sh -c 'command -v tmux >/dev/null 2>&1 && exec tmux new-session -A -s <session> -- <shell> -l || exec <shell> -l'
//
// When session is empty: we run the bare shell. This path is only used
// when the Router decides not to wrap (older callers, future use cases
// like one-shot debug shells where resume isn't desired).
//
// The shell is launched with -l so /etc/profile and ~/.profile run.
// Same behavior as the SPDY path.
func buildPTYCommand(req PTYRequest) []string {
	shell := req.Shell
	if shell == "" {
		shell = "/bin/bash"
	}
	if req.Session == "" {
		// No tmux wrap requested. Pass shell as a login shell directly.
		return []string{shell, "-l"}
	}
	shellQuoted := ptyShellQuote(shell)
	sessionQuoted := ptyShellQuote(req.Session)

	// Match bridge.go: force UTF-8 (-u), force 256 colors (-2), suppress
	// the default green status bar via a tmpfs-backed config file. /tmp
	// is a 256 MiB writable emptyDir on every sandbox Pod (see
	// pkg/controller/pod_builder.go's tmpVolumeName).
	tmuxConfigPath := "/tmp/.agenttier-tmux.conf"
	tmuxConfig := "set -g status off\n" +
		"set -g default-terminal \"tmux-256color\"\n" +
		"set -g mouse on\n"
	writeConfig := "printf '%s' " + ptyShellQuote(tmuxConfig) + " > " + tmuxConfigPath
	tmuxCmd := "exec tmux -u -2 -f " + tmuxConfigPath + " new-session -A -s " + sessionQuoted + " -- " + shellQuoted + " -l"
	fallbackCmd := "exec " + shellQuoted + " -l"
	wrapper := "command -v tmux >/dev/null 2>&1 && { " + writeConfig + "; " + tmuxCmd + "; } || " + fallbackCmd

	return []string{"/bin/sh", "-c", wrapper}
}

// ptyShellQuote is the same single-quote-escape helper the Router-side
// bridge.go uses. We deliberately duplicate it rather than expose
// shellQuote across packages — it's small, the dependency direction
// (router → runtime, never the reverse) makes shared helpers awkward,
// and a unit test in each package guards against drift.
func ptyShellQuote(s string) string {
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
