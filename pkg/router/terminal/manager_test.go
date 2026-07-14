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
	"testing"
	"time"
)

func TestManager_CreateAndGetSession(t *testing.T) {
	m := NewManager(testLogger())
	_, client, cleanup := newWSPair(t)
	defer cleanup()

	s := m.CreateSession("sbx-1", "default", "user-1", "user@example.com", "sbx-1-pod", "/bin/bash", client, false)
	if s.SandboxID != "sbx-1" {
		t.Errorf("expected sandboxID sbx-1, got %s", s.SandboxID)
	}

	got, ok := m.GetSession(s.ID)
	if !ok {
		t.Fatal("expected session to be found")
	}
	if got != s {
		t.Error("GetSession returned a different session instance")
	}

	if m.ActiveSessionCount() != 1 {
		t.Errorf("expected 1 active session, got %d", m.ActiveSessionCount())
	}
}

func TestManager_GetSession_NotFound(t *testing.T) {
	m := NewManager(testLogger())
	_, ok := m.GetSession("does-not-exist")
	if ok {
		t.Error("expected not found for unknown session ID")
	}
}

func TestManager_GetSandboxSessions(t *testing.T) {
	m := NewManager(testLogger())
	_, c1, cleanup1 := newWSPair(t)
	defer cleanup1()
	_, c2, cleanup2 := newWSPair(t)
	defer cleanup2()

	s1 := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", c1, false)
	s2 := m.CreateSession("sbx-1", "default", "user-2", "u2@example.com", "pod-1", "/bin/bash", c2, true)
	// A session on a different sandbox must not show up.
	_, c3, cleanup3 := newWSPair(t)
	defer cleanup3()
	m.CreateSession("sbx-2", "default", "user-3", "u3@example.com", "pod-2", "/bin/bash", c3, false)

	sessions := m.GetSandboxSessions("sbx-1")
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions for sbx-1, got %d", len(sessions))
	}
	ids := map[string]bool{sessions[0].ID: true, sessions[1].ID: true}
	if !ids[s1.ID] || !ids[s2.ID] {
		t.Errorf("expected sessions %s and %s, got %v", s1.ID, s2.ID, ids)
	}
}

func TestManager_GetSandboxSessions_ReturnsIndependentCopy(t *testing.T) {
	m := NewManager(testLogger())
	_, client, cleanup := newWSPair(t)
	defer cleanup()
	m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client, false)

	sessions := m.GetSandboxSessions("sbx-1")
	sessions[0] = nil // Mutating the returned slice must not affect the manager's state.

	again := m.GetSandboxSessions("sbx-1")
	if again[0] == nil {
		t.Error("GetSandboxSessions should return a defensive copy, internal state was mutated")
	}
}

func TestManager_RemoveSession(t *testing.T) {
	m := NewManager(testLogger())
	_, client, cleanup := newWSPair(t)
	defer cleanup()
	s := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client, false)

	m.RemoveSession(s.ID)

	if _, ok := m.GetSession(s.ID); ok {
		t.Error("expected session to be removed")
	}
	if len(m.GetSandboxSessions("sbx-1")) != 0 {
		t.Error("expected no sessions remaining for sbx-1 after removal")
	}
	if m.ActiveSessionCount() != 0 {
		t.Errorf("expected 0 active sessions, got %d", m.ActiveSessionCount())
	}
}

func TestManager_RemoveSession_UnknownIsNoop(t *testing.T) {
	m := NewManager(testLogger())
	// Must not panic on an unknown ID.
	m.RemoveSession("does-not-exist")
}

func TestManager_DisconnectSession_UnknownIsNoop(t *testing.T) {
	m := NewManager(testLogger())
	// Must not panic on an unknown ID.
	m.DisconnectSession("does-not-exist")
}

func TestManager_DisconnectAndReconnect(t *testing.T) {
	m := NewManager(testLogger())
	_, client, cleanup := newWSPair(t)
	defer cleanup()
	s := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client, false)

	m.DisconnectSession(s.ID)
	if !s.IsDisconnected() {
		t.Fatal("expected session to be marked disconnected")
	}

	_, newClient, cleanup2 := newWSPair(t)
	defer cleanup2()

	reconnected, err := m.Reconnect(s.ID, newClient)
	if err != nil {
		t.Fatalf("unexpected reconnect error: %v", err)
	}
	if reconnected.IsDisconnected() {
		t.Error("expected session to no longer be disconnected after Reconnect")
	}
}

func TestManager_Reconnect_UnknownSession(t *testing.T) {
	m := NewManager(testLogger())
	_, client, cleanup := newWSPair(t)
	defer cleanup()

	_, err := m.Reconnect("does-not-exist", client)
	if err == nil {
		t.Error("expected error reconnecting an unknown session")
	}
}

func TestManager_Reconnect_NotDisconnected(t *testing.T) {
	m := NewManager(testLogger())
	_, client, cleanup := newWSPair(t)
	defer cleanup()
	s := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client, false)

	_, newClient, cleanup2 := newWSPair(t)
	defer cleanup2()

	// Session was never disconnected — Reconnect must reject it.
	_, err := m.Reconnect(s.ID, newClient)
	if err == nil {
		t.Error("expected error reconnecting a session that isn't disconnected")
	}
}

func TestManager_Reconnect_ExpiredSessionIsRemoved(t *testing.T) {
	m := NewManager(testLogger())
	m.reconnectTTL = 10 * time.Millisecond
	_, client, cleanup := newWSPair(t)
	defer cleanup()
	s := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client, false)

	m.DisconnectSession(s.ID)
	time.Sleep(20 * time.Millisecond)

	_, newClient, cleanup2 := newWSPair(t)
	defer cleanup2()

	_, err := m.Reconnect(s.ID, newClient)
	if err == nil {
		t.Fatal("expected error reconnecting an expired session")
	}
	if _, ok := m.GetSession(s.ID); ok {
		t.Error("expected expired session to be removed from the manager")
	}
}

func TestManager_TerminateSandboxSessions(t *testing.T) {
	m := NewManager(testLogger())
	server1, client1, cleanup1 := newWSPair(t)
	defer cleanup1()
	server2, client2, cleanup2 := newWSPair(t)
	defer cleanup2()

	s1 := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client1, false)
	s2 := m.CreateSession("sbx-1", "default", "user-2", "u2@example.com", "pod-1", "/bin/bash", client2, false)
	_ = server1
	_ = server2

	m.TerminateSandboxSessions("sbx-1", CloseReasonSandboxStopped)

	if _, ok := m.GetSession(s1.ID); ok {
		t.Error("expected session 1 to be removed after termination")
	}
	if _, ok := m.GetSession(s2.ID); ok {
		t.Error("expected session 2 to be removed after termination")
	}
	if len(m.GetSandboxSessions("sbx-1")) != 0 {
		t.Error("expected no sessions left for sbx-1")
	}
}

func TestManager_TerminateSandboxSessions_UnknownSandboxIsNoop(t *testing.T) {
	m := NewManager(testLogger())
	// Must not panic when there are no sessions for the sandbox.
	m.TerminateSandboxSessions("does-not-exist", CloseReasonSandboxDeleted)
}

func TestManager_TerminateUserSessions(t *testing.T) {
	m := NewManager(testLogger())
	_, client1, cleanup1 := newWSPair(t)
	defer cleanup1()
	_, client2, cleanup2 := newWSPair(t)
	defer cleanup2()

	s1 := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client1, false)
	s2 := m.CreateSession("sbx-1", "default", "user-2", "u2@example.com", "pod-1", "/bin/bash", client2, false)

	m.TerminateUserSessions("sbx-1", "user-1", CloseReasonRevoked)

	if _, ok := m.GetSession(s1.ID); ok {
		t.Error("expected user-1's session to be removed")
	}
	if _, ok := m.GetSession(s2.ID); !ok {
		t.Error("expected user-2's session to remain")
	}
	remaining := m.GetSandboxSessions("sbx-1")
	if len(remaining) != 1 || remaining[0].UserID != "user-2" {
		t.Errorf("expected only user-2's session remaining, got %+v", remaining)
	}
}

func TestManager_Cleanup_RemovesExpiredDisconnectedSessions(t *testing.T) {
	m := NewManager(testLogger())
	m.reconnectTTL = 10 * time.Millisecond
	_, client, cleanup := newWSPair(t)
	defer cleanup()
	s := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", client, false)

	m.DisconnectSession(s.ID)
	time.Sleep(20 * time.Millisecond)

	m.cleanup()

	if _, ok := m.GetSession(s.ID); ok {
		t.Error("expected expired session to be cleaned up")
	}
	if len(m.GetSandboxSessions("sbx-1")) != 0 {
		t.Error("expected sbx-1 to have no sessions after cleanup")
	}
}

func TestManager_Cleanup_KeepsActiveAndRecentlyDisconnectedSessions(t *testing.T) {
	m := NewManager(testLogger())
	m.reconnectTTL = 1 * time.Hour
	_, activeClient, cleanupActive := newWSPair(t)
	defer cleanupActive()
	_, disconnectedClient, cleanupDisconnected := newWSPair(t)
	defer cleanupDisconnected()

	active := m.CreateSession("sbx-1", "default", "user-1", "u1@example.com", "pod-1", "/bin/bash", activeClient, false)
	disconnected := m.CreateSession("sbx-1", "default", "user-2", "u2@example.com", "pod-1", "/bin/bash", disconnectedClient, false)
	m.DisconnectSession(disconnected.ID)

	m.cleanup()

	if _, ok := m.GetSession(active.ID); !ok {
		t.Error("expected active session to survive cleanup")
	}
	if _, ok := m.GetSession(disconnected.ID); !ok {
		t.Error("expected recently-disconnected session (within TTL) to survive cleanup")
	}
}

func TestManager_GenerateSessionID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateSessionID()
		if id == "" {
			t.Fatal("expected non-empty session ID")
		}
		if ids[id] {
			t.Fatalf("generated duplicate session ID: %s", id)
		}
		ids[id] = true
	}
}
