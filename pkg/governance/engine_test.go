/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package governance

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func sandboxWith(mods func(s *agenttierv1alpha1.Sandbox)) *agenttierv1alpha1.Sandbox {
	s := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{
			TemplateRef: &agenttierv1alpha1.TemplateReference{Name: "general-coding"},
			CreatedBy:   &agenttierv1alpha1.UserIdentity{Sub: "user-1"},
		},
	}
	if mods != nil {
		mods(s)
	}
	return s
}

func TestCheck_NoLimits(t *testing.T) {
	v := Check(Policy{}, Usage{}, sandboxWith(nil))
	if v.Violated() {
		t.Fatalf("expected no violations, got %v", v)
	}
}

func TestCheck_TemplateAllowlist(t *testing.T) {
	policy := Policy{AllowedTemplates: []string{"claude-code-bedrock"}}

	ok := Check(policy, Usage{}, sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.TemplateRef.Name = "claude-code-bedrock"
	}))
	if ok.Violated() {
		t.Fatalf("allowed template should pass: %v", ok)
	}

	bad := Check(policy, Usage{}, sandboxWith(nil)) // template = general-coding
	if !bad.Violated() {
		t.Fatal("disallowed template should violate")
	}
	if bad[0].Code != "template_not_allowed" {
		t.Fatalf("expected template_not_allowed, got %s", bad[0].Code)
	}
}

func TestCheck_UserQuotaExceeded(t *testing.T) {
	policy := Policy{MaxSandboxesPerUser: 2}

	// Under quota.
	v := Check(policy, Usage{UserSandboxes: 1}, sandboxWith(nil))
	if v.Violated() {
		t.Fatalf("under quota should pass: %v", v)
	}

	// At quota — creating one more would exceed.
	v = Check(policy, Usage{UserSandboxes: 2}, sandboxWith(nil))
	if !v.Violated() || v[0].Code != "user_quota_exceeded" {
		t.Fatalf("at quota should violate user_quota_exceeded, got %v", v)
	}
}

func TestCheck_NamespaceQuotaExceeded(t *testing.T) {
	policy := Policy{MaxSandboxesTotal: 5}
	v := Check(policy, Usage{TotalSandboxes: 5}, sandboxWith(nil))
	if !v.Violated() || v[0].Code != "namespace_quota_exceeded" {
		t.Fatalf("expected namespace_quota_exceeded, got %v", v)
	}
}

func TestCheck_ResourceCaps(t *testing.T) {
	policy := Policy{MaxCPU: "2", MaxMemory: "4Gi", MaxStorage: "20Gi"}

	// All within limits.
	s := sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Resources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				"cpu":    resource.MustParse("1"),
				"memory": resource.MustParse("2Gi"),
			},
		}
		s.Spec.Storage = &agenttierv1alpha1.StorageSpec{Size: resource.MustParse("10Gi")}
	})
	if v := Check(policy, Usage{}, s); v.Violated() {
		t.Fatalf("within-limits sandbox should pass: %v", v)
	}

	// CPU over.
	s = sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Resources = &corev1.ResourceRequirements{
			Limits: corev1.ResourceList{"cpu": resource.MustParse("4")},
		}
	})
	v := Check(policy, Usage{}, s)
	if !v.Violated() || v[0].Code != "cpu_limit_exceeded" {
		t.Fatalf("expected cpu_limit_exceeded, got %v", v)
	}

	// Storage over.
	s = sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Storage = &agenttierv1alpha1.StorageSpec{Size: resource.MustParse("50Gi")}
	})
	v = Check(policy, Usage{}, s)
	if !v.Violated() || v[0].Code != "storage_limit_exceeded" {
		t.Fatalf("expected storage_limit_exceeded, got %v", v)
	}
}

func TestCheck_TimeoutCap(t *testing.T) {
	policy := Policy{MaxTimeout: "4h", MaxIdleTimeout: "1h"}

	// Sandbox well within limits.
	s := sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Timeout = &metav1.Duration{Duration: 2 * time.Hour}
		s.Spec.IdleTimeout = &metav1.Duration{Duration: 30 * time.Minute}
	})
	if v := Check(policy, Usage{}, s); v.Violated() {
		t.Fatalf("within timeout caps should pass: %v", v)
	}

	// Timeout too long.
	s = sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Timeout = &metav1.Duration{Duration: 24 * time.Hour}
	})
	v := Check(policy, Usage{}, s)
	if !v.Violated() || v[0].Code != "timeout_exceeded" {
		t.Fatalf("expected timeout_exceeded, got %v", v)
	}

	// "Infinite" (0) also violates a cap.
	s = sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Timeout = &metav1.Duration{Duration: 0}
	})
	v = Check(policy, Usage{}, s)
	if !v.Violated() || v[0].Code != "timeout_exceeded" {
		t.Fatalf("expected timeout_exceeded for 0 (infinite), got %v", v)
	}
}

func TestCheck_RegistryAllowlist(t *testing.T) {
	policy := Policy{ApprovedRegistries: []string{"ghcr.io/agenttier"}}

	// No sandbox image override — trusts template.
	if v := Check(policy, Usage{}, sandboxWith(nil)); v.Violated() {
		t.Fatalf("no image override should pass: %v", v)
	}

	// Approved override.
	s := sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Image = &agenttierv1alpha1.ImageSpec{Repository: "ghcr.io/agenttier/sandbox-custom:latest"}
	})
	if v := Check(policy, Usage{}, s); v.Violated() {
		t.Fatalf("approved image should pass: %v", v)
	}

	// Disallowed override.
	s = sandboxWith(func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Image = &agenttierv1alpha1.ImageSpec{Repository: "docker.io/random/image:latest"}
	})
	v := Check(policy, Usage{}, s)
	if !v.Violated() || v[0].Code != "image_registry_not_approved" {
		t.Fatalf("expected image_registry_not_approved, got %v", v)
	}
}

func TestMergePolicies(t *testing.T) {
	cluster := Policy{
		MaxCPU:              "4",
		MaxMemory:           "8Gi",
		MaxSandboxesPerUser: 5,
		AllowedTemplates:    []string{"a", "b"},
	}
	ns := Policy{
		MaxCPU:              "2",           // override
		MaxSandboxesPerUser: 0,             // don't override (zero means no limit)
		AllowedTemplates:    []string{"c"}, // override
	}
	merged := mergePolicies(cluster, ns)

	if merged.MaxCPU != "2" {
		t.Errorf("expected MaxCPU overridden to 2, got %s", merged.MaxCPU)
	}
	if merged.MaxMemory != "8Gi" {
		t.Errorf("expected MaxMemory inherited as 8Gi, got %s", merged.MaxMemory)
	}
	if merged.MaxSandboxesPerUser != 5 {
		t.Errorf("expected MaxSandboxesPerUser inherited as 5, got %d", merged.MaxSandboxesPerUser)
	}
	if len(merged.AllowedTemplates) != 1 || merged.AllowedTemplates[0] != "c" {
		t.Errorf("expected AllowedTemplates overridden to [c], got %v", merged.AllowedTemplates)
	}
}

func TestCountUsage(t *testing.T) {
	list := &agenttierv1alpha1.SandboxList{
		Items: []agenttierv1alpha1.Sandbox{
			{
				Spec: agenttierv1alpha1.SandboxSpec{
					CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "u1"},
				},
				Status: agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseRunning},
			},
			{
				Spec: agenttierv1alpha1.SandboxSpec{
					CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "u1"},
				},
				Status: agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseStopped},
			},
			{
				Spec: agenttierv1alpha1.SandboxSpec{
					CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "u2"},
				},
				Status: agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseRunning},
			},
			{
				// Error state — should not count.
				Spec: agenttierv1alpha1.SandboxSpec{
					CreatedBy: &agenttierv1alpha1.UserIdentity{Sub: "u1"},
				},
				Status: agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseError},
			},
		},
	}

	got := CountUsage(list, "u1")
	if got.TotalSandboxes != 3 {
		t.Errorf("expected 3 total (error excluded), got %d", got.TotalSandboxes)
	}
	if got.UserSandboxes != 2 {
		t.Errorf("expected 2 for u1, got %d", got.UserSandboxes)
	}
}
