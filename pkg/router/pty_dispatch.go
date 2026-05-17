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

package router

// PTY dispatch — pick between the legacy SPDY exec path and the new
// in-pod HTTP-PTY path for the browser terminal WebSocket.
//
// **Why this is a separate file from exec_dispatch.go.** The /exec
// dispatch is request/response, single-shot. The PTY dispatch is a
// long-lived bidirectional stream. Mixing both decision trees into one
// file would muddy the executor interface (Execute returns a result;
// the PTY path takes ownership of two WebSockets and bridges them
// until either side closes). Keeping them apart matches what we already
// did for InvokeStream vs Exec at the sandboxhttp client layer.
//
// **Decision tree (mirrors dispatchExec):**
//
//  1. Sandbox has no runtime-token Secret → SPDY (existing tmux-wrap
//     path through bridge.Connect).
//  2. Token exists but pod has no IP yet → SPDY.
//  3. /healthz on the runtime fails → SPDY (with structured warn log).
//  4. Healthy runtime → HTTP-PTY: dial /pty over WebSocket, ferry
//     frames between browser ↔ runtime until either side closes.
//
// The decision is made once per session at WebSocket-upgrade time. The
// browser's auto-reconnect on drop will re-evaluate on the next
// session. There's no mid-session swap — that's not necessary because
// the in-pod runtime sits on a stable Pod IP that doesn't change
// without a pod restart, and a pod restart drops the SPDY path too.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/controller"
	"github.com/agenttier/agenttier/pkg/router/sandboxhttp"
	"github.com/agenttier/agenttier/pkg/router/terminal"
)

// dispatchTerminal picks the right transport for the browser terminal
// session and runs it. Blocks until the session ends (browser closes,
// shell exits, transport error, or context cancel).
//
// On the HTTP-PTY path, the spawned tmux session lives in the pod's
// network namespace and survives apiserver-side stream churn — the
// 20-60 minute SPDY drop the user was hitting goes away entirely.
//
// On the SPDY fallback, behavior is byte-identical to what bridge.go
// did before this dispatcher existed.
func (s *Server) dispatchTerminal(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, session *terminal.Session) error {
	transport, fallbackReason := s.pickTerminalTransport(ctx, sandbox)
	if fallbackReason != "" {
		s.logger.Info("HTTP-PTY fallback to SPDY",
			"sandbox", sandbox.Name,
			"namespace", sandbox.Namespace,
			"sessionId", session.ID,
			"reason", fallbackReason,
		)
	}
	switch t := transport.(type) {
	case *spdyTerminalTransport:
		return t.bridge.Connect(ctx, session)
	case *httpPTYTerminalTransport:
		s.logger.Info("terminal session via HTTP-PTY",
			"sandbox", sandbox.Name,
			"sessionId", session.ID,
		)
		return t.run(ctx, session)
	default:
		// Defensive — pickTerminalTransport always returns one of the
		// two concrete types above. If a future refactor adds a third
		// without updating this switch, fail loudly rather than
		// silently dropping the session.
		return fmt.Errorf("dispatchTerminal: unknown transport type %T", transport)
	}
}

// terminalTransport is the contract for the two concrete bridge
// implementations. Today there are two cases (SPDY, HTTP-PTY) and we
// dispatch via type switch; the interface is intentionally empty so
// adding a third doesn't force a method set on existing call sites.
type terminalTransport interface{}

// spdyTerminalTransport runs the legacy SPDY-exec path. The actual
// bridging is unchanged; this is just a wrapper so dispatchTerminal can
// distinguish it from the HTTP-PTY path.
type spdyTerminalTransport struct {
	bridge *terminal.Bridge
}

// httpPTYTerminalTransport drives the new in-pod /pty endpoint.
type httpPTYTerminalTransport struct {
	client  *sandboxhttp.Client
	shell   string
	cwd     string
	cols    int
	rows    int
	session string
}

// pickTerminalTransport mirrors pickExecutor's decision tree but
// returns a terminal-shaped transport rather than an executor. Same
// preconditions, same probe, same fallback reasons.
func (s *Server) pickTerminalTransport(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (terminalTransport, string) {
	spdy := &spdyTerminalTransport{bridge: s.bridge}

	if s.k8sClient == nil {
		return spdy, ""
	}

	token, err := controller.ReadRuntimeToken(ctx, s.k8sClient, sandbox.Name, sandbox.Namespace)
	if err != nil {
		return spdy, "runtime-token Secret read failed: " + err.Error()
	}
	if token == "" {
		// Deliberately not opted in. No fallback warning.
		return spdy, ""
	}

	podIP := s.lookupPodIP(ctx, sandbox)
	if podIP == "" {
		return spdy, "pod IP not yet assigned"
	}

	client := sandboxhttp.New("http://"+podIP+":"+runtimePortForTest, token)

	probeCtx, cancel := context.WithTimeout(ctx, runtimeHealthzTimeout)
	defer cancel()
	if err := client.Healthz(probeCtx); err != nil {
		return spdy, "runtime healthz failed: " + err.Error()
	}

	return &httpPTYTerminalTransport{
		client: client,
		shell:  "/bin/bash",
		// Per-sandbox tmux session name — same convention as the
		// SPDY-path's bridge.go buildShellCommand. All terminal
		// reconnects on this sandbox attach to the same tmux
		// session, so the user resumes their shell with running
		// processes intact across HTTP-PTY drops.
		session: "agenttier-" + sandbox.Name,
		// Default initial size matches what the Router-side Session
		// pushes on first connect; the browser will resize on its
		// first window-fit anyway.
		cols: 120,
		rows: 40,
	}, ""
}

// run dials the runtime's /pty endpoint over WebSocket and ferries
// frames between the browser-facing session.Conn and the runtime
// connection until either side closes.
//
// The runtime's wire format is byte-identical to the browser's
// (pkg/router/terminal/protocol.go) by design — both sides decode the
// same JSON envelope shapes. That lets us pass frames through
// verbatim without re-marshaling, with one exception: the
// keepalive/heartbeat frames the runtime emits are dropped on the way
// to the browser because the Router-side keepalive goroutine
// (Session.StartKeepalive) already produces them on its own cadence.
// Forwarding the runtime's would double up.
//
// Tmux session resume: we pass session=agenttier-<sandbox> on the
// upgrade so the runtime wraps the spawned shell in
// `tmux new-session -A -s <name>`. Reconnects re-attach to the same
// tmux server inside the pod, surviving WebSocket drops with running
// processes (gdownload, builds, long apt installs) intact.
func (t *httpPTYTerminalTransport) run(ctx context.Context, session *terminal.Session) error {
	upstream, err := t.client.DialPTY(ctx, sandboxhttp.PTYOptions{
		Shell:   t.shell,
		Cwd:     t.cwd,
		Cols:    t.cols,
		Rows:    t.rows,
		Session: t.session,
	})
	if err != nil {
		return fmt.Errorf("HTTP-PTY dial failed: %w", err)
	}
	defer upstream.Close()

	// Per-session bridging context. Cancelled when either side hangs
	// up so both forwarding goroutines wind down.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// gorilla/websocket disallows concurrent writes per connection.
	// Each side gets its own write mutex.
	var browserWriteMu sync.Mutex
	var upstreamWriteMu sync.Mutex

	var wg sync.WaitGroup

	// Browser → upstream: read frames from the browser session and
	// forward them to the runtime. Same protocol on both sides, so
	// the JSON bytes pass through verbatim.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			_, raw, err := session.Conn.ReadMessage()
			if err != nil {
				return
			}
			upstreamWriteMu.Lock()
			_ = upstream.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err = upstream.WriteMessage(websocket.TextMessage, raw)
			upstreamWriteMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	// Upstream → browser: read frames from the runtime and forward to
	// the browser, dropping heartbeat/pong frames the Router-side
	// keepalive will produce on its own cadence.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			_, raw, err := upstream.ReadMessage()
			if err != nil {
				return
			}
			// Cheap peek to drop runtime-side heartbeat / pong frames.
			// We unmarshal into a tiny header struct rather than the
			// full protocol type to avoid pulling more code into this
			// hot path.
			var hdr struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &hdr) == nil {
				switch hdr.Type {
				case "heartbeat", "pong":
					continue
				}
			}
			browserWriteMu.Lock()
			_ = session.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			err = session.Conn.WriteMessage(websocket.TextMessage, raw)
			browserWriteMu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	<-sessionCtx.Done()
	wg.Wait()
	return nil
}
