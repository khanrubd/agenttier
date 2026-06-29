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
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig configures the per-IP and per-user rate limiters. All
// fields are optional; zero values disable that layer entirely so an
// operator can opt out of either bucket.
type RateLimitConfig struct {
	// PerIPRate is the steady-state rate (events per second) for any
	// single client IP. 1.0 = 60 req/min. Zero disables IP-level
	// limiting.
	PerIPRate float64

	// PerIPBurst is the size of the IP token bucket — how many requests
	// can be served in a burst before the rate cap kicks in. Zero falls
	// back to a sensible default when PerIPRate > 0.
	PerIPBurst int

	// PerUserRate is the steady-state rate for any authenticated user
	// (Sub claim). Authenticated users typically get a higher ceiling
	// than anonymous IPs because they're identifiable and accountable.
	// Zero disables user-level limiting.
	PerUserRate float64

	// PerUserBurst is the size of the per-user token bucket.
	PerUserBurst int

	// CleanupInterval determines how often we sweep stale limiter entries
	// out of the in-memory map so a steady stream of unique IPs doesn't
	// grow memory unbounded. Defaults to 10 minutes.
	CleanupInterval time.Duration

	// LimiterTTL is how long an IP / user limiter survives without
	// activity before the cleanup pass evicts it. Defaults to 30 min.
	LimiterTTL time.Duration

	// TrustForwardedHeaders controls whether the per-IP limiter trusts the
	// X-Forwarded-For / X-Real-IP request headers to identify the client.
	// DEFAULT false (secure): the limiter keys on the real TCP peer
	// (RemoteAddr), so a client cannot mint a fresh token bucket per
	// request by spoofing a forwarding header. Set true ONLY when the
	// Router sits behind a trusted proxy/LB (e.g. the ALB) that appends a
	// reliable client IP — otherwise the per-IP throttle is bypassable.
	TrustForwardedHeaders bool
}

// DefaultRateLimitConfig returns a config matching the values documented in
// the project's "Rate limiting on Router endpoints" task: 60 req/min/IP,
// 600 req/min/user.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		PerIPRate:       1.0, // 1/sec = 60/min
		PerIPBurst:      30,
		PerUserRate:     10.0, // 10/sec = 600/min
		PerUserBurst:    100,
		CleanupInterval: 10 * time.Minute,
		LimiterTTL:      30 * time.Minute,
	}
}

// rateLimiter holds the per-IP and per-user limiter maps and a single
// goroutine that GCs stale entries. Construction always returns a non-nil
// rateLimiter even when both rates are zero — the gateRequest method is a
// fast no-op in that case so the call sites stay simple.
type rateLimiter struct {
	cfg RateLimitConfig

	mu       sync.Mutex
	byIP     map[string]*limiterEntry
	byUser   map[string]*limiterEntry
	stopOnce sync.Once
	stop     chan struct{}
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func newRateLimiter(cfg RateLimitConfig) *rateLimiter {
	if cfg.CleanupInterval == 0 {
		cfg.CleanupInterval = 10 * time.Minute
	}
	if cfg.LimiterTTL == 0 {
		cfg.LimiterTTL = 30 * time.Minute
	}
	r := &rateLimiter{
		cfg:    cfg,
		byIP:   make(map[string]*limiterEntry),
		byUser: make(map[string]*limiterEntry),
		stop:   make(chan struct{}),
	}
	if cfg.PerIPRate > 0 || cfg.PerUserRate > 0 {
		go r.cleanupLoop()
	}
	return r
}

// stopCleanup signals the cleanup goroutine to exit. Used by tests.
func (rl *rateLimiter) stopCleanup() {
	rl.stopOnce.Do(func() { close(rl.stop) })
}

// cleanupLoop sweeps idle limiters out of the maps. Without this a Router
// that's seen N unique IPs over its lifetime would hold N rate.Limiter
// values forever.
func (rl *rateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cfg.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-rl.stop:
			return
		case <-ticker.C:
			rl.evictStale()
		}
	}
}

func (rl *rateLimiter) evictStale() {
	cutoff := time.Now().Add(-rl.cfg.LimiterTTL)
	rl.mu.Lock()
	for k, e := range rl.byIP {
		if e.lastSeen.Before(cutoff) {
			delete(rl.byIP, k)
		}
	}
	for k, e := range rl.byUser {
		if e.lastSeen.Before(cutoff) {
			delete(rl.byUser, k)
		}
	}
	rl.mu.Unlock()
}

// gateRequest applies both the per-IP and per-user limits. Returns nil when
// the request should proceed and a structured error otherwise. The error's
// message is what we surface to the client; it includes Retry-After context.
//
// Order matters: per-IP runs first because we want to throttle abusive
// callers before we even look at their auth context. Per-user runs second
// and only when claims are present.
func (rl *rateLimiter) gateRequest(ip, userSub string) (allowed bool, retryAfter time.Duration) {
	// Per-IP — applies regardless of auth state.
	if rl.cfg.PerIPRate > 0 && ip != "" {
		l := rl.getOrCreate(rl.byIP, ip, rl.cfg.PerIPRate, rl.cfg.PerIPBurst)
		if !l.Allow() {
			// Reserve a single token to compute when the next allowance
			// arrives, then cancel the reservation so we don't burn the
			// quota for a request we're rejecting.
			r := l.Reserve()
			delay := r.Delay()
			r.Cancel()
			return false, delay
		}
	}

	// Per-user — only when we know who's calling. Anonymous traffic only
	// pays the IP cost above.
	if rl.cfg.PerUserRate > 0 && userSub != "" {
		l := rl.getOrCreate(rl.byUser, userSub, rl.cfg.PerUserRate, rl.cfg.PerUserBurst)
		if !l.Allow() {
			r := l.Reserve()
			delay := r.Delay()
			r.Cancel()
			return false, delay
		}
	}

	return true, 0
}

// getOrCreate fetches an existing limiter or creates a new one keyed by
// the given identifier. Updates lastSeen on every access so the cleanup
// pass sees activity.
func (rl *rateLimiter) getOrCreate(m map[string]*limiterEntry, key string, perSec float64, burst int) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if e, ok := m[key]; ok {
		e.lastSeen = now
		return e.limiter
	}
	if burst <= 0 {
		burst = max(int(perSec), 1)
	}
	l := rate.NewLimiter(rate.Limit(perSec), burst)
	m[key] = &limiterEntry{limiter: l, lastSeen: now}
	return l
}

// rateLimitMiddleware enforces the configured per-IP and per-user limits.
// Mounted before authMiddleware so unauthenticated abusers also get
// throttled — the per-user layer is a no-op in that case but the per-IP
// layer still bites.
//
// Health endpoints (/healthz, /readyz, /metrics) and WebSocket upgrades
// bypass the limiter — they have no business getting 429'd by burst
// traffic. SSE invokes count as one event per request, not one per
// streamed event, so /invoke/* is fine on the standard counter.
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	if s.rateLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bypass health probes and metrics. These get hammered by
		// kubelet and Prometheus; throttling them would hide outages.
		if isExemptFromRateLimit(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r, s.rateLimiter.cfg.TrustForwardedHeaders)
		// Per-user lookup needs the Claims, which authMiddleware sets
		// later in the chain. Mount order: rateLimit → auth → handler,
		// meaning at this point we can only enforce per-IP. Per-user
		// throttling lives in s.rateLimitAuthenticated below.
		allowed, retryAfter := s.rateLimiter.gateRequest(ip, "")
		if !allowed {
			writeRateLimitResponse(w, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitAuthenticatedMiddleware enforces the per-user (Sub claim)
// budget. Mounted after authMiddleware so claims are available. It does
// NOT re-do the IP check — that already ran in rateLimitMiddleware.
func (s *Server) rateLimitAuthenticatedMiddleware(next http.Handler) http.Handler {
	if s.rateLimiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isExemptFromRateLimit(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		claims := GetClaims(r.Context())
		if claims == nil || claims.Sub == "" {
			next.ServeHTTP(w, r) // auth not present — IP layer already ran
			return
		}
		// Skip the IP path inside gateRequest since we already paid that
		// cost in rateLimitMiddleware: pass an empty IP here.
		allowed, retryAfter := s.rateLimiter.gateRequest("", claims.Sub)
		if !allowed {
			writeRateLimitResponse(w, retryAfter)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// isExemptFromRateLimit returns true for paths that must never be throttled
// — health probes, metrics, and WebSocket terminals (whose lifetime exceeds
// any sane request rate; one connection counts as one request, not one per
// frame).
func isExemptFromRateLimit(path string) bool {
	switch {
	case path == "/healthz", path == "/readyz", path == "/metrics":
		return true
	case strings.HasPrefix(path, "/ws/"):
		return true
	}
	return false
}

// clientIP extracts the client IP used as the per-IP rate-limit key.
//
// When trustForwarded is false (the secure default), it ignores the
// client-supplied X-Forwarded-For / X-Real-IP headers and uses the real TCP
// peer (RemoteAddr) — otherwise an attacker could send a unique forged
// X-Forwarded-For per request and get a fresh token bucket every time,
// defeating the per-IP limit entirely. Set trustForwarded true only when the
// Router sits behind a trusted proxy/LB that appends a reliable client IP.
//
// The first entry in X-Forwarded-For is the original client; subsequent
// entries are intermediate proxies.
func clientIP(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// Take the first comma-separated entry.
			if idx := strings.Index(xff, ","); idx > 0 {
				return strings.TrimSpace(xff[:idx])
			}
			return strings.TrimSpace(xff)
		}
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			return strings.TrimSpace(xri)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// writeRateLimitResponse emits a 429 with a Retry-After header and a
// structured JSON body matching the existing concurrency_exceeded shape
// so SDKs can render a useful message without endpoint-specific code.
func writeRateLimitResponse(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter.Seconds() + 1) // round up so Retry-After is at least 1s
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	// G705 (XSS taint) is a false positive here: only the int `seconds` is
	// interpolated, twice, into a constant JSON template — no caller-controlled
	// string reaches the writer.
	fmt.Fprintf(w, `{"error":"rate_limited","retryAfter":%d,"message":"too many requests; retry after %d seconds"}`, seconds, seconds) //nolint:gosec
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
