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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// failingClient wraps a real client and forces List to return an error.
// We use it to simulate a Router that has lost its connection to the K8s
// API server — the readiness probe should flip to 503 instead of silently
// reporting healthy.
type failingClient struct {
	client.Client
	err error
}

func (f *failingClient) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return f.err
}

func TestHandleReadyz_HealthyWhenK8sReachable(t *testing.T) {
	s, _ := buildTestServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	s.handleReadyz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", rec.Body.String())
	}
}

func TestHandleReadyz_NotReadyWhenK8sUnreachable(t *testing.T) {
	s, c := buildTestServer(t)
	// Replace the k8sClient with one that fails List — simulates a Router
	// that has lost API access (token rotation, network partition, etc).
	s.k8sClient = &failingClient{Client: c, err: errors.New("connection refused")}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	s.handleReadyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() == 0 {
		t.Errorf("expected non-empty error body for diagnosis, got empty")
	}
}
