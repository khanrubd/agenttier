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

package governance

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("add clientgoscheme: %v", err)
	}
	return s
}

func TestPolicy_IsEmpty(t *testing.T) {
	if !(Policy{}).IsEmpty() {
		t.Error("zero-value Policy should be empty")
	}
	if (Policy{MaxSandboxesPerUser: 1}).IsEmpty() {
		t.Error("MaxSandboxesPerUser set should not be empty")
	}
	if (Policy{MaxCPU: "1"}).IsEmpty() {
		t.Error("MaxCPU set should not be empty")
	}
	if (Policy{AllowedTemplates: []string{"a"}}).IsEmpty() {
		t.Error("AllowedTemplates set should not be empty")
	}
	if (Policy{MaxAgentSandboxes: 1}).IsEmpty() {
		t.Error("MaxAgentSandboxes set should not be empty")
	}
	if (Policy{AllowedAgentImages: []string{"a"}}).IsEmpty() {
		t.Error("AllowedAgentImages set should not be empty")
	}
	if (Policy{MaxConcurrentInvokesPerSandbox: 1}).IsEmpty() {
		t.Error("MaxConcurrentInvokesPerSandbox set should not be empty")
	}
}

func TestConfigMapStore_SetGetDeletePolicy_RoundTrip(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	store := NewConfigMapStore(c)
	ctx := context.Background()

	// Missing policy returns empty Policy, no error.
	got, err := store.GetPolicy(ctx, "team-a")
	if err != nil {
		t.Fatalf("GetPolicy on missing scope: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("expected empty policy for missing scope, got %+v", got)
	}

	// Set + Get round-trip for a namespace scope.
	p := Policy{MaxSandboxesPerUser: 3, Description: "team a policy"}
	if err := store.SetPolicy(ctx, "team-a", p); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	got, err = store.GetPolicy(ctx, "team-a")
	if err != nil {
		t.Fatalf("GetPolicy after set: %v", err)
	}
	if got.MaxSandboxesPerUser != 3 || got.Description != "team a policy" {
		t.Errorf("GetPolicy = %+v, want MaxSandboxesPerUser=3 Description=%q", got, "team a policy")
	}

	// Set + Get round-trip for cluster scope (empty string).
	clusterPolicy := Policy{MaxSandboxesTotal: 100}
	if err := store.SetPolicy(ctx, "", clusterPolicy); err != nil {
		t.Fatalf("SetPolicy(cluster): %v", err)
	}
	got, err = store.GetPolicy(ctx, "")
	if err != nil {
		t.Fatalf("GetPolicy(cluster): %v", err)
	}
	if got.MaxSandboxesTotal != 100 {
		t.Errorf("GetPolicy(cluster) = %+v, want MaxSandboxesTotal=100", got)
	}

	// ListPolicies returns both, cluster first.
	list, err := store.ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListPolicies returned %d entries, want 2: %+v", len(list), list)
	}
	if list[0].Scope != "" {
		t.Errorf("expected cluster policy (scope=\"\") first, got scope=%q", list[0].Scope)
	}

	// Update an existing scope (exercises the ResourceVersion-set save path).
	updated := Policy{MaxSandboxesPerUser: 9}
	if err := store.SetPolicy(ctx, "team-a", updated); err != nil {
		t.Fatalf("SetPolicy (update): %v", err)
	}
	got, err = store.GetPolicy(ctx, "team-a")
	if err != nil {
		t.Fatalf("GetPolicy after update: %v", err)
	}
	if got.MaxSandboxesPerUser != 9 {
		t.Errorf("GetPolicy after update = %+v, want MaxSandboxesPerUser=9", got)
	}

	// Delete removes it.
	if err := store.DeletePolicy(ctx, "team-a"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	got, err = store.GetPolicy(ctx, "team-a")
	if err != nil {
		t.Fatalf("GetPolicy after delete: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("expected empty policy after delete, got %+v", got)
	}

	// Deleting the cluster default resets to "no governance".
	if err := store.DeletePolicy(ctx, ""); err != nil {
		t.Fatalf("DeletePolicy(cluster): %v", err)
	}
	got, err = store.GetPolicy(ctx, "")
	if err != nil {
		t.Fatalf("GetPolicy(cluster) after delete: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("expected empty cluster policy after delete, got %+v", got)
	}
}

func TestConfigMapStore_DeletePolicy_OnEmptyStore(t *testing.T) {
	// Deleting from a store with no ConfigMap at all must not error — this
	// exercises load()'s IsNotFound branch feeding into DeletePolicy's
	// delete-from-nil-map path.
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	store := NewConfigMapStore(c)
	if err := store.DeletePolicy(context.Background(), "nonexistent"); err != nil {
		t.Fatalf("DeletePolicy on empty store: %v", err)
	}
}

func TestConfigMapStore_ListPolicies_Empty(t *testing.T) {
	c := fake.NewClientBuilder().WithScheme(testScheme(t)).Build()
	store := NewConfigMapStore(c)
	list, err := store.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 policies on empty store, got %d", len(list))
	}
}

// fakeStore is an in-memory Store used to test Resolve() in isolation from
// the ConfigMap-backed implementation.
type fakeStore struct {
	policies map[string]Policy
}

func (f *fakeStore) ListPolicies(_ context.Context) ([]ScopedPolicy, error) { return nil, nil }
func (f *fakeStore) GetPolicy(_ context.Context, scope string) (Policy, error) {
	return f.policies[scope], nil
}
func (f *fakeStore) SetPolicy(_ context.Context, scope string, p Policy) error {
	f.policies[scope] = p
	return nil
}
func (f *fakeStore) DeletePolicy(_ context.Context, scope string) error {
	delete(f.policies, scope)
	return nil
}

func TestResolve_ClusterOnly(t *testing.T) {
	store := &fakeStore{policies: map[string]Policy{"": {MaxCPU: "4"}}}
	got, err := Resolve(context.Background(), store, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.MaxCPU != "4" {
		t.Errorf("Resolve(\"\") = %+v, want MaxCPU=4", got)
	}
}

func TestResolve_MergesNamespaceOverCluster(t *testing.T) {
	store := &fakeStore{policies: map[string]Policy{
		"":       {MaxCPU: "4", MaxMemory: "8Gi"},
		"team-a": {MaxCPU: "2"},
	}}
	got, err := Resolve(context.Background(), store, "team-a")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.MaxCPU != "2" {
		t.Errorf("expected namespace override MaxCPU=2, got %s", got.MaxCPU)
	}
	if got.MaxMemory != "8Gi" {
		t.Errorf("expected inherited MaxMemory=8Gi, got %s", got.MaxMemory)
	}
}

func TestViolation_Error(t *testing.T) {
	v := Violation{Code: "x", Message: "boom"}
	if v.Error() != "boom" {
		t.Errorf("Violation.Error() = %q, want %q", v.Error(), "boom")
	}
}

func TestViolations_Error(t *testing.T) {
	if (Violations{}).Error() != "" {
		t.Error("empty Violations.Error() should be empty string")
	}
	vs := Violations{{Message: "a"}, {Message: "b"}}
	if got := vs.Error(); got != "a; b" {
		t.Errorf("Violations.Error() = %q, want %q", got, "a; b")
	}
}
