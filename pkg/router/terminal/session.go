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
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/client-go/tools/remotecommand"
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
			s.sizeQueue.Push(remotecommand.TerminalSize{
				Width:  uint16(msg.Cols),
				Height: uint16(msg.Rows),
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
	err = s.Conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		return 0, err
	}

	return len(p), nil
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
		s.Conn.Close()
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
