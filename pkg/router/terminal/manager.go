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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// DefaultReconnectTTL is how long a disconnected session remains available for reconnection.
	DefaultReconnectTTL = 30 * time.Second

	// CleanupInterval is how often the manager checks for expired sessions.
	CleanupInterval = 10 * time.Second
)

// Manager tracks all active terminal sessions and supports reconnection.
type Manager struct {
	sessions     map[string]*Session
	bySandbox    map[string][]*Session // sandboxID -> sessions
	mu           sync.RWMutex
	reconnectTTL time.Duration
	logger       *slog.Logger
}

// NewManager creates a new session manager.
func NewManager(logger *slog.Logger) *Manager {
	m := &Manager{
		sessions:     make(map[string]*Session),
		bySandbox:    make(map[string][]*Session),
		reconnectTTL: DefaultReconnectTTL,
		logger:       logger,
	}

	// Start cleanup goroutine
	go m.cleanupLoop()

	return m
}

// CreateSession registers a new terminal session.
func (m *Manager) CreateSession(sandboxID, namespace, userID, email, podName, shell string, conn *websocket.Conn, isViewer bool) *Session {
	sessionID := generateSessionID()

	session := NewSession(sessionID, sandboxID, namespace, userID, email, podName, shell, conn, isViewer)

	m.mu.Lock()
	m.sessions[sessionID] = session
	m.bySandbox[sandboxID] = append(m.bySandbox[sandboxID], session)
	m.mu.Unlock()

	m.logger.Info("session created",
		"sessionId", sessionID,
		"sandboxId", sandboxID,
		"userId", userID,
		"isViewer", isViewer,
	)

	return session
}

// GetSession retrieves a session by ID.
func (m *Manager) GetSession(sessionID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, ok := m.sessions[sessionID]
	return session, ok
}

// Reconnect attempts to reconnect a disconnected session.
func (m *Manager) Reconnect(sessionID string, conn *websocket.Conn) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	if !session.IsDisconnected() {
		return nil, fmt.Errorf("session %s is not disconnected", sessionID)
	}

	// Check if within reconnection window
	if time.Since(session.disconnectedAt) > m.reconnectTTL {
		// Session expired — remove it
		delete(m.sessions, sessionID)
		m.removeSandboxSession(session.SandboxID, sessionID)
		return nil, fmt.Errorf("session %s expired (disconnected for %s)", sessionID, time.Since(session.disconnectedAt))
	}

	session.Reconnect(conn)
	m.logger.Info("session reconnected", "sessionId", sessionID, "sandboxId", session.SandboxID)

	return session, nil
}

// DisconnectSession marks a session as disconnected (starts reconnection timer).
func (m *Manager) DisconnectSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return
	}

	session.MarkDisconnected()
	m.logger.Info("session disconnected", "sessionId", sessionID, "sandboxId", session.SandboxID)
}

// RemoveSession permanently removes a session.
func (m *Manager) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return
	}

	delete(m.sessions, sessionID)
	m.removeSandboxSession(session.SandboxID, sessionID)

	m.logger.Info("session removed",
		"sessionId", sessionID,
		"sandboxId", session.SandboxID,
		"duration", session.Duration().String(),
		"bytesIn", session.BytesIn,
		"bytesOut", session.BytesOut,
	)
}

// GetSandboxSessions returns all active sessions for a sandbox.
func (m *Manager) GetSandboxSessions(sandboxID string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := m.bySandbox[sandboxID]
	// Return a copy to avoid race conditions
	result := make([]*Session, len(sessions))
	copy(result, sessions)
	return result
}

// TerminateSandboxSessions closes all sessions for a sandbox (on stop/delete).
func (m *Manager) TerminateSandboxSessions(sandboxID string, reason CloseReason) {
	m.mu.Lock()
	sessions := m.bySandbox[sandboxID]
	delete(m.bySandbox, sandboxID)
	m.mu.Unlock()

	for _, session := range sessions {
		session.SendClose(reason, 4001)
		session.Close()

		m.mu.Lock()
		delete(m.sessions, session.ID)
		m.mu.Unlock()
	}

	if len(sessions) > 0 {
		m.logger.Info("terminated sandbox sessions",
			"sandboxId", sandboxID,
			"reason", reason,
			"count", len(sessions),
		)
	}
}

// TerminateUserSessions closes all sessions for a specific user on a sandbox (on revoke).
func (m *Manager) TerminateUserSessions(sandboxID, userID string, reason CloseReason) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions := m.bySandbox[sandboxID]
	var remaining []*Session

	for _, session := range sessions {
		if session.UserID == userID {
			session.SendClose(reason, 4003)
			session.Close()
			delete(m.sessions, session.ID)
		} else {
			remaining = append(remaining, session)
		}
	}

	m.bySandbox[sandboxID] = remaining
}

// ActiveSessionCount returns the total number of active sessions.
func (m *Manager) ActiveSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// cleanupLoop periodically removes expired disconnected sessions.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		m.cleanup()
	}
}

func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, session := range m.sessions {
		if session.IsDisconnected() && time.Since(session.disconnectedAt) > m.reconnectTTL {
			delete(m.sessions, id)
			m.removeSandboxSession(session.SandboxID, id)
			m.logger.Info("expired disconnected session", "sessionId", id)
		}
	}
}

func (m *Manager) removeSandboxSession(sandboxID, sessionID string) {
	sessions := m.bySandbox[sandboxID]
	for i, s := range sessions {
		if s.ID == sessionID {
			m.bySandbox[sandboxID] = append(sessions[:i], sessions[i+1:]...)
			break
		}
	}
	if len(m.bySandbox[sandboxID]) == 0 {
		delete(m.bySandbox, sandboxID)
	}
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
