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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func nsResolveServer(t *testing.T, objs ...*agenttierv1alpha1.Sandbox) *Server {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, agenttierv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	b := fake.NewClientBuilder().WithScheme(scheme)
	for _, o := range objs {
		b = b.WithObjects(o)
	}
	c := b.Build()
	return NewServer(&Config{ListenAddr: ":0", SandboxNamespace: "default", DevAuth: true}, c, nil)
}

func sbInNS(name, ns string) *agenttierv1alpha1.Sandbox {
	return &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseRunning},
	}
}

// TestResolveSandbox_FindsNonDefaultNamespace is the regression guard for the
// hardcoded-"default" ceiling: a sandbox created in another namespace must be
// resolvable via the cluster-wide fallback, not 404'd.
func TestResolveSandbox_FindsNonDefaultNamespace(t *testing.T) {
	s := nsResolveServer(t, sbInNS("team-a-box", "team-a"))

	sb, err := s.resolveSandbox(context.Background(), "team-a-box", "")
	if err != nil {
		t.Fatalf("REGRESSION: sandbox in namespace team-a not found: %v", err)
	}
	if sb.Namespace != "team-a" {
		t.Errorf("resolved namespace = %q, want team-a", sb.Namespace)
	}
}

// TestResolveSandbox_PrimaryNamespaceFastPath confirms a sandbox in the
// configured namespace still resolves.
func TestResolveSandbox_PrimaryNamespaceFastPath(t *testing.T) {
	s := nsResolveServer(t, sbInNS("default-box", "default"))

	sb, err := s.resolveSandbox(context.Background(), "default-box", "")
	if err != nil {
		t.Fatalf("default-namespace sandbox not found: %v", err)
	}
	if sb.Namespace != "default" {
		t.Errorf("resolved namespace = %q, want default", sb.Namespace)
	}
}

// TestResolveSandbox_AmbiguousAcrossNamespaces returns a clear error when the
// same name exists in multiple namespaces and no hint is given.
func TestResolveSandbox_AmbiguousAcrossNamespaces(t *testing.T) {
	s := nsResolveServer(t, sbInNS("dup", "team-a"), sbInNS("dup", "team-b"))

	if _, err := s.resolveSandbox(context.Background(), "dup", ""); err == nil {
		t.Fatal("expected an ambiguity error for a name in two namespaces")
	}

	// With a namespace hint it resolves unambiguously.
	sb, err := s.resolveSandbox(context.Background(), "dup", "team-b")
	if err != nil {
		t.Fatalf("namespace hint should disambiguate: %v", err)
	}
	if sb.Namespace != "team-b" {
		t.Errorf("hinted resolve namespace = %q, want team-b", sb.Namespace)
	}
}

// TestResolveSandbox_NotFound returns an error for a missing sandbox.
func TestResolveSandbox_NotFound(t *testing.T) {
	s := nsResolveServer(t)
	if _, err := s.resolveSandbox(context.Background(), "ghost", ""); err == nil {
		t.Fatal("expected not-found error")
	}
}
