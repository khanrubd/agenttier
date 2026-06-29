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

func prefsServer(t *testing.T) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, agenttierv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	return NewServer(&Config{ListenAddr: ":0", InstallNamespace: "agenttier", DevAuth: true}, c, nil)
}

// TestPreferences_RoundTrip guards the fix for the 501 user-preferences
// endpoint: PUT /user/preferences must persist, and GET must return it.
func TestPreferences_RoundTrip(t *testing.T) {
	s := prefsServer(t)

	// Initially empty.
	gr := httptest.NewRequest(http.MethodGet, "/api/v1/user/preferences", nil)
	grRec := httptest.NewRecorder()
	s.router.ServeHTTP(grRec, gr)
	if grRec.Code != http.StatusOK {
		t.Fatalf("initial GET: expected 200, got %d", grRec.Code)
	}
	if strings.TrimSpace(grRec.Body.String()) != "{}" {
		t.Errorf("initial preferences should be empty object, got %s", grRec.Body.String())
	}

	// Save preferences.
	body := `{"theme":"dark","notifications":{"email":true}}`
	pr := httptest.NewRequest(http.MethodPut, "/api/v1/user/preferences", strings.NewReader(body))
	pr.Header.Set("Content-Type", "application/json")
	pr.ContentLength = int64(len(body))
	prRec := httptest.NewRecorder()
	s.router.ServeHTTP(prRec, pr)
	if prRec.Code != http.StatusOK {
		t.Fatalf("PUT: expected 200 (was 501 before the fix), got %d body=%s", prRec.Code, prRec.Body.String())
	}

	// Read them back.
	gr2 := httptest.NewRequest(http.MethodGet, "/api/v1/user/preferences", nil)
	gr2Rec := httptest.NewRecorder()
	s.router.ServeHTTP(gr2Rec, gr2)
	if gr2Rec.Code != http.StatusOK {
		t.Fatalf("GET after PUT: expected 200, got %d", gr2Rec.Code)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(gr2Rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("invalid prefs JSON: %v", err)
	}
	if got["theme"] != "dark" {
		t.Errorf("theme = %v, want dark (preferences did not round-trip)", got["theme"])
	}
}
