/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_PerIP_AllowsBurstThenThrottles(t *testing.T) {
	rl := newRateLimiter(RateLimitConfig{
		PerIPRate:  1.0, // 1/sec steady state
		PerIPBurst: 3,
	})
	defer rl.stopCleanup()

	const ip = "10.0.0.1"

	// Burst of 3 should all pass.
	for i := 0; i < 3; i++ {
		ok, _ := rl.gateRequest(ip, "")
		if !ok {
			t.Fatalf("burst request %d unexpectedly throttled", i)
		}
	}

	// 4th request inside the same instant must hit the cap.
	ok, retryAfter := rl.gateRequest(ip, "")
	if ok {
		t.Fatal("expected 4th request to be throttled, got allowed")
	}
	if retryAfter <= 0 {
		t.Errorf("expected retryAfter > 0 on throttled request, got %v", retryAfter)
	}
}

func TestRateLimiter_PerIP_DifferentIPsAreIndependent(t *testing.T) {
	rl := newRateLimiter(RateLimitConfig{
		PerIPRate:  1.0,
		PerIPBurst: 1,
	})
	defer rl.stopCleanup()

	// First IP exhausts its burst.
	if ok, _ := rl.gateRequest("10.0.0.1", ""); !ok {
		t.Fatal("first IP first request rejected")
	}
	if ok, _ := rl.gateRequest("10.0.0.1", ""); ok {
		t.Fatal("first IP second request unexpectedly allowed (burst=1)")
	}

	// Second IP has its own bucket and must not inherit the first's
	// exhaustion. This is the test that fails if we accidentally key by
	// something other than IP.
	if ok, _ := rl.gateRequest("10.0.0.2", ""); !ok {
		t.Fatal("second IP rejected — buckets are sharing state across IPs")
	}
}

func TestRateLimiter_PerUser_BypassedWhenAnonymous(t *testing.T) {
	rl := newRateLimiter(RateLimitConfig{
		PerUserRate:  1.0,
		PerUserBurst: 1,
	})
	defer rl.stopCleanup()

	// Anonymous request — no Sub claim. The per-user layer must not
	// engage at all.
	for i := 0; i < 5; i++ {
		ok, _ := rl.gateRequest("10.0.0.1", "")
		if !ok {
			t.Errorf("anonymous request %d unexpectedly throttled by per-user limiter", i)
		}
	}
}

func TestRateLimiter_DisabledByDefault(t *testing.T) {
	// Zero config = both rates are 0. gateRequest must always allow,
	// matching today's behavior so an operator who doesn't opt in sees
	// no change.
	rl := newRateLimiter(RateLimitConfig{})
	defer rl.stopCleanup()

	for i := 0; i < 1000; i++ {
		ok, _ := rl.gateRequest("10.0.0.1", "user-1")
		if !ok {
			t.Fatalf("request %d throttled despite zero config", i)
		}
	}
}

func TestRateLimitMiddleware_ExemptHealthEndpoints(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/metrics", "/ws/terminal/sb-1"} {
		if !isExemptFromRateLimit(path) {
			t.Errorf("expected %q to be exempt from rate limiting", path)
		}
	}
	for _, path := range []string{"/api/v1/sandboxes", "/api/v1/sandboxes/sb-1/exec"} {
		if isExemptFromRateLimit(path) {
			t.Errorf("expected %q to NOT be exempt from rate limiting", path)
		}
	}
}

func TestClientIP_HonorsXForwardedFor(t *testing.T) {
	cases := []struct {
		name string
		req  *http.Request
		want string
	}{
		{
			name: "no headers — falls back to RemoteAddr",
			req:  &http.Request{RemoteAddr: "192.0.2.10:55432"},
			want: "192.0.2.10",
		},
		{
			name: "X-Forwarded-For with one entry",
			req: func() *http.Request {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("X-Forwarded-For", "203.0.113.1")
				return r
			}(),
			want: "203.0.113.1",
		},
		{
			name: "X-Forwarded-For with chained proxies — first wins",
			req: func() *http.Request {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("X-Forwarded-For", "203.0.113.1, 10.0.0.5, 10.0.0.6")
				return r
			}(),
			want: "203.0.113.1",
		},
		{
			name: "X-Real-IP fallback",
			req: func() *http.Request {
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("X-Real-IP", "203.0.113.99")
				return r
			}(),
			want: "203.0.113.99",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientIP(tc.req); got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRateLimiter_SteadyStateRefillAllowsContinuedRequests(t *testing.T) {
	// Burst of 1, rate of 100/sec → after 10ms one new token should be
	// available. Using fast-tick mode (rate=100) keeps the test under a
	// second while still exercising the refill path.
	rl := newRateLimiter(RateLimitConfig{
		PerIPRate:  100.0,
		PerIPBurst: 1,
	})
	defer rl.stopCleanup()

	const ip = "10.0.0.42"

	// First request consumes the burst.
	if ok, _ := rl.gateRequest(ip, ""); !ok {
		t.Fatal("first request unexpectedly throttled")
	}
	// Immediate second request must fail (no refill yet).
	if ok, _ := rl.gateRequest(ip, ""); ok {
		t.Fatal("immediate second request unexpectedly allowed (burst=1, no refill)")
	}
	// Wait long enough for refill (rate=100/sec → 10ms per token; allow
	// margin for scheduler jitter).
	time.Sleep(50 * time.Millisecond)
	if ok, _ := rl.gateRequest(ip, ""); !ok {
		t.Fatal("post-refill request unexpectedly throttled")
	}
}
