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
	"fmt"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// Action represents a lifecycle action that can be performed on a sandbox.
type Action string

const (
	ActionStop   Action = "stop"
	ActionResume Action = "resume"
	ActionDelete Action = "delete"
)

// validTransitions defines the allowed state transitions for the sandbox state machine.
// Key: current phase, Value: list of allowed actions.
var validTransitions = map[agenttierv1alpha1.SandboxPhase][]Action{
	agenttierv1alpha1.SandboxPhaseCreating: {},                                       // Creating can only transition internally (to Running or Error)
	agenttierv1alpha1.SandboxPhaseRunning:  {ActionStop, ActionDelete},               // Running → Stopped or Deleting
	agenttierv1alpha1.SandboxPhaseStopped:  {ActionResume, ActionDelete},             // Stopped → Running or Deleting
	agenttierv1alpha1.SandboxPhaseError:    {ActionResume, ActionStop, ActionDelete}, // Error → Running, Stopped, or Deleting
}

// ValidateAction checks if the given action is valid for the sandbox's current phase.
// Returns nil if valid, or an error describing why the transition is invalid.
func ValidateAction(phase agenttierv1alpha1.SandboxPhase, action Action) error {
	allowed, exists := validTransitions[phase]
	if !exists {
		return fmt.Errorf("unknown sandbox phase: %q", phase)
	}

	for _, a := range allowed {
		if a == action {
			return nil
		}
	}

	return &InvalidTransitionError{
		CurrentPhase: phase,
		Action:       action,
		Allowed:      allowed,
	}
}

// InvalidTransitionError is returned when an invalid state transition is attempted.
type InvalidTransitionError struct {
	CurrentPhase agenttierv1alpha1.SandboxPhase
	Action       Action
	Allowed      []Action
}

func (e *InvalidTransitionError) Error() string {
	if len(e.Allowed) == 0 {
		return fmt.Sprintf("cannot perform action %q on sandbox in phase %q: no actions allowed in this phase", e.Action, e.CurrentPhase)
	}
	return fmt.Sprintf("cannot perform action %q on sandbox in phase %q: allowed actions are %v", e.Action, e.CurrentPhase, e.Allowed)
}

// IsInvalidTransition returns true if the error is an InvalidTransitionError.
func IsInvalidTransition(err error) bool {
	_, ok := err.(*InvalidTransitionError)
	return ok
}

// CanTransition returns true if the given action is valid for the current phase.
func CanTransition(phase agenttierv1alpha1.SandboxPhase, action Action) bool {
	return ValidateAction(phase, action) == nil
}
