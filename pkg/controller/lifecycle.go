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
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// StopSandbox stops a running sandbox: executes onStop hook, deletes pod, preserves PVC.
func (r *SandboxReconciler) StopSandbox(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, reason string) error {
	logger := log.FromContext(ctx)

	// Validate state transition
	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseRunning {
		return fmt.Errorf("cannot stop sandbox in phase %s: must be Running", sandbox.Status.Phase)
	}

	logger.Info("stopping sandbox", "sandbox", sandbox.Name, "reason", reason)

	// Execute onStop hook if defined
	// TODO: Execute hook via exec into pod (requires template resolution)

	// Delete the pod
	if sandbox.Status.PodName != "" {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sandbox.Status.PodName,
				Namespace: sandbox.Namespace,
			},
		}
		if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete pod: %w", err)
		}
		logger.Info("deleted pod", "pod", sandbox.Status.PodName)
	}

	// Update status — PVC is preserved
	sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseStopped
	sandbox.Status.PodName = ""
	sandbox.Status.StartedAt = nil
	sandbox.Status.Message = reason

	if err := r.Status().Update(ctx, sandbox); err != nil {
		return fmt.Errorf("failed to update sandbox status: %w", err)
	}

	r.Recorder.Eventf(sandbox, corev1.EventTypeNormal, "Stopped", "Sandbox stopped: %s", reason)
	return nil
}

// ResumeSandbox resumes a stopped sandbox: creates new pod with existing PVC.
func (r *SandboxReconciler) ResumeSandbox(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) error {
	logger := log.FromContext(ctx)

	// Validate state transition
	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped &&
		sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseError {
		return fmt.Errorf("cannot resume sandbox in phase %s: must be Stopped or Error", sandbox.Status.Phase)
	}

	// Verify PVC still exists
	if sandbox.Status.PVCName != "" {
		pvc := &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, client.ObjectKey{Namespace: sandbox.Namespace, Name: sandbox.Status.PVCName}, pvc)
		if err != nil {
			if errors.IsNotFound(err) {
				return fmt.Errorf("PVC %s not found — cannot resume without persistent storage", sandbox.Status.PVCName)
			}
			return fmt.Errorf("failed to check PVC: %w", err)
		}
	}

	logger.Info("resuming sandbox", "sandbox", sandbox.Name)

	// Transition to Creating — the reconcileCreating handler will create the new pod
	sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseCreating
	sandbox.Status.Message = "Resuming"
	now := metav1.Now()
	sandbox.Status.StartedAt = &now

	if err := r.Status().Update(ctx, sandbox); err != nil {
		return fmt.Errorf("failed to update sandbox status: %w", err)
	}

	r.Recorder.Event(sandbox, corev1.EventTypeNormal, "Resumed", "Sandbox resume initiated")
	return nil
}

// ValidateStateTransition checks if a transition from current phase to target action is valid.
func ValidateStateTransition(currentPhase agenttierv1alpha1.SandboxPhase, action string) error {
	validTransitions := map[agenttierv1alpha1.SandboxPhase][]string{
		agenttierv1alpha1.SandboxPhaseCreating: {"running", "error"},
		agenttierv1alpha1.SandboxPhaseRunning:  {"stop", "error", "delete"},
		agenttierv1alpha1.SandboxPhaseStopped:  {"resume", "delete"},
		agenttierv1alpha1.SandboxPhaseError:    {"resume", "stop", "delete"},
	}

	allowed, exists := validTransitions[currentPhase]
	if !exists {
		return fmt.Errorf("unknown phase: %s", currentPhase)
	}

	for _, a := range allowed {
		if a == action {
			return nil
		}
	}

	return fmt.Errorf("invalid transition: cannot %s sandbox in phase %s (allowed: %v)", action, currentPhase, allowed)
}

// executeHook runs a lifecycle hook script inside the sandbox pod.
// TODO(13.10): wire into state machine when self-healing/hook execution lands.
//
//nolint:unused // retained for task 13.10 (self-healing and hooks)
func (r *SandboxReconciler) executeHook(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, hookName, script string) error {
	if script == "" {
		return nil
	}

	logger := log.FromContext(ctx)
	logger.Info("executing lifecycle hook", "hook", hookName, "sandbox", sandbox.Name)

	// TODO: Implement exec into pod to run hook script
	// This requires the remotecommand package from client-go
	// For now, log that the hook would be executed
	logger.Info("hook execution placeholder", "hook", hookName, "script_length", len(script))

	return nil
}

// calculateBackoffDelay returns the exponential backoff delay for the given restart count.
// Delays: 10s, 20s, 40s, 80s, 160s (capped at 160s).
func calculateBackoffDelay(restartCount int) time.Duration {
	delay := time.Duration(10<<uint(restartCount)) * time.Second
	maxDelay := 160 * time.Second
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

// isInfrastructureFailure determines if a pod termination was caused by infrastructure
// (OOM, eviction, node failure) vs user-initiated stop.
func isInfrastructureFailure(pod *corev1.Pod) bool {
	if pod == nil {
		return true // Pod disappeared entirely — infrastructure failure
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			reason := cs.State.Terminated.Reason
			switch reason {
			case "OOMKilled", "Evicted":
				return true
			case "Completed":
				return false // Normal exit
			}
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}

	// Check pod-level reasons
	switch pod.Status.Reason {
	case "Evicted", "NodeLost", "UnexpectedAdmissionError":
		return true
	}

	return true // Default to infrastructure failure (safer — triggers restart)
}
