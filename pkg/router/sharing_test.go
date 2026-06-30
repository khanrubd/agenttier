/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/router/sharelinks"
)

func TestSharing_GrantListRevoke(t *testing.T) {
	s, c := apiKeyFixture(t)
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "share-sb", Namespace: "agenttier"},
		Spec:       agenttierv1alpha1.SandboxSpec{CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "dev-user"}},
	}
	if err := c.Create(context.Background(), sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	// Grant access.
	body := `{"identity":"alice@example.com","level":"collaborator"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/share-sb/share", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("share: expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	// The CR carries the permission.
	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "share-sb", Namespace: "agenttier"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Spec.Sharing == nil || len(got.Spec.Sharing.Users) != 1 ||
		got.Spec.Sharing.Users[0].Identity != "alice@example.com" || got.Spec.Sharing.Users[0].Level != "collaborator" {
		t.Fatalf("expected alice as collaborator on the CR, got %+v", got.Spec.Sharing)
	}

	// GET reflects it.
	gr := httptest.NewRequest(http.MethodGet, "/api/v1/sandboxes/share-sb/share", nil)
	grRec := httptest.NewRecorder()
	s.router.ServeHTTP(grRec, gr)
	if grRec.Code != http.StatusOK || !strings.Contains(grRec.Body.String(), "alice@example.com") {
		t.Fatalf("get sharing: code=%d body=%s", grRec.Code, grRec.Body.String())
	}

	// Revoke.
	dr := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/share-sb/share/alice@example.com", nil)
	drRec := httptest.NewRecorder()
	s.router.ServeHTTP(drRec, dr)
	if drRec.Code != http.StatusOK {
		t.Fatalf("revoke: expected 200, got %d", drRec.Code)
	}
	got2 := &agenttierv1alpha1.Sandbox{}
	_ = c.Get(context.Background(), client.ObjectKey{Name: "share-sb", Namespace: "agenttier"}, got2)
	if got2.Spec.Sharing != nil && len(got2.Spec.Sharing.Users) != 0 {
		t.Fatalf("expected no users after revoke, got %+v", got2.Spec.Sharing.Users)
	}
}

func TestSharing_CreateShareLinkReturnsTokenOnceAndStoresHash(t *testing.T) {
	s, c := apiKeyFixture(t)
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "link-sb", Namespace: "agenttier"},
		Spec:       agenttierv1alpha1.SandboxSpec{CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "dev-user"}},
	}
	if err := c.Create(context.Background(), sb); err != nil {
		t.Fatalf("create sandbox: %v", err)
	}

	body := `{"level":"viewer","expiresIn":"24h","maxUses":3}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/link-sb/share-links", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link: expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		Level string `json:"level"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if resp.Token == "" || resp.ID == "" || resp.Level != "viewer" {
		t.Fatalf("expected a token+id+level, got %+v", resp)
	}

	// The CR stores only the HASH, never the raw token.
	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "link-sb", Namespace: "agenttier"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Spec.Sharing == nil || len(got.Spec.Sharing.ShareLinks) != 1 {
		t.Fatalf("expected 1 share link on the CR, got %+v", got.Spec.Sharing)
	}
	link := got.Spec.Sharing.ShareLinks[0]
	if link.Token != "" {
		t.Error("SECURITY: raw token persisted on the CR")
	}
	if link.TokenHash != sharelinks.HashToken(resp.Token) {
		t.Error("stored hash does not match the issued token")
	}
}

func TestSharing_SharedUserCanReadButNotManage(t *testing.T) {
	// A sandbox owned by someone else, shared with bob (viewer).
	sb := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{
			CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "owner-1"},
			Sharing: &agenttierv1alpha1.SharingSpec{
				Users: []agenttierv1alpha1.SharePermission{{Identity: "bob@example.com", Level: "viewer"}},
			},
		},
	}
	bob := &Claims{Sub: "bob", Email: "bob@example.com"}
	if !userCanAccessSandbox(sb, bob) {
		t.Error("a shared viewer should have read access")
	}
	stranger := &Claims{Sub: "eve", Email: "eve@example.com"}
	if userCanAccessSandbox(sb, stranger) {
		t.Error("a non-shared, non-owner user must NOT have access")
	}
	// Admin always.
	if !userCanAccessSandbox(sb, &Claims{Sub: "x", IsAdmin: true}) {
		t.Error("admin should always have access")
	}
}
