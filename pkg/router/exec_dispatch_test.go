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
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/router/sandboxhttp"
)

// pickExecutorFixture builds a Server with a fake k8s client containing
// the supplied objects and a no-op SPDY bridge. Tests use it to exercise
// the dispatch decision tree without spinning a real cluster.
func pickExecutorFixture(t *testing.T, objs ...client.Object) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		agenttierv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	s := NewServer(&Config{ListenAddr: ":0"}, c, nil)
	return s
}

func TestPickExecutor_NoTokenSecretFallsToSPDY(t *testing.T) {
	// Common case today: sandbox isn't opted into HTTP-exec, no token
	// Secret exists. Must produce SPDY without any fallback warning
	// (this is the deliberate path, not a degraded one).
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{PodName: "sbx-1-pod"},
	}
	s := pickExecutorFixture(t, sb)

	exec, reason := s.pickExecutor(context.Background(), sb)
	if reason != "" {
		t.Errorf("expected empty fallback reason on no-opt-in path, got %q", reason)
	}
	if _, ok := exec.(*spdyExecutor); !ok {
		t.Errorf("expected spdyExecutor, got %T", exec)
	}
}

func TestPickExecutor_TokenButNoPodIPFallsToSPDY(t *testing.T) {
	// Brief window between Pod scheduling and kubelet finalizing the
	// network setup: token Secret exists but Pod.Status.PodIP is empty.
	// Falling back avoids a 502; the next call (after kubelet finishes)
	// will pick the HTTP path naturally.
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{PodName: "sbx-1-pod"},
	}
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1-runtime-token", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("test-token")},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1-pod", Namespace: "default"},
		// PodIP intentionally empty — simulates the gap window.
	}
	s := pickExecutorFixture(t, sb, tokenSecret, pod)

	exec, reason := s.pickExecutor(context.Background(), sb)
	if reason == "" {
		t.Error("expected a fallback reason explaining the SPDY choice")
	}
	if !strings.Contains(reason, "pod IP") {
		t.Errorf("reason should mention pod IP: %q", reason)
	}
	if _, ok := exec.(*spdyExecutor); !ok {
		t.Errorf("expected spdyExecutor, got %T", exec)
	}
}

func TestPickExecutor_HealthzFailFallsBack(t *testing.T) {
	// Token + IP present, but the in-pod runtime returns 503 from
	// /healthz. The dispatcher must fall back rather than 502'ing
	// the user.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}))
	defer failing.Close()
	host, port := splitHostPort(t, failing.URL)

	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{PodName: "sbx-1-pod"},
	}
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1-runtime-token", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("test-token")},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1-pod", Namespace: "default"},
		Status:     corev1.PodStatus{PodIP: host},
	}
	// We can't redirect the pod's IP to httptest's port via the
	// production code path — the dispatcher hard-codes :9000. Instead
	// inject the test's port via a dispatcher overlay used only by the
	// HealthzFail / Happy-path tests below.
	s := pickExecutorFixture(t, sb, tokenSecret, pod)
	originalPort := runtimePortForTest
	runtimePortForTest = port
	defer func() { runtimePortForTest = originalPort }()

	exec, reason := s.pickExecutor(context.Background(), sb)
	if reason == "" {
		t.Error("expected fallback reason on /healthz failure")
	}
	if !strings.Contains(reason, "healthz") {
		t.Errorf("reason should mention healthz: %q", reason)
	}
	if _, ok := exec.(*spdyExecutor); !ok {
		t.Errorf("expected spdyExecutor, got %T", exec)
	}
}

func TestPickExecutor_HealthyRuntimePicksHTTP(t *testing.T) {
	// Happy path: token + IP + reachable runtime → httpExecutor.
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/exec":
			_ = json.NewEncoder(w).Encode(sandboxhttp.ExecResponse{
				ExitCode: 0,
				Stdout:   "hi\n",
			})
		}
	}))
	defer healthy.Close()
	host, port := splitHostPort(t, healthy.URL)

	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{PodName: "sbx-1-pod"},
	}
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1-runtime-token", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("test-token")},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1-pod", Namespace: "default"},
		Status:     corev1.PodStatus{PodIP: host},
	}
	s := pickExecutorFixture(t, sb, tokenSecret, pod)
	originalPort := runtimePortForTest
	runtimePortForTest = port
	defer func() { runtimePortForTest = originalPort }()

	exec, reason := s.pickExecutor(context.Background(), sb)
	if reason != "" {
		t.Errorf("expected empty fallback reason on happy path, got %q", reason)
	}
	if _, ok := exec.(*httpExecutor); !ok {
		t.Fatalf("expected httpExecutor, got %T", exec)
	}

	// End-to-end: dispatchExec should produce the runtime's response.
	result, err := s.dispatchExec(context.Background(), sb, []string{"echo", "hi"}, 5)
	if err != nil {
		t.Fatalf("dispatchExec: %v", err)
	}
	if result.Stdout != "hi\n" || result.ExitCode != 0 {
		t.Errorf("result mismatch: %+v", result)
	}
}

// splitHostPort parses an httptest.Server URL into host/port.
func splitHostPort(t *testing.T, urlStr string) (host, port string) {
	t.Helper()
	u, err := url.Parse(urlStr)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u.Hostname(), u.Port()
}
