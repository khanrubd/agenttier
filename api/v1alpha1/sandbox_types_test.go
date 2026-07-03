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

package v1alpha1

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fullSandbox builds a Sandbox with every optional field populated so
// DeepCopy round-trip tests exercise every branch in the generated
// DeepCopyInto, not just the zero-value fast path.
func fullSandbox() *Sandbox {
	rc := "gvisor"
	timeout := metav1.Duration{Duration: 3600}
	idleTimeout := metav1.Duration{Duration: 900}
	now := metav1.Now()
	expires := metav1.NewTime(now.Add(3600))

	return &Sandbox{
		TypeMeta: metav1.TypeMeta{Kind: "Sandbox", APIVersion: "agenttier.io/v1alpha1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sample",
			Namespace: "default",
			Labels:    map[string]string{"team": "platform"},
		},
		Spec: SandboxSpec{
			Mode: SandboxModeAgent,
			TemplateRef: &TemplateReference{
				Name: "base", Kind: "ClusterSandboxTemplate", Namespace: "default",
			},
			Image: &ImageSpec{
				Repository: "ghcr.io/agenttier/sandbox-base:v1",
				PullPolicy: corev1.PullIfNotPresent,
				PullSecret: "regcred",
			},
			Resources: &corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU: resource.MustParse("1"),
				},
			},
			Storage: &StorageSpec{
				Size:         resource.MustParse("10Gi"),
				StorageClass: "gp3",
				MountPath:    "/workspace",
			},
			Network: &NetworkSpec{
				AllowInternet: true,
				EgressRules: []NetworkRule{
					{CIDR: "10.0.0.0/8", Ports: []NetworkPort{{Protocol: corev1.ProtocolTCP, Port: 443}}},
				},
				IngressRules: []NetworkRule{
					{ServiceRef: &ServiceReference{Name: "router", Namespace: "default", Port: 8080}},
				},
				AllowedDomains:      []string{"example.com"},
				AllowPeerSandboxes:  true,
				PeerSandboxSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "sandbox"}},
			},
			Env:            []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
			Timeout:        &timeout,
			IdleTimeout:    &idleTimeout,
			RuntimeClass:   &rc,
			ServiceAccount: "sandbox-sa",
			AutoResume:     true,
			Security:       &SecuritySpec{Privileged: true},
			Credentials: []CredentialRef{
				{SecretName: "creds", MountAs: "env", EnvPrefix: "APP_"},
			},
			Sidecars:       []corev1.Container{{Name: "sidecar", Image: "busybox"}},
			InitContainers: []corev1.Container{{Name: "init", Image: "busybox"}},
			CreatedBy:      &UserIdentity{Sub: "user-1", Email: "user@example.com", DisplayName: "User One"},
			Sharing: &SharingSpec{
				Users:  []SharePermission{{Identity: "someone@example.com", Level: "viewer"}},
				Groups: []SharePermission{{Identity: "team", Level: "collaborator"}},
				ShareLinks: []ShareLink{
					{ID: "link-1", TokenHash: "hash", Level: "viewer", ExpiresAt: &expires, MaxUses: 5, UsedCount: 1},
				},
			},
			CloneFromSnapshot: "snap-1",
		},
		Status: SandboxStatus{
			Phase:                   SandboxPhaseRunning,
			PodName:                 "sample-pod",
			PVCName:                 "sample-pvc",
			ResolvedTemplate:        "base",
			TemplateResourceVersion: "123",
			StartedAt:               &now,
			LastActivityTimestamp:   &now,
			RestartCount:            2,
			Message:                 "running",
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue, Reason: "PodReady", LastTransitionTime: now},
			},
			ForwardedPorts: []ForwardedPort{
				{Port: 8888, PreviewURL: "https://preview.example.com", Protocol: "http"},
			},
			ClonedFrom: "source-1",
			AgentConfigure: &AgentConfigureStatus{
				LastConfiguredAt:            &now,
				InstallCommandHash:          "abc123",
				Entrypoint:                  []string{"python", "agent.py"},
				InstallExitCode:             0,
				InstallLogConfigMapRef:      &LocalObjectReference{Name: "install-log"},
				MaxConcurrentInvokes:        4,
				DefaultInvokeTimeoutSeconds: 1800,
			},
			ResolvedAgentSpec: &AgentSpec{
				Entrypoint:           []string{"python", "agent.py"},
				InstallCommand:       []string{"pip", "install", "-r", "requirements.txt"},
				WorkingDir:           "/workspace",
				Env:                  []corev1.EnvVar{{Name: "MODEL", Value: "claude"}},
				MaxConcurrentInvokes: &trueValAsInt32,
				DefaultInvokeTimeout: &idleTimeout,
			},
		},
	}
}

var trueValAsInt32 int32 = 4

func TestSandbox_DeepCopy_ProducesEqualButIndependentCopy(t *testing.T) {
	original := fullSandbox()
	copy := original.DeepCopy()

	if copy == original {
		t.Fatal("DeepCopy returned the same pointer as the original")
	}
	if copy.Spec.Image.Repository != original.Spec.Image.Repository {
		t.Fatalf("copy diverged before mutation: got %q want %q", copy.Spec.Image.Repository, original.Spec.Image.Repository)
	}

	// Mutate every pointer/slice/map field on the copy and confirm the
	// original is untouched — this is the actual guarantee DeepCopy exists
	// to provide, and it's exactly the guarantee a shallow-copy regression
	// would silently break.
	copy.ObjectMeta.Labels["team"] = "mutated"
	copy.Spec.Image.Repository = "mutated"
	copy.Spec.TemplateRef.Name = "mutated"
	copy.Spec.Storage.Size = resource.MustParse("999Gi")
	copy.Spec.Network.EgressRules[0].CIDR = "0.0.0.0/0"
	copy.Spec.Env[0].Value = "mutated"
	copy.Spec.RuntimeClass = ptrTo("mutated")
	copy.Spec.Security.Privileged = false
	copy.Spec.Credentials[0].SecretName = "mutated"
	copy.Spec.CreatedBy.Email = "mutated@example.com"
	copy.Spec.Sharing.Users[0].Identity = "mutated"
	copy.Spec.Sharing.ShareLinks[0].TokenHash = "mutated"
	copy.Status.Conditions[0].Reason = "mutated"
	copy.Status.ForwardedPorts[0].PreviewURL = "mutated"
	copy.Status.AgentConfigure.InstallCommandHash = "mutated"
	copy.Status.ResolvedAgentSpec.Entrypoint[0] = "mutated"

	original = fullSandbox() // pristine reference for comparison

	if copy.ObjectMeta.Labels["team"] == original.ObjectMeta.Labels["team"] {
		t.Error("mutating copy.Labels affected original")
	}
	if copy.Spec.Image.Repository == original.Spec.Image.Repository {
		t.Error("mutating copy.Spec.Image affected original")
	}
	if copy.Spec.TemplateRef.Name == original.Spec.TemplateRef.Name {
		t.Error("mutating copy.Spec.TemplateRef affected original")
	}
	if copy.Spec.Network.EgressRules[0].CIDR == original.Spec.Network.EgressRules[0].CIDR {
		t.Error("mutating copy.Spec.Network.EgressRules affected original")
	}
	if copy.Spec.Env[0].Value == original.Spec.Env[0].Value {
		t.Error("mutating copy.Spec.Env affected original")
	}
	if copy.Spec.Security.Privileged == original.Spec.Security.Privileged {
		t.Error("mutating copy.Spec.Security affected original")
	}
	if copy.Spec.Credentials[0].SecretName == original.Spec.Credentials[0].SecretName {
		t.Error("mutating copy.Spec.Credentials affected original")
	}
	if copy.Spec.CreatedBy.Email == original.Spec.CreatedBy.Email {
		t.Error("mutating copy.Spec.CreatedBy affected original")
	}
	if copy.Spec.Sharing.Users[0].Identity == original.Spec.Sharing.Users[0].Identity {
		t.Error("mutating copy.Spec.Sharing.Users affected original")
	}
	if copy.Spec.Sharing.ShareLinks[0].TokenHash == original.Spec.Sharing.ShareLinks[0].TokenHash {
		t.Error("mutating copy.Spec.Sharing.ShareLinks affected original")
	}
	if copy.Status.Conditions[0].Reason == original.Status.Conditions[0].Reason {
		t.Error("mutating copy.Status.Conditions affected original")
	}
	if copy.Status.ForwardedPorts[0].PreviewURL == original.Status.ForwardedPorts[0].PreviewURL {
		t.Error("mutating copy.Status.ForwardedPorts affected original")
	}
	if copy.Status.AgentConfigure.InstallCommandHash == original.Status.AgentConfigure.InstallCommandHash {
		t.Error("mutating copy.Status.AgentConfigure affected original")
	}
	if copy.Status.ResolvedAgentSpec.Entrypoint[0] == original.Status.ResolvedAgentSpec.Entrypoint[0] {
		t.Error("mutating copy.Status.ResolvedAgentSpec affected original")
	}
}

func TestSandbox_DeepCopy_NilReceiver(t *testing.T) {
	var s *Sandbox
	if got := s.DeepCopy(); got != nil {
		t.Errorf("DeepCopy on nil *Sandbox should return nil, got %+v", got)
	}
}

func TestSandbox_DeepCopyObject_ReturnsRuntimeObject(t *testing.T) {
	original := fullSandbox()
	obj := original.DeepCopyObject()

	copy, ok := obj.(*Sandbox)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *Sandbox", obj)
	}
	if copy.Name != original.Name {
		t.Errorf("copy.Name = %q, want %q", copy.Name, original.Name)
	}
}

func TestSandboxList_DeepCopy(t *testing.T) {
	original := &SandboxList{
		Items: []Sandbox{*fullSandbox(), *fullSandbox()},
	}
	copy := original.DeepCopy()

	if len(copy.Items) != len(original.Items) {
		t.Fatalf("copy has %d items, want %d", len(copy.Items), len(original.Items))
	}

	copy.Items[0].Spec.Image.Repository = "mutated"
	if original.Items[0].Spec.Image.Repository == "mutated" {
		t.Error("mutating copy.Items affected original items (slice element aliasing)")
	}

	obj := original.DeepCopyObject()
	if _, ok := obj.(*SandboxList); !ok {
		t.Fatalf("DeepCopyObject returned %T, want *SandboxList", obj)
	}
}

func TestSandboxSpec_DeepCopy_NilOptionalFields(t *testing.T) {
	// A minimal spec with every optional pointer/slice/map left nil must
	// copy cleanly without panicking — this is the branch the generated
	// code takes for the vast majority of real sandboxes (most fields are
	// optional and template-inherited).
	original := &SandboxSpec{Mode: SandboxModeCode}
	copy := original.DeepCopy()

	if copy.Mode != original.Mode {
		t.Errorf("copy.Mode = %q, want %q", copy.Mode, original.Mode)
	}
	if copy.TemplateRef != nil || copy.Image != nil || copy.Storage != nil {
		t.Error("expected nil optional fields to remain nil after DeepCopy")
	}
}

func TestSandboxStatus_DeepCopy_NilOptionalFields(t *testing.T) {
	original := &SandboxStatus{Phase: SandboxPhaseCreating}
	copy := original.DeepCopy()

	if copy.Phase != original.Phase {
		t.Errorf("copy.Phase = %q, want %q", copy.Phase, original.Phase)
	}
	if copy.StartedAt != nil || copy.AgentConfigure != nil || copy.ResolvedAgentSpec != nil {
		t.Error("expected nil optional fields to remain nil after DeepCopy")
	}
}

func ptrTo[T any](v T) *T { return &v }
