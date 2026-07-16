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

package crds

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// fakeCRDServer emulates just enough of the apiextensions.k8s.io/v1 REST API
// (GET/POST/PUT on customresourcedefinitions) for Apply's create-or-update
// loop, without needing a real API server or envtest.
type fakeCRDServer struct {
	mu sync.Mutex
	// existing maps CRD name -> resourceVersion for CRDs that should appear
	// already installed (triggering the Update branch in Apply).
	existing map[string]string
	// getStatus, when non-zero, is returned for every GET instead of the
	// existing/not-found logic (used to simulate a non-404 Get error).
	getStatus int
	// failCreate/failUpdate force the corresponding write to fail.
	failCreate bool
	failUpdate bool

	created []string
	updated []string
}

func newFakeCRDServer() *fakeCRDServer {
	return &fakeCRDServer{existing: map[string]string{}}
}

func (f *fakeCRDServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeCRDServer) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	name := strings.TrimPrefix(r.URL.Path, "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/")
	name = strings.TrimSuffix(name, "/")

	switch r.Method {
	case http.MethodGet:
		f.mu.Lock()
		status := f.getStatus
		rv, ok := f.existing[name]
		f.mu.Unlock()

		if status != 0 {
			writeStatus(w, status, "boom")
			return
		}
		if !ok {
			writeStatus(w, http.StatusNotFound, "not found")
			return
		}
		crd := &apiextensionsv1.CustomResourceDefinition{
			TypeMeta:   metav1.TypeMeta{Kind: "CustomResourceDefinition", APIVersion: "apiextensions.k8s.io/v1"},
			ObjectMeta: metav1.ObjectMeta{Name: name, ResourceVersion: rv},
		}
		json.NewEncoder(w).Encode(crd) //nolint:errcheck // test helper, no reader to observe a write error

	case http.MethodPost:
		f.mu.Lock()
		fail := f.failCreate
		f.mu.Unlock()
		if fail {
			writeStatus(w, http.StatusInternalServerError, "create failed")
			return
		}
		var crd apiextensionsv1.CustomResourceDefinition
		_ = json.NewDecoder(r.Body).Decode(&crd)
		crd.TypeMeta = metav1.TypeMeta{Kind: "CustomResourceDefinition", APIVersion: "apiextensions.k8s.io/v1"}
		f.mu.Lock()
		f.created = append(f.created, crd.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(crd) //nolint:errcheck // test helper

	case http.MethodPut:
		f.mu.Lock()
		fail := f.failUpdate
		f.mu.Unlock()
		if fail {
			writeStatus(w, http.StatusInternalServerError, "update failed")
			return
		}
		var crd apiextensionsv1.CustomResourceDefinition
		_ = json.NewDecoder(r.Body).Decode(&crd)
		crd.TypeMeta = metav1.TypeMeta{Kind: "CustomResourceDefinition", APIVersion: "apiextensions.k8s.io/v1"}
		f.mu.Lock()
		f.updated = append(f.updated, crd.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(crd) //nolint:errcheck // test helper

	default:
		writeStatus(w, http.StatusMethodNotAllowed, "unsupported method in fake CRD server: "+r.Method)
	}
}

func writeStatus(w http.ResponseWriter, code int, msg string) {
	w.WriteHeader(code)
	reason := metav1.StatusReasonInternalError
	if code == http.StatusNotFound {
		reason = metav1.StatusReasonNotFound
	}
	json.NewEncoder(w).Encode(metav1.Status{ //nolint:errcheck // test helper
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Message:  msg,
		Reason:   reason,
		Code:     int32(code),
	})
}

func TestApply_CreatesMissingCRDs(t *testing.T) {
	fake := newFakeCRDServer()
	srv := fake.start(t)

	if err := Apply(context.Background(), &rest.Config{Host: srv.URL}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	defs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != len(defs) {
		t.Errorf("created %d CRDs, want %d (one per embedded manifest)", len(fake.created), len(defs))
	}
	if len(fake.updated) != 0 {
		t.Errorf("expected no updates on a from-scratch Apply, got %v", fake.updated)
	}
}

func TestApply_UpdatesExistingCRDsPreservingResourceVersion(t *testing.T) {
	fake := newFakeCRDServer()

	defs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, d := range defs {
		fake.existing[d.Name] = "42"
	}

	srv := fake.start(t)
	if err := Apply(context.Background(), &rest.Config{Host: srv.URL}, slog.Default()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.created) != 0 {
		t.Errorf("expected no creates when all CRDs pre-exist, got %v", fake.created)
	}
	if len(fake.updated) != len(defs) {
		t.Errorf("updated %d CRDs, want %d", len(fake.updated), len(defs))
	}
}

func TestApply_GetErrorOtherThanNotFoundIsPropagated(t *testing.T) {
	fake := newFakeCRDServer()
	fake.getStatus = http.StatusInternalServerError
	srv := fake.start(t)

	err := Apply(context.Background(), &rest.Config{Host: srv.URL}, nil)
	if err == nil {
		t.Fatal("Apply should fail when Get returns a non-404, non-nil error")
	}
	if !strings.Contains(err.Error(), "get CRD") {
		t.Errorf("error should be wrapped with \"get CRD\" context, got: %v", err)
	}
}

func TestApply_CreateErrorIsPropagated(t *testing.T) {
	fake := newFakeCRDServer()
	fake.failCreate = true
	srv := fake.start(t)

	err := Apply(context.Background(), &rest.Config{Host: srv.URL}, nil)
	if err == nil {
		t.Fatal("Apply should fail when Create returns an error")
	}
	if !strings.Contains(err.Error(), "create CRD") {
		t.Errorf("error should be wrapped with \"create CRD\" context, got: %v", err)
	}
}

func TestApply_UpdateErrorIsPropagated(t *testing.T) {
	fake := newFakeCRDServer()
	defs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, d := range defs {
		fake.existing[d.Name] = "1"
	}
	fake.failUpdate = true
	srv := fake.start(t)

	err = Apply(context.Background(), &rest.Config{Host: srv.URL}, nil)
	if err == nil {
		t.Fatal("Apply should fail when Update returns an error")
	}
	if !strings.Contains(err.Error(), "update CRD") {
		t.Errorf("error should be wrapped with \"update CRD\" context, got: %v", err)
	}
}

// TestApply_ClientConstructionErrorIsPropagated exercises the
// apiextensionsclient.NewForConfig error branch by supplying a rest.Config
// that is rejected before any HTTP call is made (ExecProvider and
// AuthProvider are mutually exclusive).
func TestApply_ClientConstructionErrorIsPropagated(t *testing.T) {
	cfg := &rest.Config{
		Host:         "http://127.0.0.1:0",
		ExecProvider: &clientcmdapi.ExecConfig{Command: "true"},
		AuthProvider: &clientcmdapi.AuthProviderConfig{Name: "does-not-matter"},
	}

	err := Apply(context.Background(), cfg, nil)
	if err == nil {
		t.Fatal("Apply should fail when the apiextensions client cannot be constructed")
	}
	if !strings.Contains(err.Error(), "build apiextensions client") {
		t.Errorf("error should be wrapped with \"build apiextensions client\" context, got: %v", err)
	}
}

// TestApply_NilLoggerDoesNotPanic guards Apply's `if logger == nil { logger =
// slog.Default() }` fallback: a nil logger must not panic when Apply logs an
// installed/updated CRD.
func TestApply_NilLoggerDoesNotPanic(t *testing.T) {
	fake := newFakeCRDServer()
	srv := fake.start(t)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Apply panicked with nil logger: %v", r)
		}
	}()

	if err := Apply(context.Background(), &rest.Config{Host: srv.URL}, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}
