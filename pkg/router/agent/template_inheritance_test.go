/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agent

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TestResolveAgentCaps_PrefersStatusOverDirectTemplate verifies the fix for
// "Template inheritance not walked when resolving agent caps." The
// controller writes the merged AgentSpec (after walking the inheritance
// chain) onto Sandbox.status.resolvedAgentSpec at create time. The agent
// handler must read from there, not redo a direct-template lookup.
//
// Scenario: a child template inherits MaxConcurrentInvokes from a parent
// (so the child's directly-set value is nil but the merged value is 5).
// We simulate this by setting status.resolvedAgentSpec.MaxConcurrentInvokes=5
// without setting any TemplateRef. resolveAgentCaps must return 5.
func TestResolveAgentCaps_PrefersStatusOverDirectTemplate(t *testing.T) {
	maxConcurrent := int32(5)
	defaultTimeout := metav1.Duration{Duration: 90 * time.Second}

	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "child-sb", Namespace: "default"},
		Spec: agenttierv1alpha1.SandboxSpec{
			Mode: agenttierv1alpha1.SandboxModeAgent,
			// No TemplateRef — the controller has already merged the chain
			// and stashed the result on status. Reading from status alone
			// must produce the right caps.
		},
		Status: agenttierv1alpha1.SandboxStatus{
			ResolvedAgentSpec: &agenttierv1alpha1.AgentSpec{
				Entrypoint:           []string{"python", "agent.py"},
				MaxConcurrentInvokes: &maxConcurrent,
				DefaultInvokeTimeout: &defaultTimeout,
			},
		},
	}

	h, _ := buildHandler(t, sb, nil)
	got, gotTimeout := h.resolveAgentCaps(context.Background(), sb)

	if got != maxConcurrent {
		t.Errorf("MaxConcurrentInvokes = %d, want %d (status path took priority over direct lookup)", got, maxConcurrent)
	}
	if gotTimeout != defaultTimeout.Duration {
		t.Errorf("DefaultInvokeTimeout = %v, want %v", gotTimeout, defaultTimeout.Duration)
	}
}

// TestResolveAgentCaps_LegacySandboxFallback verifies that sandboxes created
// before the status field existed still work. With no status.resolvedAgentSpec
// and no template ref either, we return zeros (= "use Router defaults").
func TestResolveAgentCaps_LegacySandboxFallback(t *testing.T) {
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy-sb", Namespace: "default"},
		Spec: agenttierv1alpha1.SandboxSpec{
			Mode: agenttierv1alpha1.SandboxModeAgent,
		},
		// No status, no template ref.
	}

	h, _ := buildHandler(t, sb, nil)
	got, gotTimeout := h.resolveAgentCaps(context.Background(), sb)

	if got != 0 {
		t.Errorf("MaxConcurrentInvokes = %d, want 0 (should fall through to Router default)", got)
	}
	if gotTimeout != 0 {
		t.Errorf("DefaultInvokeTimeout = %v, want 0", gotTimeout)
	}
}
