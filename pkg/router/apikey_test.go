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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/router/auth"
)

func apiKeyFixture(t *testing.T) (*Server, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, agenttierv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	// DevAuth so the create/list/revoke handlers see an admin "dev-user".
	s := NewServer(&Config{ListenAddr: ":0", InstallNamespace: "agenttier", DevAuth: true}, c, nil)
	return s, c
}

func TestAPIKey_CreateThenValidate_RoundTrip(t *testing.T) {
	s, c := apiKeyFixture(t)

	// Create a key via the handler.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/api-keys", strings.NewReader(`{"name":"ci"}`))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(`{"name":"ci"}`))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("invalid create response: %v", err)
	}
	if created.Key == "" || !strings.HasPrefix(created.Key, "atk_") {
		t.Fatalf("expected an atk_-prefixed plaintext key, got %q", created.Key)
	}

	// The Secret must store the HASH, never the plaintext.
	store := newSecretAPIKeyStore(c, "agenttier")
	secret := &corev1.Secret{}
	if err := c.Get(context.Background(), client.ObjectKey{Namespace: "agenttier", Name: created.ID}, secret); err != nil {
		t.Fatalf("api key secret not found: %v", err)
	}
	if strings.Contains(string(secret.Data["record"]), created.Key) {
		t.Fatal("SECURITY: plaintext key found in stored Secret")
	}
	if string(secret.Data["keyHash"]) != auth.HashAPIKey(created.Key) {
		t.Fatal("stored hash does not match the issued key")
	}

	// The validator must accept the plaintext key and reject a wrong one.
	if _, err := s.validateAPIKey(context.Background(), created.Key); err != nil {
		t.Fatalf("issued key failed validation: %v", err)
	}
	if _, err := s.validateAPIKey(context.Background(), "atk_wrong"); err == nil {
		t.Fatal("a bogus key was accepted")
	}
	_ = store
}

func TestAPIKey_RevokeRemovesIt(t *testing.T) {
	s, _ := apiKeyFixture(t)

	// Create.
	cr := httptest.NewRequest(http.MethodPost, "/api/v1/user/api-keys", strings.NewReader(`{"name":"temp"}`))
	cr.Header.Set("Content-Type", "application/json")
	cr.ContentLength = int64(len(`{"name":"temp"}`))
	crRec := httptest.NewRecorder()
	s.router.ServeHTTP(crRec, cr)
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	_ = json.Unmarshal(crRec.Body.Bytes(), &created)

	// Revoke.
	dr := httptest.NewRequest(http.MethodDelete, "/api/v1/user/api-keys/"+created.ID, nil)
	drRec := httptest.NewRecorder()
	s.router.ServeHTTP(drRec, dr)
	if drRec.Code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d body=%s", drRec.Code, drRec.Body.String())
	}

	// Key no longer validates.
	if _, err := s.validateAPIKey(context.Background(), created.Key); err == nil {
		t.Fatal("revoked key still validates")
	}
}

func TestAPIKey_RevokeEvictsCachedKey(t *testing.T) {
	s, _ := apiKeyFixture(t)

	// Create.
	body := `{"name":"cached"}`
	cr := httptest.NewRequest(http.MethodPost, "/api/v1/user/api-keys", strings.NewReader(body))
	cr.Header.Set("Content-Type", "application/json")
	cr.ContentLength = int64(len(body))
	crRec := httptest.NewRecorder()
	s.router.ServeHTTP(crRec, cr)
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	_ = json.Unmarshal(crRec.Body.Bytes(), &created)

	// Validate once to prime the validator cache.
	if _, err := s.validateAPIKey(context.Background(), created.Key); err != nil {
		t.Fatalf("prime validate: %v", err)
	}

	// Revoke.
	dr := httptest.NewRequest(http.MethodDelete, "/api/v1/user/api-keys/"+created.ID, nil)
	drRec := httptest.NewRecorder()
	s.router.ServeHTTP(drRec, dr)
	if drRec.Code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d", drRec.Code)
	}

	// The revoke must evict the cache so the key fails IMMEDIATELY, not after
	// cacheTTL. Without the eviction the cached entry keeps authenticating.
	if _, err := s.validateAPIKey(context.Background(), created.Key); err == nil {
		t.Fatal("revoked key still validates from cache (eviction missing)")
	}
}

func TestAPIKey_ListReturnsMetadataNotSecrets(t *testing.T) {
	s, _ := apiKeyFixture(t)

	body := `{"name":"listed"}`
	cr := httptest.NewRequest(http.MethodPost, "/api/v1/user/api-keys", strings.NewReader(body))
	cr.Header.Set("Content-Type", "application/json")
	cr.ContentLength = int64(len(body))
	s.router.ServeHTTP(httptest.NewRecorder(), cr)

	lr := httptest.NewRequest(http.MethodGet, "/api/v1/user/api-keys", nil)
	lrRec := httptest.NewRecorder()
	s.router.ServeHTTP(lrRec, lr)
	if lrRec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", lrRec.Code)
	}
	out := lrRec.Body.String()
	if !strings.Contains(out, "listed") {
		t.Errorf("expected the created key name in the list, got %s", out)
	}
	if strings.Contains(out, "atk_") || strings.Contains(out, "keyHash") {
		t.Errorf("list leaked secret material: %s", out)
	}
}

func TestSanitizeLabelValue(t *testing.T) {
	cases := map[string]string{
		"":                    "unknown",
		"us-east-1:abcd-1234": "us-east-1-abcd-1234",
		"simple":              "simple",
		"-leading":            "leading",
		"trailing-":           "trailing",
		"::::":                "user",
	}
	for in, want := range cases {
		if got := sanitizeLabelValue(in); got != want {
			t.Errorf("sanitizeLabelValue(%q) = %q, want %q", in, got, want)
		}
	}
}
