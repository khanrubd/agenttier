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

// TestLeafTypes_DeepCopy_NilReceiver exercises the nil-receiver branch of
// every generated DeepCopy() method that fullSandbox/fullSandboxTemplateSpec
// don't reach directly (they only invoke these through a non-nil parent's
// DeepCopyInto). Kubernetes generated clients call DeepCopy() on nil
// pointers routinely (e.g. copying a struct with an unset optional field
// fetched from a partially-populated object), so the nil-safety guarantee
// is worth asserting for every type, not just the top-level CRs.
func TestLeafTypes_DeepCopy_NilReceiver(t *testing.T) {
	if got := (*AgentConfigureStatus)(nil).DeepCopy(); got != nil {
		t.Errorf("(*AgentConfigureStatus)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*AgentSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*AgentSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*ConstraintsSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*ConstraintsSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*CredentialRef)(nil).DeepCopy(); got != nil {
		t.Errorf("(*CredentialRef)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*FileOrRef)(nil).DeepCopy(); got != nil {
		t.Errorf("(*FileOrRef)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*FileSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*FileSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*ForwardedPort)(nil).DeepCopy(); got != nil {
		t.Errorf("(*ForwardedPort)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*HarnessSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*HarnessSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*HooksSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*HooksSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*ImageSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*ImageSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*LocalObjectReference)(nil).DeepCopy(); got != nil {
		t.Errorf("(*LocalObjectReference)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*NetworkPort)(nil).DeepCopy(); got != nil {
		t.Errorf("(*NetworkPort)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*NetworkRule)(nil).DeepCopy(); got != nil {
		t.Errorf("(*NetworkRule)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*NetworkSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*NetworkSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*SecuritySpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*SecuritySpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*ServiceReference)(nil).DeepCopy(); got != nil {
		t.Errorf("(*ServiceReference)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*ShareLink)(nil).DeepCopy(); got != nil {
		t.Errorf("(*ShareLink)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*SharePermission)(nil).DeepCopy(); got != nil {
		t.Errorf("(*SharePermission)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*SharingSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*SharingSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*SkillSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*SkillSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*StorageSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*StorageSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*TemplateReference)(nil).DeepCopy(); got != nil {
		t.Errorf("(*TemplateReference)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*ToolSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*ToolSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*UserIdentity)(nil).DeepCopy(); got != nil {
		t.Errorf("(*UserIdentity)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*SandboxSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*SandboxSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*SandboxStatus)(nil).DeepCopy(); got != nil {
		t.Errorf("(*SandboxStatus)(nil).DeepCopy() = %+v, want nil", got)
	}
	if got := (*SandboxTemplateSpec)(nil).DeepCopy(); got != nil {
		t.Errorf("(*SandboxTemplateSpec)(nil).DeepCopy() = %+v, want nil", got)
	}
}

// TestLeafTypes_DeepCopy_IndependentCopy directly invokes DeepCopy() on
// every leaf type with all fields populated, confirming the returned value
// is a distinct, independently-mutable copy. Nested-struct round-trip
// tests elsewhere in this package exercise these types via DeepCopyInto;
// this test closes the coverage gap on the standalone DeepCopy() wrapper
// entry points, which Kubernetes clients call directly (e.g. copying a
// single sub-struct fetched off a live object without copying the whole
// parent).
func TestLeafTypes_DeepCopy_IndependentCopy(t *testing.T) {
	t.Run("CredentialRef", func(t *testing.T) {
		original := &CredentialRef{SecretName: "creds", MountAs: "env", EnvPrefix: "APP_"}
		copy := original.DeepCopy()
		copy.SecretName = "mutated"
		if original.SecretName == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("ImageSpec", func(t *testing.T) {
		original := &ImageSpec{Repository: "repo", PullPolicy: corev1.PullAlways}
		copy := original.DeepCopy()
		copy.Repository = "mutated"
		if original.Repository == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("StorageSpec", func(t *testing.T) {
		original := &StorageSpec{Size: resource.MustParse("10Gi"), StorageClass: "gp3"}
		copy := original.DeepCopy()
		copy.Size = resource.MustParse("1Gi")
		if original.Size.String() == copy.Size.String() {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("NetworkSpec", func(t *testing.T) {
		original := &NetworkSpec{EgressRules: []NetworkRule{{CIDR: "10.0.0.0/8"}}}
		copy := original.DeepCopy()
		copy.EgressRules[0].CIDR = "mutated"
		if original.EgressRules[0].CIDR == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("NetworkRule", func(t *testing.T) {
		original := &NetworkRule{CIDR: "10.0.0.0/8", Ports: []NetworkPort{{Port: 80}}}
		copy := original.DeepCopy()
		copy.Ports[0].Port = 443
		if original.Ports[0].Port == 443 {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("NetworkPort", func(t *testing.T) {
		original := &NetworkPort{Protocol: corev1.ProtocolTCP, Port: 80}
		copy := original.DeepCopy()
		copy.Port = 443
		if original.Port == 443 {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("ServiceReference", func(t *testing.T) {
		original := &ServiceReference{Name: "svc", Namespace: "default", Port: 8080}
		copy := original.DeepCopy()
		copy.Name = "mutated"
		if original.Name == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("SecuritySpec", func(t *testing.T) {
		original := &SecuritySpec{Privileged: true}
		copy := original.DeepCopy()
		copy.Privileged = false
		if original.Privileged == copy.Privileged {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("UserIdentity", func(t *testing.T) {
		original := &UserIdentity{Sub: "sub-1", Email: "a@b.com"}
		copy := original.DeepCopy()
		copy.Email = "mutated"
		if original.Email == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("SharingSpec", func(t *testing.T) {
		original := &SharingSpec{Users: []SharePermission{{Identity: "u1", Level: "viewer"}}}
		copy := original.DeepCopy()
		copy.Users[0].Identity = "mutated"
		if original.Users[0].Identity == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("SharePermission", func(t *testing.T) {
		original := &SharePermission{Identity: "u1", Level: "viewer"}
		copy := original.DeepCopy()
		copy.Identity = "mutated"
		if original.Identity == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("ShareLink", func(t *testing.T) {
		expires := metav1.Now()
		original := &ShareLink{ID: "link-1", TokenHash: "hash", Level: "viewer", ExpiresAt: &expires}
		copy := original.DeepCopy()
		copy.TokenHash = "mutated"
		*copy.ExpiresAt = metav1.NewTime(expires.Add(3600))
		if original.TokenHash == "mutated" {
			t.Error("mutation leaked to original")
		}
		if original.ExpiresAt.Equal(copy.ExpiresAt) {
			t.Error("mutating copy.ExpiresAt affected original (pointer aliasing)")
		}
	})

	t.Run("ForwardedPort", func(t *testing.T) {
		original := &ForwardedPort{Port: 8080, PreviewURL: "https://example.com"}
		copy := original.DeepCopy()
		copy.PreviewURL = "mutated"
		if original.PreviewURL == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("TemplateReference", func(t *testing.T) {
		original := &TemplateReference{Name: "base", Kind: "SandboxTemplate"}
		copy := original.DeepCopy()
		copy.Name = "mutated"
		if original.Name == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("LocalObjectReference", func(t *testing.T) {
		original := &LocalObjectReference{Name: "cm-1"}
		copy := original.DeepCopy()
		copy.Name = "mutated"
		if original.Name == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("HooksSpec", func(t *testing.T) {
		original := &HooksSpec{OnStart: "start", OnStop: "stop"}
		copy := original.DeepCopy()
		copy.OnStart = "mutated"
		if original.OnStart == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("ConstraintsSpec", func(t *testing.T) {
		maxSize := resource.MustParse("100Mi")
		original := &ConstraintsSpec{MaxFileSize: &maxSize, RestrictedCommands: []string{"rm"}}
		copy := original.DeepCopy()
		copy.RestrictedCommands[0] = "mutated"
		*copy.MaxFileSize = resource.MustParse("1Mi")
		if original.RestrictedCommands[0] == "mutated" {
			t.Error("mutation leaked to original")
		}
		if original.MaxFileSize.String() == copy.MaxFileSize.String() {
			t.Error("mutating copy.MaxFileSize affected original (pointer aliasing)")
		}
	})

	t.Run("FileOrRef", func(t *testing.T) {
		original := &FileOrRef{Content: "hello", Path: "/etc/foo"}
		copy := original.DeepCopy()
		copy.Content = "mutated"
		if original.Content == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("FileSpec", func(t *testing.T) {
		mode := int32(0644)
		original := &FileSpec{Path: "/etc/foo", Content: "hello", Mode: &mode}
		copy := original.DeepCopy()
		copy.Content = "mutated"
		*copy.Mode = 0600
		if original.Content == "mutated" {
			t.Error("mutation leaked to original")
		}
		if *original.Mode == *copy.Mode {
			t.Error("mutating copy.Mode affected original (pointer aliasing)")
		}
	})

	t.Run("SkillSpec", func(t *testing.T) {
		original := &SkillSpec{Name: "skill-1", Content: &FileOrRef{Content: "body"}}
		copy := original.DeepCopy()
		copy.Content.Content = "mutated"
		if original.Content.Content == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("ToolSpec", func(t *testing.T) {
		original := &ToolSpec{Name: "node", Version: ">=18"}
		copy := original.DeepCopy()
		copy.Version = "mutated"
		if original.Version == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("HarnessSpec", func(t *testing.T) {
		original := &HarnessSpec{Command: []string{"/bin/bash"}, Shell: "/bin/bash"}
		copy := original.DeepCopy()
		copy.Command[0] = "mutated"
		if original.Command[0] == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("AgentSpec", func(t *testing.T) {
		original := &AgentSpec{Entrypoint: []string{"python", "agent.py"}}
		copy := original.DeepCopy()
		copy.Entrypoint[0] = "mutated"
		if original.Entrypoint[0] == "mutated" {
			t.Error("mutation leaked to original")
		}
	})

	t.Run("AgentConfigureStatus", func(t *testing.T) {
		now := metav1.Now()
		original := &AgentConfigureStatus{LastConfiguredAt: &now, Entrypoint: []string{"python"}}
		copy := original.DeepCopy()
		copy.Entrypoint[0] = "mutated"
		if original.Entrypoint[0] == "mutated" {
			t.Error("mutation leaked to original")
		}
	})
}
