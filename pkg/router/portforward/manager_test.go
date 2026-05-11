/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package portforward

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func newTestClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add corev1: %v", err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add networkingv1: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add agenttier: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).Build()
}

func testSandbox() *agenttierv1alpha1.Sandbox {
	return &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "my-sbx", Namespace: "default", UID: "u1"},
	}
}

func TestCreate_ServiceOnly(t *testing.T) {
	c := newTestClient(t)
	m := New(c, Options{})

	sb := testSandbox()
	fp, err := m.Create(context.Background(), sb, 8080, "http")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if fp.PreviewURL != "" {
		t.Errorf("expected no preview URL when PreviewDomain unset, got %q", fp.PreviewURL)
	}
	if fp.InternalURL == "" {
		t.Errorf("expected InternalURL to be populated")
	}

	// The service should exist.
	svc := &corev1.Service{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "pf-my-sbx-8080", Namespace: "default"}, svc); err != nil {
		t.Fatalf("service not created: %v", err)
	}
	if svc.Spec.Selector["agenttier.io/sandbox"] != "my-sbx" {
		t.Errorf("unexpected selector: %+v", svc.Spec.Selector)
	}

	// No ingress should exist.
	ing := &networkingv1.Ingress{}
	err = c.Get(context.Background(), types.NamespacedName{Name: "pf-my-sbx-8080", Namespace: "default"}, ing)
	if !errors.IsNotFound(err) {
		t.Errorf("expected no ingress when PreviewDomain unset, got err=%v", err)
	}
}

func TestCreate_WithPreviewDomain(t *testing.T) {
	c := newTestClient(t)
	m := New(c, Options{PreviewDomain: "preview.agenttier.company.com", IngressClassName: "nginx"})

	sb := testSandbox()
	fp, err := m.Create(context.Background(), sb, 3000, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := "https://sandbox-my-sbx-3000.preview.agenttier.company.com/"
	if fp.PreviewURL != want {
		t.Errorf("PreviewURL = %q, want %q", fp.PreviewURL, want)
	}

	ing := &networkingv1.Ingress{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "pf-my-sbx-3000", Namespace: "default"}, ing); err != nil {
		t.Fatalf("ingress not created: %v", err)
	}
	if ing.Spec.IngressClassName == nil || *ing.Spec.IngressClassName != "nginx" {
		t.Errorf("ingress class = %v, want nginx", ing.Spec.IngressClassName)
	}
	if len(ing.Spec.Rules) != 1 || ing.Spec.Rules[0].Host != "sandbox-my-sbx-3000.preview.agenttier.company.com" {
		t.Errorf("unexpected ingress rules: %+v", ing.Spec.Rules)
	}
}

func TestCreate_Idempotent(t *testing.T) {
	c := newTestClient(t)
	m := New(c, Options{})

	sb := testSandbox()
	_, err := m.Create(context.Background(), sb, 8080, "http")
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err = m.Create(context.Background(), sb, 8080, "http")
	if err != nil {
		t.Fatalf("second Create should be idempotent: %v", err)
	}
}

func TestDelete_ServiceAndIngress(t *testing.T) {
	c := newTestClient(t)
	m := New(c, Options{PreviewDomain: "preview.test"})
	sb := testSandbox()

	if _, err := m.Create(context.Background(), sb, 8080, "http"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.Delete(context.Background(), sb, 8080); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Double-delete should be a no-op.
	if err := m.Delete(context.Background(), sb, 8080); err != nil {
		t.Fatalf("second Delete should be no-op: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: "pf-my-sbx-8080", Namespace: "default"}, svc); !errors.IsNotFound(err) {
		t.Errorf("service should be gone, got err=%v", err)
	}
}

func TestList_ReturnsAllSandboxPorts(t *testing.T) {
	c := newTestClient(t)
	m := New(c, Options{})
	sb := testSandbox()

	if _, err := m.Create(context.Background(), sb, 8080, ""); err != nil {
		t.Fatalf("Create 8080: %v", err)
	}
	if _, err := m.Create(context.Background(), sb, 3000, ""); err != nil {
		t.Fatalf("Create 3000: %v", err)
	}

	got, err := m.List(context.Background(), sb)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 forwards, got %d: %+v", len(got), got)
	}
}

func TestCreate_InvalidPort(t *testing.T) {
	c := newTestClient(t)
	m := New(c, Options{})
	if _, err := m.Create(context.Background(), testSandbox(), 0, "http"); err == nil {
		t.Error("expected error for port 0")
	}
	if _, err := m.Create(context.Background(), testSandbox(), 70000, "http"); err == nil {
		t.Error("expected error for port >65535")
	}
}
