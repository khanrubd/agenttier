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
	"time"

	"github.com/gorilla/mux"
)

func TestStampDeprecation(t *testing.T) {
	h := http.Header{}
	stampDeprecation(h, deprecatedEndpoint{
		Sunset:    time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		Successor: "/api/v2/sandboxes",
	})
	if h.Get("Deprecation") != "true" {
		t.Errorf("Deprecation header = %q, want true", h.Get("Deprecation"))
	}
	if h.Get("Sunset") == "" {
		t.Error("expected a Sunset header")
	}
	if h.Get("Link") != `</api/v2/sandboxes>; rel="successor-version"` {
		t.Errorf("Link header = %q", h.Get("Link"))
	}
}

func TestDeprecationMiddleware_NoopByDefault(t *testing.T) {
	// With an empty registry the middleware must add no deprecation headers.
	s := &Server{}
	r := mux.NewRouter()
	r.Use(s.deprecationMiddleware)
	r.HandleFunc("/api/v1/sandboxes", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes", nil))
	if rec.Header().Get("Deprecation") != "" {
		t.Error("no endpoint is deprecated yet — middleware must be a no-op")
	}
}

func TestDeprecationMiddleware_StampsFlaggedRoute(t *testing.T) {
	// Temporarily flag a route to prove the wiring works end to end.
	const tmpl = "/api/v1/legacy"
	deprecatedRoutes[tmpl] = deprecatedEndpoint{Successor: "/api/v2/legacy"}
	defer delete(deprecatedRoutes, tmpl)

	s := &Server{}
	r := mux.NewRouter()
	r.Use(s.deprecationMiddleware)
	r.HandleFunc(tmpl, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tmpl, nil))
	if rec.Header().Get("Deprecation") != "true" {
		t.Errorf("expected Deprecation:true on the flagged route, got %q", rec.Header().Get("Deprecation"))
	}
	if rec.Header().Get("Link") == "" {
		t.Error("expected a successor Link header on the flagged route")
	}
}
