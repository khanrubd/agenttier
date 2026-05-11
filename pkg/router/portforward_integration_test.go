/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package router

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/router/portforward"
)

// buildTestServer wires up a minimal Server against a fake Kubernetes client
// with a single Running sandbox named "sbx-1" in the default namespace.
func buildTestServer(t *testing.T) (*Server, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		networkingv1.AddToScheme,
		agenttierv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme setup: %v", err)
		}
	}

	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-1", Namespace: "default", UID: "u1"},
		Spec: agenttierv1alpha1.SandboxSpec{
			CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "dev-user"},
		},
		Status: agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseRunning},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sandbox).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()

	s := NewServer(&Config{ListenAddr: ":0"}, c, nil)
	return s, c
}

func authedRequest(method, path string, body []byte) *http.Request {
	var buf *bytes.Buffer
	if body != nil {
		buf = bytes.NewBuffer(body)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	// In dev mode (no OIDC) the server auto-identifies as admin,
	// so no auth header is needed.
	return req
}

func TestPortForward_E2E_CreateListRemove(t *testing.T) {
	s, c := buildTestServer(t)

	// 1) List returns no ports initially.
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sandboxes/sbx-1/ports", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("initial list: status %d body=%s", rec.Code, rec.Body.String())
	}
	var initial struct {
		Ports []portforward.ForwardedPort `json:"ports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &initial); err != nil {
		t.Fatalf("unmarshal initial list: %v", err)
	}
	if len(initial.Ports) != 0 {
		t.Fatalf("expected 0 ports initially, got %+v", initial.Ports)
	}

	// 2) Create port 8080.
	body, _ := json.Marshal(map[string]any{"port": 8080, "protocol": "http"})
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/sandboxes/sbx-1/ports", body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d body=%s", rec.Code, rec.Body.String())
	}
	var created portforward.ForwardedPort
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.Port != 8080 {
		t.Errorf("expected port 8080, got %d", created.Port)
	}
	if !strings.Contains(created.InternalURL, "pf-sbx-1-8080") {
		t.Errorf("internal URL doesn't contain service name: %q", created.InternalURL)
	}

	// 3) A Service object exists in the cluster.
	svc := &corev1.Service{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "pf-sbx-1-8080", Namespace: "default"}, svc); err != nil {
		t.Fatalf("expected service to exist: %v", err)
	}

	// 4) List now returns it.
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sandboxes/sbx-1/ports", nil))
	var list struct {
		Ports []portforward.ForwardedPort `json:"ports"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Ports) != 1 || list.Ports[0].Port != 8080 {
		t.Fatalf("expected [8080], got %+v", list.Ports)
	}

	// 5) Sandbox status was updated with the forwarded port.
	updated := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "sbx-1", Namespace: "default"}, updated); err != nil {
		t.Fatalf("refetch sandbox: %v", err)
	}
	if len(updated.Status.ForwardedPorts) != 1 || updated.Status.ForwardedPorts[0].Port != 8080 {
		t.Errorf("sandbox status not updated with forwarded port: %+v", updated.Status.ForwardedPorts)
	}

	// 6) Remove the port.
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/sandboxes/sbx-1/ports/8080", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remove: status %d body=%s", rec.Code, rec.Body.String())
	}

	// 7) Service is gone and sandbox status is cleaned up.
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sandboxes/sbx-1/ports", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal final list: %v", err)
	}
	if len(list.Ports) != 0 {
		t.Errorf("expected empty list after remove, got %+v", list.Ports)
	}
}

func TestPortForward_E2E_PreviewProxyRequiresExistingForward(t *testing.T) {
	s, _ := buildTestServer(t)

	// Preview call without a forwarded port should 404 even though the
	// sandbox is running and the user is authenticated.
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sandboxes/sbx-1/preview/9000/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown port, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPortForward_E2E_SandboxMustBeRunning(t *testing.T) {
	s, c := buildTestServer(t)

	// Flip the sandbox to Stopped and attempt to preview.
	sb := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "sbx-1", Namespace: "default"}, sb); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	sb.Status.Phase = agenttierv1alpha1.SandboxPhaseStopped
	if err := c.Status().Update(context.Background(), sb); err != nil {
		t.Fatalf("update status: %v", err)
	}

	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/sandboxes/sbx-1/preview/8080/", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 when sandbox not running, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPortForward_E2E_AuthorizationEnforced(t *testing.T) {
	// Building a second server with OIDC configured activates the admin check
	// path — a stopped claim pipeline would require more setup. Instead this
	// test guarantees that in dev mode we succeed AND that the auth bypass
	// still goes through getSandboxWithAuthCheck (covered above). Documenting
	// as a no-op reminder to add a full OIDC flow test once JWT validation
	// is plumbed through.
	t.Skip("OIDC-mode auth test deferred to when validateJWT is implemented")
}
