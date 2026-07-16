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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func TestIsPodReady(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want bool
	}{
		{
			name: "running and ready condition true",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			}},
			want: true,
		},
		{
			name: "running but ready condition false",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}},
			}},
			want: false,
		},
		{
			name: "not running",
			pod:  &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}},
			want: false,
		},
		{
			name: "running with no conditions",
			pod:  &corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPodReady(tt.pod); got != tt.want {
				t.Errorf("isPodReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetPodFailureReason(t *testing.T) {
	tests := []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{
			name: "terminated container reason and message",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", Message: "memory limit exceeded"},
					},
				}},
			}},
			want: "OOMKilled: memory limit exceeded",
		},
		{
			name: "waiting crashloopbackoff",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				}},
			}},
			want: "CrashLoopBackOff",
		},
		{
			name: "pod-level reason fallback",
			pod:  &corev1.Pod{Status: corev1.PodStatus{Reason: "Evicted"}},
			want: "Evicted",
		},
		{
			name: "no info available",
			pod:  &corev1.Pod{},
			want: "Unknown failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getPodFailureReason(tt.pod); got != tt.want {
				t.Errorf("getPodFailureReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReconcileError_PermanentAtRestartLimit(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-err-perm", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:        agenttierv1alpha1.SandboxPhaseError,
			RestartCount: MaxRestartCount,
		},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	result, err := r.reconcileError(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("reconcileError: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected no requeue once restart limit is exhausted, got %+v", result)
	}
}

func TestReconcileError_NonRestartEligibleWaits(t *testing.T) {
	// Message doesn't contain "before restart attempt" — e.g. a template
	// resolution failure — so reconcileError must not auto-rebuild.
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-err-noauto", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:        agenttierv1alpha1.SandboxPhaseError,
			RestartCount: 1,
			Message:      "Template resolution failed: not found",
		},
	}
	r, c := lifecycleReconciler(t, sandbox)

	result, err := r.reconcileError(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("reconcileError: %v", err)
	}
	if result.RequeueAfter != DefaultRequeueDelay {
		t.Errorf("RequeueAfter = %v, want DefaultRequeueDelay for a non-restart-eligible error", result.RequeueAfter)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-err-noauto", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseError {
		t.Errorf("phase = %s, want to stay in Error (not auto-rebuilt)", got.Status.Phase)
	}
}

func TestReconcileError_BackoffElapsedRebuildsPod(t *testing.T) {
	// RestartCount=0 → calculateBackoffDelay(0) = 10s. StartedAt far enough
	// in the past that the backoff window has elapsed.
	past := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-err-rebuild", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:        agenttierv1alpha1.SandboxPhaseError,
			RestartCount: 0,
			Message:      "Pod failed (OOMKilled), waiting 10s before restart attempt 1/5",
			StartedAt:    &past,
		},
	}
	r, c := lifecycleReconciler(t, sandbox)

	result, err := r.reconcileError(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("reconcileError: %v", err)
	}
	if result != (ctrl.Result{Requeue: true}) {
		t.Errorf("expected immediate Requeue once backoff elapsed, got %+v", result)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-err-rebuild", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseCreating {
		t.Errorf("phase = %s, want Creating after backoff window elapses", got.Status.Phase)
	}
	if got.Status.Message != "" {
		t.Errorf("message = %q, want cleared on rebuild", got.Status.Message)
	}
}

func TestReconcileError_BackoffNotYetElapsedWaits(t *testing.T) {
	now := metav1.Now()
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-err-waiting", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:        agenttierv1alpha1.SandboxPhaseError,
			RestartCount: 0,
			Message:      "Pod failed (OOMKilled), waiting 10s before restart attempt 1/5",
			StartedAt:    &now,
		},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	result, err := r.reconcileError(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("reconcileError: %v", err)
	}
	if result.RequeueAfter <= 0 {
		t.Errorf("expected a positive RequeueAfter while backoff window is still open, got %+v", result)
	}
	if result != (ctrl.Result{RequeueAfter: result.RequeueAfter}) {
		t.Error("expected RequeueAfter (delayed), not immediate Requeue, while backoff is pending")
	}
}

func TestReconcileDelete_CleansUpChildResourcesAndRemovesFinalizer(t *testing.T) {
	now := metav1.Now()
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sb-delete",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerName},
		},
		Status: agenttierv1alpha1.SandboxStatus{
			PodName: "sb-delete-pod",
			PVCName: "sb-delete-pvc",
		},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "sb-delete-pod", Namespace: "default"}}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "sb-delete-pvc", Namespace: "default"}}
	r, c := lifecycleReconciler(t, sandbox, pod, pvc)

	if _, err := r.reconcileDelete(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileDelete: %v", err)
	}

	gotPod := &corev1.Pod{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-delete-pod", Namespace: "default"}, gotPod); err == nil {
		t.Error("expected pod to be deleted")
	}
	gotPVC := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-delete-pvc", Namespace: "default"}, gotPVC); err == nil {
		t.Error("expected PVC to be deleted")
	}

	// Sandbox itself: finalizer removed (fake client with no finalizers left
	// and DeletionTimestamp set would normally be garbage collected by a
	// real API server, but the fake client still lets us read it back).
	got := &agenttierv1alpha1.Sandbox{}
	err := c.Get(context.Background(), client.ObjectKey{Name: "sb-delete", Namespace: "default"}, got)
	if err == nil && len(got.Finalizers) != 0 {
		t.Errorf("expected finalizer removed, got: %v", got.Finalizers)
	}
}

func TestReconcileDelete_NoFinalizerIsNoop(t *testing.T) {
	now := metav1.Now()
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sb-delete-nofin",
			Namespace:         "default",
			DeletionTimestamp: &now,
			// No finalizers set.
		},
	}
	// reconcileDelete returns immediately (no client calls) when the
	// finalizer is absent, so the sandbox doesn't need to be seeded into the
	// fake client — and a real apiserver (and the fake client, matching that
	// behavior) would refuse an object with deletionTimestamp set but no
	// finalizers anyway.
	r, _ := lifecycleReconciler(t)

	result, err := r.reconcileDelete(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("reconcileDelete: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected empty result when there's no finalizer to process, got %+v", result)
	}
}

func TestInvalidTransitionError_MessageFormat(t *testing.T) {
	err := ValidateAction(agenttierv1alpha1.SandboxPhaseStopped, ActionStop)
	if err == nil {
		t.Fatal("expected an error for Stopped -> stop")
	}
	if !strings.Contains(err.Error(), "cannot perform action") {
		t.Errorf("error message = %q, want it to describe the disallowed action", err.Error())
	}

	// Creating has zero allowed actions — exercises the "no actions allowed" branch.
	errNoActions := ValidateAction(agenttierv1alpha1.SandboxPhaseCreating, ActionStop)
	if errNoActions == nil {
		t.Fatal("expected an error for Creating -> stop")
	}
	if !strings.Contains(errNoActions.Error(), "no actions allowed in this phase") {
		t.Errorf("error message = %q, want the zero-actions-allowed branch text", errNoActions.Error())
	}
}
