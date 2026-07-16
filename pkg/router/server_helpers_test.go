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

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/router/agent"
)

// --- isOTelExempt ---

func TestIsOTelExempt(t *testing.T) {
	exempt := []string{"/healthz", "/readyz", "/metrics", "/ws/terminal/sb-1", "/ws/anything"}
	for _, p := range exempt {
		if !isOTelExempt(p) {
			t.Errorf("expected %q to be OTel-exempt", p)
		}
	}
	notExempt := []string{"/api/v1/sandboxes", "/api/v1/sandboxes/sb-1/exec", "/"}
	for _, p := range notExempt {
		if isOTelExempt(p) {
			t.Errorf("expected %q to NOT be OTel-exempt", p)
		}
	}
}

// --- agentClaims / agentSandboxOf ---

func TestAgentClaims_NilWhenNoClaimsInContext(t *testing.T) {
	s, _ := apiKeyFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := s.agentClaims(req); got != nil {
		t.Errorf("expected nil agent claims with no router claims in context, got %+v", got)
	}
}

func TestAgentClaims_MapsFromRouterClaims(t *testing.T) {
	s, _ := apiKeyFixture(t)
	rc := &Claims{Sub: "u1", Email: "u1@example.com", Name: "User One", IsAdmin: true}
	ctx := context.WithValue(context.Background(), ClaimsContextKey, rc)
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)

	got := s.agentClaims(req)
	if got == nil {
		t.Fatal("expected non-nil agent claims")
	}
	if got.Sub != "u1" || got.Email != "u1@example.com" || got.Name != "User One" || !got.IsAdmin {
		t.Errorf("agentClaims mapped incorrectly: %+v", got)
	}
}

func TestAgentSandboxOf_NilClaimsIsUnauthenticated(t *testing.T) {
	s, _ := apiKeyFixture(t)
	_, err := s.agentSandboxOf(context.Background(), "sb-1", nil)
	if err == nil {
		t.Fatal("expected an error for nil agent claims")
	}
}

func TestAgentSandboxOf_OwnerCanAccess(t *testing.T) {
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-agent-owned", Namespace: "default"},
		Spec:       agenttierv1alpha1.SandboxSpec{CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "owner-1"}},
	}
	s, _ := statusSubresourceFixture(t, sb)

	got, err := s.agentSandboxOf(context.Background(), "sb-agent-owned", &agent.Claims{Sub: "owner-1"})
	if err != nil {
		t.Fatalf("expected owner access, got error: %v", err)
	}
	if got.Name != "sb-agent-owned" {
		t.Errorf("expected sb-agent-owned, got %s", got.Name)
	}
}

func TestAgentSandboxOf_NonOwnerDenied(t *testing.T) {
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-agent-notowned", Namespace: "default"},
		Spec:       agenttierv1alpha1.SandboxSpec{CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "owner-1"}},
	}
	s, _ := statusSubresourceFixture(t, sb)

	_, err := s.agentSandboxOf(context.Background(), "sb-agent-notowned", &agent.Claims{Sub: "someone-else"})
	if err == nil {
		t.Fatal("expected access denied for non-owner, non-admin caller")
	}
}

func TestMiddleware_ResponseWriterHijackAndFlush(t *testing.T) {
	// httptest.NewRecorder() implements neither Hijacker nor Flusher, so the
	// wrapper's "upstream doesn't support it" branches are exercised here;
	// the happy-path branches are implicitly covered by any WebSocket test
	// that hijacks through a real net/http server (see terminal tests).
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, statusCode: http.StatusOK}

	if _, _, err := rw.Hijack(); err == nil {
		t.Error("expected an error hijacking a ResponseRecorder (no Hijacker support)")
	}
	// Flush must not panic even when the upstream doesn't implement Flusher.
	rw.Flush()
}

func TestMiddleware_ResponseWriterHijackSucceedsThroughRealServer(t *testing.T) {
	// httptest.NewServer gives us a real net.Conn-backed ResponseWriter that
	// does implement http.Hijacker, covering the wrapper's success path.
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		conn, buf, err := rw.Hijack()
		if err != nil {
			t.Errorf("expected successful hijack through a real server, got %v", err)
			close(done)
			return
		}
		defer conn.Close()
		_ = buf.Flush()
		close(done)
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	_ = req.Write(conn)
	_, _ = bufio.NewReader(conn).ReadByte()
	<-done
}

// --- ratelimit small helpers ---

func TestMaxHelper(t *testing.T) {
	if max(3, 5) != 5 {
		t.Error("max(3,5) should be 5")
	}
	if max(5, 3) != 5 {
		t.Error("max(5,3) should be 5")
	}
}

func TestDefaultRateLimitConfig_MatchesDocumentedThresholds(t *testing.T) {
	cfg := DefaultRateLimitConfig()
	if cfg.PerIPRate != 1.0 || cfg.PerIPBurst != 30 {
		t.Errorf("per-IP config = %+v, want 1.0/30 (60 req/min)", cfg)
	}
	if cfg.PerUserRate != 10.0 || cfg.PerUserBurst != 100 {
		t.Errorf("per-user config = %+v, want 10.0/100 (600 req/min)", cfg)
	}
}

func TestRateLimiter_EvictStaleRemovesOldEntries(t *testing.T) {
	rl := newRateLimiter(RateLimitConfig{
		PerIPRate:  1.0,
		PerIPBurst: 1,
		LimiterTTL: 0, // defaulted to 30m by newRateLimiter, but evictStale
		// with a zero cutoff base still exercises the sweep logic below by
		// directly manipulating lastSeen.
	})
	defer rl.stopCleanup()

	// Prime an entry, then force it to look stale by backdating lastSeen.
	rl.gateRequest("10.0.0.9", "")
	rl.mu.Lock()
	if e, ok := rl.byIP["10.0.0.9"]; ok {
		e.lastSeen = e.lastSeen.Add(-2 * rl.cfg.LimiterTTL)
	}
	rl.mu.Unlock()

	rl.evictStale()

	rl.mu.Lock()
	_, stillThere := rl.byIP["10.0.0.9"]
	rl.mu.Unlock()
	if stillThere {
		t.Error("expected stale entry to be evicted")
	}
}

func TestWriteRateLimitResponse_SetsRetryAfterAndStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	writeRateLimitResponse(rec, 0)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Errorf("expected Retry-After=1 for a zero/negative delay, got %q", rec.Header().Get("Retry-After"))
	}
}
