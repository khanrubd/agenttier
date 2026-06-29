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

// terminalAuthFixture builds a Server with the given DevAuth setting and NO
// OIDC issuer configured (oidcValidator stays nil), mirroring the production
// "API-key-only / fail-closed" posture.
func terminalAuthFixture(t *testing.T, devAuth bool) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, agenttierv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	return NewServer(&Config{ListenAddr: ":0", DevAuth: devAuth}, c, nil)
}

func wsRequest(s *Server, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// TestTerminalWS_FailsClosed_NoDevAuthNoToken is the P0 regression guard:
// with dev-auth OFF and no OIDC issuer, a tokenless WebSocket upgrade must be
// rejected 401 BEFORE any sandbox lookup — never granted a blanket-admin
// shell the way the pre-v0.6.5 OIDCIssuerURL=="" gate did.
func TestTerminalWS_FailsClosed_NoDevAuthNoToken(t *testing.T) {
	s := terminalAuthFixture(t, false)

	rec := wsRequest(s, "/ws/terminal/any-sandbox")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (fail closed), got %d body=%s", rec.Code, rec.Body.String())
	}
	// A 404 would mean auth passed and we reached the sandbox lookup — i.e.
	// the bug. Assert we never got that far.
	if rec.Code == http.StatusNotFound {
		t.Fatal("SECURITY: auth was bypassed — reached sandbox lookup without a token")
	}
}

// TestTerminalWS_FailsClosed_NoDevAuthWithToken: even with a token, when no
// OIDC validator is configured the JWT path fails closed (validateJWT returns
// "not configured"), so no admin shell is granted.
func TestTerminalWS_FailsClosed_NoDevAuthWithToken(t *testing.T) {
	s := terminalAuthFixture(t, false)

	rec := wsRequest(s, "/ws/terminal/any-sandbox?token=forged.jwt.value")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for an unverifiable token, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestTerminalWS_DevAuth_Authenticates: with dev-auth ON, auth passes and the
// handler proceeds to the sandbox lookup (404 for a missing sandbox). This
// proves the explicit dev path still works for the live dev cluster.
func TestTerminalWS_DevAuth_Authenticates(t *testing.T) {
	s := terminalAuthFixture(t, true)

	rec := wsRequest(s, "/ws/terminal/missing-sandbox")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (auth passed, sandbox missing), got %d body=%s", rec.Code, rec.Body.String())
	}
}
