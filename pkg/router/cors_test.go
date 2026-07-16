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
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// corsFixture returns a Server configured with the given allowed origins.
func corsFixture(t *testing.T, allowedOrigins []string) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, agenttierv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	return NewServer(&Config{
		ListenAddr:         ":0",
		InstallNamespace:   "agenttier",
		DevAuth:            true, // simplify auth for CORS tests
		CORSAllowedOrigins: allowedOrigins,
	}, c, nil)
}

// corsRequest issues a GET /healthz with the given Origin header and returns
// the recorder. healthz is outside the auth middleware, which lets us focus
// purely on CORS behaviour without API-key plumbing.
func corsRequest(s *Server, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, r)
	return rec
}

// TestCORS_AllowedOriginIsReflected verifies that a request from an origin in
// the allowlist receives Access-Control-Allow-Origin echoing that origin.
func TestCORS_AllowedOriginIsReflected(t *testing.T) {
	s := corsFixture(t, []string{"https://dashboard.example.com"})

	rec := corsRequest(s, "https://dashboard.example.com")
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "https://dashboard.example.com" {
		t.Errorf("expected Access-Control-Allow-Origin: https://dashboard.example.com, got %q", got)
	}
}

// TestCORS_DisallowedOriginNotReflected verifies that a request from an origin
// NOT in the allowlist receives no Access-Control-Allow-Origin header, so the
// browser enforces the same-origin policy and cannot read the response.
func TestCORS_DisallowedOriginNotReflected(t *testing.T) {
	s := corsFixture(t, []string{"https://dashboard.example.com"})

	rec := corsRequest(s, "https://evil.example.com")
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("expected no Access-Control-Allow-Origin for disallowed origin, got %q", got)
	}
}

// TestCORS_WildcardNeverEmitted verifies that even if a caller passes "*" as
// an origin value, the server does not reflect it — only exact-match allowlist
// entries are accepted.
func TestCORS_WildcardNeverEmitted(t *testing.T) {
	// Even if the allowlist contained "*" by accident, the Origin header
	// from a real browser is never the literal string "*", so this test
	// confirms the match is exact and the server never emits "*" as the
	// reflected value.
	s := corsFixture(t, []string{"https://dashboard.example.com"})

	rec := corsRequest(s, "*")
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got == "*" {
		t.Error("server must never emit Access-Control-Allow-Origin: * on an authenticated API")
	}
}

// TestCORS_EmptyAllowlistDisablesCORS verifies that when CORSAllowedOrigins is
// empty (the default), no CORS headers are emitted regardless of the Origin.
func TestCORS_EmptyAllowlistDisablesCORS(t *testing.T) {
	s := corsFixture(t, nil)

	rec := corsRequest(s, "https://dashboard.example.com")
	got := rec.Header().Get("Access-Control-Allow-Origin")
	if got != "" {
		t.Errorf("expected no CORS headers when allowlist is empty, got Access-Control-Allow-Origin: %q", got)
	}
}

// TestCORS_PreflightAllowedOriginReturns204 verifies that an OPTIONS preflight
// from an allowed origin gets a 204 No Content with the CORS headers.
func TestCORS_PreflightAllowedOriginReturns204(t *testing.T) {
	s := corsFixture(t, []string{"https://dashboard.example.com"})

	r := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	r.Header.Set("Origin", "https://dashboard.example.com")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, r)

	if rec.Code != http.StatusNoContent {
		t.Errorf("preflight from allowed origin: expected 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.example.com" {
		t.Errorf("preflight: expected ACAO: https://dashboard.example.com, got %q", got)
	}
}

// TestCORS_PreflightDisallowedOriginReturns403 verifies that an OPTIONS
// preflight from an origin not in the allowlist is rejected with 403, so the
// browser cannot proceed with the cross-origin request.
func TestCORS_PreflightDisallowedOriginReturns403(t *testing.T) {
	s := corsFixture(t, []string{"https://dashboard.example.com"})

	r := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Errorf("preflight from disallowed origin: expected 403, got %d", rec.Code)
	}
}

// TestCORS_OptionsWithoutOriginReturns204 verifies that an OPTIONS request with
// no Origin header (a non-CORS request from curl/Postman or a same-origin
// caller, not a browser preflight) is answered 204, not 403 — matching the
// documented intent so command-line tooling keeps working.
func TestCORS_OptionsWithoutOriginReturns204(t *testing.T) {
	s := corsFixture(t, []string{"https://dashboard.example.com"})

	r := httptest.NewRequest(http.MethodOptions, "/healthz", nil)
	// No Origin header set.
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, r)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS without Origin: expected 204, got %d", rec.Code)
	}
}
