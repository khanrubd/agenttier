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
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/tools/remotecommand"
)

// Keepalive tuning.
//
//   - KeepaliveInterval: how often we send a WS control ping and an app-level
//     heartbeat. 30s is well below the 60s default Classic ELB idle timeout
//     and the 60s default ALB idle timeout, so any correctly-configured LB
//     will see traffic before timing out.
//   - PongWait: how long we wait for a pong after a ping before considering
//     the socket dead. On timeout the read side fails and the session closes.
//   - WriteWait: maximum time a write operation may block. Keeps a slow
//     client from blocking the exec stream indefinitely.
const (
	KeepaliveInterval = 30 * time.Second
	PongWait          = 70 * time.Second
	WriteWait         = 10 * time.Second
)

// Session represents an active terminal session bridging a WebSocket to a K8s exec stream.
type Session struct {
	ID         string
	SandboxID  string
	Namespace  string
	UserID     string
	UserEmail  string
	PodName    string
	Shell      string
	Conn       *websocket.Conn
	LastActive time.Time
	CreatedAt  time.Time
	BytesIn    int64
	BytesOut   int64
	IsViewer   bool // Read-only spectator mode

	// Internal state
	sizeQueue      *TerminalSizeQueue
	mu             sync.Mutex
	closed         bool
	disconnectedAt time.Time
}

// NewSession creates a new terminal session.
func NewSession(id, sandboxID, namespace, userID, email, podName, shell string, conn *websocket.Conn, isViewer bool) *Session {
	s := &Session{
		ID:         id,
		SandboxID:  sandboxID,
		Namespace:  namespace,
		UserID:     userID,
		UserEmail:  email,
		PodName:    podName,
		Shell:      shell,
		Conn:       conn,
		LastActive: time.Now(),
		CreatedAt:  time.Now(),
		IsViewer:   isViewer,
		sizeQueue:  NewTerminalSizeQueue(),
	}
	// Push a reasonable default size — will be overridden by the first resize from the client
	s.sizeQueue.Push(remotecommand.TerminalSize{Width: 120, Height: 40})
	return s
}

// Read implements io.Reader — reads input from the WebSocket connection.
// For viewer sessions, input is discarded (spectator mode).
func (s *Session) Read(p []byte) (int, error) {
	for {
		_, rawMsg, err := s.Conn.ReadMessage()
		if err != nil {
			return 0, err
		}

		msg, err := ParseMessage(rawMsg)
		if err != nil {
			continue // Skip malformed messages
		}

		s.updateActivity()

		switch msg.Type {
		case MessageTypeInput:
			if s.IsViewer {
				continue // Viewers cannot send input (spectator mode)
			}
			data := []byte(msg.Data)
			s.BytesIn += int64(len(data))
			n := copy(p, data)
			return n, nil

		case MessageTypeResize:
			// Cols/Rows come over JSON as ints; clamp to uint16 to satisfy the
			// exec protocol and guard against a malicious client sending a
			// negative or > 65535 value. Anything out of range is clamped to
			// a safe default so the terminal stays usable.
			cols := clampTerminalDim(msg.Cols)
			rows := clampTerminalDim(msg.Rows)
			s.sizeQueue.Push(remotecommand.TerminalSize{
				Width:  cols,
				Height: rows,
			})
			continue // Don't return data for resize messages

		case MessageTypePing:
			s.sendPong()
			continue
		}
	}
}

// Write implements io.Writer — sends output to the WebSocket connection.
func (s *Session) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, io.ErrClosedPipe
	}

	data, err := MarshalOutput("stdout", string(p))
	if err != nil {
		return 0, err
	}

	s.BytesOut += int64(len(p))
	_ = s.Conn.SetWriteDeadline(time.Now().Add(WriteWait))
	err = s.Conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return 0, err
	}

	return len(p), nil
}

// StartKeepalive launches a goroutine that sends RFC 6455 ping control frames
// and application-level heartbeat messages on KeepaliveInterval. The goroutine
// exits when ctx is done or the session is closed.
//
// It also installs a pong handler that resets the read deadline. Callers
// should set an initial read deadline (PongWait) before invoking the bridge so
// that a peer that stops responding to pings is detected promptly.
func (s *Session) StartKeepalive(ctx context.Context, logger *slog.Logger) {
	// Reset read deadline on every pong the client sends in response to our
	// control-frame pings. The deadline is enforced by the blocked ReadMessage
	// call inside Session.Read.
	s.Conn.SetPongHandler(func(string) error {
		return s.Conn.SetReadDeadline(time.Now().Add(PongWait))
	})
	_ = s.Conn.SetReadDeadline(time.Now().Add(PongWait))

	go func() {
		ticker := time.NewTicker(KeepaliveInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.writePingAndHeartbeat(); err != nil {
					if logger != nil {
						logger.Debug("keepalive write failed, ending session",
							"sessionId", s.ID, "error", err)
					}
					s.Close()
					return
				}
			}
		}
	}()
}

// writePingAndHeartbeat sends a WebSocket control ping (detected by proxies
// and load balancers) and an application-level heartbeat message (consumed by
// the client to detect a wedged server).
func (s *Session) writePingAndHeartbeat() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return io.ErrClosedPipe
	}

	deadline := time.Now().Add(WriteWait)
	if err := s.Conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
		return err
	}

	hb, err := MarshalHeartbeat(time.Now().UnixMilli())
	if err != nil {
		return err
	}
	_ = s.Conn.SetWriteDeadline(deadline)
	return s.Conn.WriteMessage(websocket.TextMessage, hb)
}

// Next implements remotecommand.TerminalSizeQueue.
func (s *Session) Next() *remotecommand.TerminalSize {
	return s.sizeQueue.Next()
}

// SendError sends an error message to the client.
func (s *Session) SendError(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, _ := MarshalError(message)
	_ = s.Conn.WriteMessage(websocket.TextMessage, data)
}

// SendClose sends a close message to the client with a reason.
func (s *Session) SendClose(reason CloseReason, code int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, _ := MarshalClose(reason, code)
	_ = s.Conn.WriteMessage(websocket.TextMessage, data)
	s.closed = true
}

// Close terminates the session.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		_ = s.Conn.Close() // best-effort close; the peer may already have gone away
	}
}

// Duration returns how long the session has been active.
func (s *Session) Duration() time.Duration {
	return time.Since(s.CreatedAt)
}

// IsDisconnected returns true if the WebSocket is disconnected.
func (s *Session) IsDisconnected() bool {
	return !s.disconnectedAt.IsZero()
}

// MarkDisconnected marks the session as disconnected (for reconnection window).
func (s *Session) MarkDisconnected() {
	s.disconnectedAt = time.Now()
}

// Reconnect reattaches a new WebSocket connection to this session.
func (s *Session) Reconnect(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Conn = conn
	s.disconnectedAt = time.Time{}
	s.closed = false
	_ = s.Conn.SetReadDeadline(time.Now().Add(PongWait))
}

func (s *Session) updateActivity() {
	s.LastActive = time.Now()
}

func (s *Session) sendPong() {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, _ := json.Marshal(WSMessage{Type: MessageTypePong})
	_ = s.Conn.WriteMessage(websocket.TextMessage, data)
}

// --- Terminal Size Queue ---

// clampTerminalDim turns a caller-supplied terminal dimension into a valid
// uint16 value for the exec protocol. Negative or zero values are replaced
// with 1 (minimum sensible column/row count) and values above uint16 max are
// capped. gosec's G115 int→uint16 cast warning is addressed by doing the
// range check in pure Go before the conversion.
func clampTerminalDim(v int) uint16 {
	if v < 1 {
		return 1
	}
	const maxDim = 1 << 16
	if v >= maxDim {
		return ^uint16(0)
	}
	return uint16(v)
}

// TerminalSizeQueue implements remotecommand.TerminalSizeQueue using a channel.
type TerminalSizeQueue struct {
	ch chan remotecommand.TerminalSize
}

// NewTerminalSizeQueue creates a new terminal size queue.
func NewTerminalSizeQueue() *TerminalSizeQueue {
	return &TerminalSizeQueue{
		ch: make(chan remotecommand.TerminalSize, 1),
	}
}

// Push adds a new terminal size to the queue.
func (q *TerminalSizeQueue) Push(size remotecommand.TerminalSize) {
	// Non-blocking push — drop old size if channel is full
	select {
	case q.ch <- size:
	default:
		// Drain and push new
		select {
		case <-q.ch:
		default:
		}
		q.ch <- size
	}
}

// Next blocks until a new terminal size is available.
func (q *TerminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.ch
	if !ok {
		return nil
	}
	return &size
}
