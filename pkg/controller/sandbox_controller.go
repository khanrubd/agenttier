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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/controller/warmpool"
)

const (
	FinalizerName       = "agenttier.io/sandbox-cleanup"
	DefaultRequeueDelay = 30 * time.Second
	MaxRestartCount     = 5
)

// SandboxReconciler reconciles Sandbox objects.
type SandboxReconciler struct {
	client.Client
	Scheme             *runtime.Scheme
	Recorder           record.EventRecorder
	MaxConcurrency     int
	DefaultImage       string
	DefaultStorageSize string
	DefaultMountPath   string
}

func (r *SandboxReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	sandbox := &agenttierv1alpha1.Sandbox{}
	if err := r.Get(ctx, req.NamespacedName, sandbox); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !sandbox.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, sandbox)
	}

	// Ensure finalizer
	if !controllerutil.ContainsFinalizer(sandbox, FinalizerName) {
		controllerutil.AddFinalizer(sandbox, FinalizerName)
		if err := r.Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// State machine dispatch
	switch sandbox.Status.Phase {
	case "", agenttierv1alpha1.SandboxPhaseCreating:
		return r.reconcileCreating(ctx, sandbox)
	case agenttierv1alpha1.SandboxPhaseRunning:
		return r.reconcileRunning(ctx, sandbox)
	case agenttierv1alpha1.SandboxPhaseStopped:
		return r.reconcileStopped(ctx, sandbox)
	case agenttierv1alpha1.SandboxPhaseError:
		return r.reconcileError(ctx, sandbox)
	default:
		logger.Info("unknown sandbox phase", "phase", sandbox.Status.Phase)
		return ctrl.Result{}, nil
	}
}

// reconcileCreating handles the full sandbox creation flow:
// 1. Resolve template
// 2. Merge specs
// 3. Create PVC
// 4. Create NetworkPolicy
// 5. Create Pod
// 6. Wait for Pod Ready → transition to Running
func (r *SandboxReconciler) reconcileCreating(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Set phase to Creating if not already
	if sandbox.Status.Phase == "" {
		sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseCreating
		if err := r.Status().Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(sandbox, corev1.EventTypeNormal, "Creating", "Sandbox creation started")
		logger.Info("sandbox creation started", "sandbox", sandbox.Name, "startedAt", time.Now().Format(time.RFC3339Nano))
	}

	// Step 1: Resolve template
	var templateSpec *agenttierv1alpha1.SandboxTemplateSpec
	var templateName string
	if sandbox.Spec.TemplateRef != nil {
		resolver := &TemplateResolver{Client: r.Client}
		resolved, err := resolver.Resolve(ctx, sandbox.Spec.TemplateRef, sandbox.Namespace)
		if err != nil {
			return r.transitionToError(ctx, sandbox, fmt.Sprintf("Template resolution failed: %v", err))
		}
		if resolved != nil {
			templateSpec = &resolved.Spec
			templateName = resolved.Name
			sandbox.Status.ResolvedTemplate = resolved.Name
			sandbox.Status.TemplateResourceVersion = resolved.ResourceVersion
		}
	}

	// Step 1.5: Try to claim a warm pool pod (instant startup path)
	if sandbox.Status.PodName == "" && templateName != "" {
		claimedPod, claimedPVC, claimErr := warmpool.Claim(ctx, r.Client, templateName)
		if claimErr == nil && claimedPod != "" {
			logger.Info("claimed warm pool pod", "sandbox", sandbox.Name, "pod", claimedPod, "pvc", claimedPVC)

			// Relabel the claimed pod to belong to this sandbox
			pod := &corev1.Pod{}
			if err := r.Get(ctx, client.ObjectKey{Namespace: sandbox.Namespace, Name: claimedPod}, pod); err == nil {
				pod.Labels["agenttier.io/sandbox"] = sandbox.Name
				pod.Labels["agenttier.io/template"] = templateName
				if err := r.Update(ctx, pod); err != nil {
					logger.Error(err, "failed to relabel claimed pod, falling through to normal creation")
				} else {
					// Success — update sandbox status directly to Running
					now := metav1.Now()
					sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseRunning
					sandbox.Status.PodName = claimedPod
					sandbox.Status.PVCName = claimedPVC
					sandbox.Status.StartedAt = &now
					sandbox.Status.LastActivityTimestamp = &now
					sandbox.Status.Message = ""
					if err := r.Status().Update(ctx, sandbox); err != nil {
						return ctrl.Result{}, err
					}

					startupDuration := now.Time.Sub(sandbox.CreationTimestamp.Time)
					logger.Info("sandbox is running (from warm pool)",
						"sandbox", sandbox.Name,
						"pod", claimedPod,
						"startupDurationMs", startupDuration.Milliseconds(),
					)
					r.Recorder.Eventf(sandbox, corev1.EventTypeNormal, "Running", "Sandbox is ready from warm pool (startup: %s)", startupDuration.Round(time.Millisecond))

					// Ensure NetworkPolicy exists for the claimed pod
					networkSpec := sandbox.Spec.Network
					if networkSpec == nil && templateSpec != nil {
						networkSpec = templateSpec.Network
					}
					r.ensureNetworkPolicy(ctx, sandbox, networkSpec)

					return ctrl.Result{RequeueAfter: DefaultRequeueDelay}, nil
				}
			}
		}
	}

	// Step 2: Merge sandbox spec with template and defaults
	defaults := &ControllerDefaults{
		Image:     r.DefaultImage,
		MountPath: r.DefaultMountPath,
		Storage:   r.DefaultStorageSize,
	}
	mergedConfig := MergeSandboxWithTemplate(&sandbox.Spec, templateSpec, defaults)

	// Step 3: Create PVC (if not already exists)
	storageSpec := sandbox.Spec.Storage
	if storageSpec == nil && templateSpec != nil {
		storageSpec = templateSpec.Storage
	}
	pvc, err := r.ensurePVC(ctx, sandbox, storageSpec)
	if err != nil {
		return r.transitionToError(ctx, sandbox, fmt.Sprintf("PVC creation failed: %v", err))
	}
	sandbox.Status.PVCName = pvc.Name
	mergedConfig.PVCName = pvc.Name

	// Step 4: Create NetworkPolicy
	networkSpec := sandbox.Spec.Network
	if networkSpec == nil && templateSpec != nil {
		networkSpec = templateSpec.Network
	}
	if err := r.ensureNetworkPolicy(ctx, sandbox, networkSpec); err != nil {
		return r.transitionToError(ctx, sandbox, fmt.Sprintf("NetworkPolicy creation failed: %v", err))
	}

	// Step 5: Create Pod (if not already exists)
	podBuilder := &PodBuilder{DefaultImage: r.DefaultImage}
	desiredPod := podBuilder.Build(sandbox, mergedConfig)

	// Set owner reference on pod
	if err := controllerutil.SetControllerReference(sandbox, desiredPod, r.Scheme); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set owner reference on Pod: %w", err)
	}

	existingPod := &corev1.Pod{}
	err = r.Get(ctx, client.ObjectKeyFromObject(desiredPod), existingPod)
	if errors.IsNotFound(err) {
		logger.Info("creating sandbox pod", "pod", desiredPod.Name)
		if err := r.Create(ctx, desiredPod); err != nil {
			return r.transitionToError(ctx, sandbox, fmt.Sprintf("Pod creation failed: %v", err))
		}
		sandbox.Status.PodName = desiredPod.Name
		if err := r.Status().Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}
		// Requeue to check pod status
		return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Step 6: Check if pod is Ready
	if isPodReady(existingPod) {
		now := metav1.Now()
		sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseRunning
		sandbox.Status.PodName = existingPod.Name
		sandbox.Status.StartedAt = &now
		sandbox.Status.LastActivityTimestamp = &now
		sandbox.Status.Message = ""
		if err := r.Status().Update(ctx, sandbox); err != nil {
			return ctrl.Result{}, err
		}

		// Log startup duration
		startupDuration := now.Time.Sub(sandbox.CreationTimestamp.Time)
		logger.Info("sandbox is running",
			"sandbox", sandbox.Name,
			"pod", existingPod.Name,
			"startupDurationMs", startupDuration.Milliseconds(),
			"startupDuration", startupDuration.String(),
		)
		r.Recorder.Eventf(sandbox, corev1.EventTypeNormal, "Running", "Sandbox is ready (startup: %s)", startupDuration.Round(time.Millisecond))
		return ctrl.Result{RequeueAfter: DefaultRequeueDelay}, nil
	}

	// Pod exists but not ready yet — check for failures
	if existingPod.Status.Phase == corev1.PodFailed {
		reason := getPodFailureReason(existingPod)
		return r.transitionToError(ctx, sandbox, fmt.Sprintf("Pod failed: %s", reason))
	}

	// Still waiting for pod to be ready
	return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
}

// reconcileRunning checks timeouts and pod health.
func (r *SandboxReconciler) reconcileRunning(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (ctrl.Result, error) {
	// Check if pod still exists
	if sandbox.Status.PodName == "" {
		return r.transitionToError(ctx, sandbox, "Pod name not set in Running state")
	}

	pod := &corev1.Pod{}
	err := r.Get(ctx, client.ObjectKey{Namespace: sandbox.Namespace, Name: sandbox.Status.PodName}, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			// Pod disappeared — infrastructure failure
			return r.handleInfrastructureFailure(ctx, sandbox, "Pod disappeared unexpectedly")
		}
		return ctrl.Result{}, err
	}

	// Check pod failures
	if pod.Status.Phase == corev1.PodFailed {
		reason := getPodFailureReason(pod)
		return r.handleInfrastructureFailure(ctx, sandbox, reason)
	}

	// Check idle timeout
	if sandbox.Spec.IdleTimeout != nil && sandbox.Spec.IdleTimeout.Duration > 0 {
		if sandbox.Status.LastActivityTimestamp != nil {
			idle := time.Since(sandbox.Status.LastActivityTimestamp.Time)
			if idle >= sandbox.Spec.IdleTimeout.Duration {
				r.Recorder.Event(sandbox, corev1.EventTypeNormal, "IdleTimeout", fmt.Sprintf("Idle for %s", idle))
				return r.stopSandbox(ctx, sandbox, "Idle timeout exceeded")
			}
		}
	}

	// Check max runtime
	if sandbox.Spec.Timeout != nil && sandbox.Spec.Timeout.Duration > 0 {
		if sandbox.Status.StartedAt != nil {
			runtime := time.Since(sandbox.Status.StartedAt.Time)
			if runtime >= sandbox.Spec.Timeout.Duration {
				r.Recorder.Event(sandbox, corev1.EventTypeNormal, "MaxRuntimeReached", fmt.Sprintf("Running for %s", runtime))
				return r.stopSandbox(ctx, sandbox, "Max runtime exceeded")
			}
		}
	}

	// Calculate next requeue
	requeueAfter := DefaultRequeueDelay
	if sandbox.Spec.IdleTimeout != nil && sandbox.Spec.IdleTimeout.Duration > 0 && sandbox.Status.LastActivityTimestamp != nil {
		remaining := sandbox.Spec.IdleTimeout.Duration - time.Since(sandbox.Status.LastActivityTimestamp.Time)
		if remaining > 0 && remaining < requeueAfter {
			requeueAfter = remaining + time.Second
		}
	}
	if sandbox.Spec.Timeout != nil && sandbox.Spec.Timeout.Duration > 0 && sandbox.Status.StartedAt != nil {
		remaining := sandbox.Spec.Timeout.Duration - time.Since(sandbox.Status.StartedAt.Time)
		if remaining > 0 && remaining < requeueAfter {
			requeueAfter = remaining + time.Second
		}
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// reconcileStopped handles stopped sandboxes (autoResume check).
func (r *SandboxReconciler) reconcileStopped(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (ctrl.Result, error) {
	// Nothing to do — wait for user action (resume or delete)
	return ctrl.Result{}, nil
}

// reconcileError handles error state with restart backoff.
func (r *SandboxReconciler) reconcileError(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (ctrl.Result, error) {
	if sandbox.Status.RestartCount >= MaxRestartCount {
		return ctrl.Result{}, nil // Stay in error permanently
	}

	backoff := calculateBackoffDelay(sandbox.Status.RestartCount)
	return ctrl.Result{RequeueAfter: backoff}, nil
}

// reconcileDelete cleans up all child resources.
func (r *SandboxReconciler) reconcileDelete(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(sandbox, FinalizerName) {
		return ctrl.Result{}, nil
	}

	logger.Info("cleaning up sandbox resources", "sandbox", sandbox.Name)

	// Delete Pod
	if sandbox.Status.PodName != "" {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: sandbox.Status.PodName, Namespace: sandbox.Namespace}}
		if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// Delete PVC
	if sandbox.Status.PVCName != "" {
		pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: sandbox.Status.PVCName, Namespace: sandbox.Namespace}}
		if err := r.Delete(ctx, pvc); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	// Delete NetworkPolicy
	np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: sandbox.Name + "-netpol", Namespace: sandbox.Namespace}}
	if err := r.Delete(ctx, np); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(sandbox, FinalizerName)
	if err := r.Update(ctx, sandbox); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(sandbox, corev1.EventTypeNormal, "Deleted", "Sandbox deleted")
	logger.Info("sandbox cleanup complete", "sandbox", sandbox.Name)
	return ctrl.Result{}, nil
}

// stopSandbox deletes the pod but preserves the PVC.
func (r *SandboxReconciler) stopSandbox(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, reason string) (ctrl.Result, error) {
	if sandbox.Status.PodName != "" {
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: sandbox.Status.PodName, Namespace: sandbox.Namespace}}
		if err := r.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseStopped
	sandbox.Status.PodName = ""
	sandbox.Status.StartedAt = nil
	sandbox.Status.Message = reason
	if err := r.Status().Update(ctx, sandbox); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(sandbox, corev1.EventTypeNormal, "Stopped", reason)
	return ctrl.Result{}, nil
}

// handleInfrastructureFailure attempts auto-restart with backoff.
func (r *SandboxReconciler) handleInfrastructureFailure(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, reason string) (ctrl.Result, error) {
	sandbox.Status.RestartCount++

	if sandbox.Status.RestartCount > MaxRestartCount {
		return r.transitionToError(ctx, sandbox, fmt.Sprintf("Restart limit exceeded (%d attempts): %s", MaxRestartCount, reason))
	}

	r.Recorder.Eventf(sandbox, corev1.EventTypeWarning, "AutoRestarted", "Pod failed (%s), restarting (attempt %d/%d)", reason, sandbox.Status.RestartCount, MaxRestartCount)

	// Go back to Creating to trigger pod recreation
	sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseCreating
	sandbox.Status.PodName = ""
	if err := r.Status().Update(ctx, sandbox); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

// transitionToError moves sandbox to Error phase.
func (r *SandboxReconciler) transitionToError(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, message string) (ctrl.Result, error) {
	sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseError
	sandbox.Status.Message = message
	if err := r.Status().Update(ctx, sandbox); err != nil {
		return ctrl.Result{}, err
	}
	r.Recorder.Event(sandbox, corev1.EventTypeWarning, "Error", message)
	return ctrl.Result{}, nil
}

// isPodReady checks if a pod has the Ready condition set to True.
func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// getPodFailureReason extracts a human-readable failure reason from a pod.
func getPodFailureReason(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			return fmt.Sprintf("%s: %s", cs.State.Terminated.Reason, cs.State.Terminated.Message)
		}
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return "CrashLoopBackOff"
		}
	}
	if pod.Status.Reason != "" {
		return pod.Status.Reason
	}
	return "Unknown failure"
}

// SetupWithManager registers the controller with the manager.
func (r *SandboxReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&agenttierv1alpha1.Sandbox{}).
		Owns(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&networkingv1.NetworkPolicy{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.MaxConcurrency,
		}).
		Complete(r)
}
