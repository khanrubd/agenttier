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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// authzFixture returns two servers sharing one fake client + Secret store:
//   - admin: DevAuth on, used to mint an API key.
//   - nonAdmin: DevAuth off, so authMiddleware takes the X-API-Key path. API
//     keys never carry IsAdmin (see APIKeyValidator.ValidateKey), so the key
//     authenticates as a non-admin user.
//
// It returns the nonAdmin server and the plaintext key.
func authzFixture(t *testing.T) (*Server, string) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, agenttierv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	admin := NewServer(&Config{ListenAddr: ":0", InstallNamespace: "agenttier", DevAuth: true}, c, nil)
	body := `{"name":"non-admin-key"}`
	cr := httptest.NewRequest(http.MethodPost, "/api/v1/user/api-keys", strings.NewReader(body))
	cr.Header.Set("Content-Type", "application/json")
	cr.ContentLength = int64(len(body))
	crRec := httptest.NewRecorder()
	admin.router.ServeHTTP(crRec, cr)
	if crRec.Code != http.StatusCreated {
		t.Fatalf("mint key: expected 201, got %d body=%s", crRec.Code, crRec.Body.String())
	}
	var created struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(crRec.Body.Bytes(), &created); err != nil || created.Key == "" {
		t.Fatalf("mint key: bad response %q (%v)", crRec.Body.String(), err)
	}

	nonAdmin := NewServer(&Config{ListenAddr: ":0", InstallNamespace: "agenttier", DevAuth: false}, c, nil)
	return nonAdmin, created.Key
}

func reqWithKey(s *Server, method, target, key, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.ContentLength = int64(len(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, r)
	return rec
}

// TestAdminGatedRoutes_RejectNonAdmin is the regression guard for the
// privilege-escalation + cross-tenant-leak findings: a valid non-admin
// identity must get 403 on template mutation, audit, analytics,
// warm-pool-config, and admin-listing routes (M1).
func TestAdminGatedRoutes_RejectNonAdmin(t *testing.T) {
	s, key := authzFixture(t)

	cases := []struct {
		name, method, path, body string
	}{
		{"create template", http.MethodPost, "/api/v1/templates", `{"name":"x"}`},
		{"update template", http.MethodPut, "/api/v1/templates/x", `{"name":"x"}`},
		{"delete template", http.MethodDelete, "/api/v1/templates/x", ""},
		{"audit events", http.MethodGet, "/api/v1/audit/events", ""},
		{"usage analytics", http.MethodGet, "/api/v1/analytics/usage", ""},
		{"cost analytics", http.MethodGet, "/api/v1/analytics/costs", ""},
		{"warmpool config", http.MethodPut, "/api/v1/warmpool/config", `{"template":"x","desiredCount":1}`},
		// M1: admin aggregation routes must be admin-gated.
		{"admin list sandboxes", http.MethodGet, "/api/v1/admin/sandboxes", ""},
		{"admin list sharing", http.MethodGet, "/api/v1/admin/sharing", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := reqWithKey(s, tc.method, tc.path, key, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: expected 403 for non-admin, got %d body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

// TestTemplateRead_AllowedForNonAdmin proves the gate is scoped to mutation —
// GET /templates stays readable by any authenticated user (and confirms the
// non-admin key actually authenticates, so the 403s above are authz, not authn).
func TestTemplateRead_AllowedForNonAdmin(t *testing.T) {
	s, key := authzFixture(t)

	rec := reqWithKey(s, http.MethodGet, "/api/v1/templates", key, "")
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("GET /templates should be readable by a non-admin, got %d body=%s", rec.Code, rec.Body.String())
	}
}
