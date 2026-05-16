/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func newTokenScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}
	return s
}

func TestEnsureRuntimeTokenSecret_CreatesAndIsIdempotent(t *testing.T) {
	scheme := newTokenScheme(t)
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sbx-1",
			Namespace: "agenttier",
			UID:       "00000000-0000-0000-0000-000000000001",
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sb).Build()
	r := &SandboxReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
	}

	// First call: creates a Secret.
	name1, err := r.ensureRuntimeTokenSecret(context.Background(), sb)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if name1 != "sbx-1-runtime-token" {
		t.Errorf("name = %q, want sbx-1-runtime-token", name1)
	}

	// Verify the Secret exists with the expected shape.
	got := &corev1.Secret{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Name: name1, Namespace: "agenttier"}, got); err != nil {
		t.Fatalf("get secret: %v", err)
	}
	token1 := string(got.Data["token"])
	if len(token1) < 32 {
		// 32 random bytes URL-safe-base64-encoded → ~43 chars. Anything
		// shorter means the token wasn't generated.
		t.Errorf("token too short (%d chars) — entropy generation failed?", len(token1))
	}
	if got.Labels["agenttier.io/secret-purpose"] != "runtime-token" {
		t.Errorf("missing secret-purpose label: %+v", got.Labels)
	}
	// Owner reference points back to the Sandbox so deleting the
	// Sandbox GCs the Secret.
	if len(got.OwnerReferences) != 1 || got.OwnerReferences[0].Kind != "Sandbox" {
		t.Errorf("owner reference missing or wrong kind: %+v", got.OwnerReferences)
	}

	// Second call: must NOT regenerate the token. Rotating on every
	// reconcile would force a Pod restart on every controller loop.
	name2, err := r.ensureRuntimeTokenSecret(context.Background(), sb)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if name2 != name1 {
		t.Errorf("second call returned different name: %q vs %q", name2, name1)
	}
	got2 := &corev1.Secret{}
	if err := c.Get(context.Background(),
		client.ObjectKey{Name: name2, Namespace: "agenttier"}, got2); err != nil {
		t.Fatalf("get secret 2nd time: %v", err)
	}
	if string(got2.Data["token"]) != token1 {
		t.Error("token was regenerated on idempotent call")
	}
}

func TestReadRuntimeToken_MissingSecretReturnsEmpty(t *testing.T) {
	// Phase 4's fallback semantics: when the Secret is absent (legacy
	// sandbox or HTTP-exec disabled), the lookup must return ""+nil so
	// the Router can transparently fall back to SPDY rather than 502'ing.
	scheme := newTokenScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	tok, err := ReadRuntimeToken(context.Background(), c, "no-such-sandbox", "agenttier")
	if err != nil {
		t.Errorf("ReadRuntimeToken: got error %v, want nil for missing secret", err)
	}
	if tok != "" {
		t.Errorf("token = %q, want empty for missing secret", tok)
	}
}

func TestReadRuntimeToken_ReturnsExistingToken(t *testing.T) {
	scheme := newTokenScheme(t)
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      runtimeTokenSecretName("sbx-2"),
			Namespace: "agenttier",
		},
		Data: map[string][]byte{"token": []byte("test-token-value")},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	tok, err := ReadRuntimeToken(context.Background(), c, "sbx-2", "agenttier")
	if err != nil {
		t.Fatalf("ReadRuntimeToken: %v", err)
	}
	if tok != "test-token-value" {
		t.Errorf("token = %q, want %q", tok, "test-token-value")
	}
}
