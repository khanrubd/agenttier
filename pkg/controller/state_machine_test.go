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

func TestValidateAction_ValidTransitions(t *testing.T) {
	tests := []struct {
		name   string
		phase  agenttierv1alpha1.SandboxPhase
		action Action
	}{
		{"Running can stop", agenttierv1alpha1.SandboxPhaseRunning, ActionStop},
		{"Running can delete", agenttierv1alpha1.SandboxPhaseRunning, ActionDelete},
		{"Stopped can resume", agenttierv1alpha1.SandboxPhaseStopped, ActionResume},
		{"Stopped can delete", agenttierv1alpha1.SandboxPhaseStopped, ActionDelete},
		{"Error can resume", agenttierv1alpha1.SandboxPhaseError, ActionResume},
		{"Error can stop", agenttierv1alpha1.SandboxPhaseError, ActionStop},
		{"Error can delete", agenttierv1alpha1.SandboxPhaseError, ActionDelete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAction(tt.phase, tt.action)
			if err != nil {
				t.Errorf("expected valid transition %s -> %s, got error: %v", tt.phase, tt.action, err)
			}
		})
	}
}

func TestValidateAction_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name   string
		phase  agenttierv1alpha1.SandboxPhase
		action Action
	}{
		{"Running cannot resume", agenttierv1alpha1.SandboxPhaseRunning, ActionResume},
		{"Stopped cannot stop", agenttierv1alpha1.SandboxPhaseStopped, ActionStop},
		{"Creating cannot stop", agenttierv1alpha1.SandboxPhaseCreating, ActionStop},
		{"Creating cannot resume", agenttierv1alpha1.SandboxPhaseCreating, ActionResume},
		{"Creating cannot delete", agenttierv1alpha1.SandboxPhaseCreating, ActionDelete},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAction(tt.phase, tt.action)
			if err == nil {
				t.Errorf("expected invalid transition %s -> %s to return error", tt.phase, tt.action)
			}
			if !IsInvalidTransition(err) {
				t.Errorf("expected InvalidTransitionError, got: %T", err)
			}
		})
	}
}

func TestValidateAction_UnknownPhase(t *testing.T) {
	err := ValidateAction("Unknown", ActionStop)
	if err == nil {
		t.Error("expected error for unknown phase")
	}
}

func TestCanTransition(t *testing.T) {
	if !CanTransition(agenttierv1alpha1.SandboxPhaseRunning, ActionStop) {
		t.Error("expected Running -> Stop to be valid")
	}
	if CanTransition(agenttierv1alpha1.SandboxPhaseStopped, ActionStop) {
		t.Error("expected Stopped -> Stop to be invalid")
	}
}
