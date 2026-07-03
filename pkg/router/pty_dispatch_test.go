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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// The terminal-transport picker mirrors pickExecutor's tree, so the
// tests here mirror exec_dispatch_test.go's. Three outcomes:
//
//  1. No token Secret → SPDY, no fallback warning.
//  2. Token + no PodIP → SPDY, fallback warning explains why.
//  3. Token + IP + healthy /healthz → HTTP-PTY transport.
//
// A separate sub-test covers the /healthz-fail-fallback path because
// it's the most likely real-world fallback (runtime crashed inside
// the pod).

func TestPickTerminalTransport_NoTokenSecretFallsToSPDY(t *testing.T) {
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{PodName: "sbx-1-pod"},
	}
	s := pickExecutorFixture(t, sb)

	transport, reason := s.pickTerminalTransport(context.Background(), sb)
	if reason != "" {
		t.Errorf("expected empty fallback reason for non-opted-in sandbox, got %q", reason)
	}
	if _, ok := transport.(*spdyTerminalTransport); !ok {
		t.Errorf("expected spdyTerminalTransport, got %T", transport)
	}
}

func TestPickTerminalTransport_TokenButNoPodIPFallsToSPDY(t *testing.T) {
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
		// PodIP intentionally empty — pre-IP-assignment window.
	}
	s := pickExecutorFixture(t, sb, tokenSecret, pod)

	transport, reason := s.pickTerminalTransport(context.Background(), sb)
	if reason == "" {
		t.Error("expected fallback reason when pod has no IP")
	}
	if !strings.Contains(reason, "pod IP") {
		t.Errorf("reason should mention pod IP: %q", reason)
	}
	if _, ok := transport.(*spdyTerminalTransport); !ok {
		t.Errorf("expected spdyTerminalTransport, got %T", transport)
	}
}

func TestPickTerminalTransport_HealthzFailFallsBack(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
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
	s := pickExecutorFixture(t, sb, tokenSecret, pod)
	originalPort := runtimePortForTest
	runtimePortForTest = port
	defer func() { runtimePortForTest = originalPort }()

	transport, reason := s.pickTerminalTransport(context.Background(), sb)
	if reason == "" {
		t.Error("expected fallback reason on /healthz failure")
	}
	if !strings.Contains(reason, "healthz") {
		t.Errorf("reason should mention healthz: %q", reason)
	}
	if _, ok := transport.(*spdyTerminalTransport); !ok {
		t.Errorf("expected spdyTerminalTransport, got %T", transport)
	}
}

func TestPickTerminalTransport_HealthyRuntimePicksHTTPPTY(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.NotFound(w, r)
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

	transport, reason := s.pickTerminalTransport(context.Background(), sb)
	if reason != "" {
		t.Errorf("expected empty fallback reason on happy path, got %q", reason)
	}
	if _, ok := transport.(*httpPTYTerminalTransport); !ok {
		t.Fatalf("expected httpPTYTerminalTransport, got %T", transport)
	}
}
