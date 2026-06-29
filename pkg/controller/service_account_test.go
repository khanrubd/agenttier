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

package controller

import (
	"testing"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TestMergeServiceAccount_Precedence guards the fix for the dead
// per-sandbox IRSA field: spec.serviceAccount (sandbox > template) now flows
// into MergedPodConfig.ServiceAccount, which the PodBuilder applies to the
// Pod's ServiceAccountName.
func TestMergeServiceAccount_Precedence(t *testing.T) {
	tmpl := &agenttierv1alpha1.SandboxTemplateSpec{ServiceAccount: "tmpl-sa"}

	// Template value used when the sandbox doesn't set one.
	cfg := MergeSandboxWithTemplate(&agenttierv1alpha1.SandboxSpec{}, tmpl, &ControllerDefaults{})
	if cfg.ServiceAccount != "tmpl-sa" {
		t.Errorf("expected template SA 'tmpl-sa', got %q", cfg.ServiceAccount)
	}

	// Sandbox value overrides the template.
	cfg = MergeSandboxWithTemplate(&agenttierv1alpha1.SandboxSpec{ServiceAccount: "sbx-sa"}, tmpl, &ControllerDefaults{})
	if cfg.ServiceAccount != "sbx-sa" {
		t.Errorf("expected sandbox SA 'sbx-sa' to win, got %q", cfg.ServiceAccount)
	}

	// Neither set → empty (namespace default applies, prior behavior).
	cfg = MergeSandboxWithTemplate(&agenttierv1alpha1.SandboxSpec{}, &agenttierv1alpha1.SandboxTemplateSpec{}, &ControllerDefaults{})
	if cfg.ServiceAccount != "" {
		t.Errorf("expected empty SA when unset, got %q", cfg.ServiceAccount)
	}
}

// TestPodBuilder_AppliesServiceAccount confirms the merged ServiceAccount
// lands on the Pod's ServiceAccountName.
func TestPodBuilder_AppliesServiceAccount(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{ServiceAccount: "irsa-sa"},
	}
	sandbox.Name = "sb-irsa"
	sandbox.Namespace = "default"

	cfg := MergeSandboxWithTemplate(&sandbox.Spec, nil, &ControllerDefaults{Image: "busybox"})
	pb := &PodBuilder{DefaultImage: "busybox"}
	pod := pb.Build(sandbox, cfg)

	if pod.Spec.ServiceAccountName != "irsa-sa" {
		t.Errorf("pod ServiceAccountName = %q, want irsa-sa", pod.Spec.ServiceAccountName)
	}
}
