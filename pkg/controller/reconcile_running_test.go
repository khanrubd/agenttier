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
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func TestReconcileRunning_NoPodNameTransitionsToError(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-nopod", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseRunning},
	}
	r, c := lifecycleReconciler(t, sandbox)

	if _, err := r.reconcileRunning(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-run-nopod", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseError {
		t.Errorf("phase = %s, want Error when PodName is unset in Running phase", got.Status.Phase)
	}
}

func TestReconcileRunning_UserRequestedStopAnnotation(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "sb-run-stopreq",
			Namespace:   "default",
			Annotations: map[string]string{"agenttier.io/action": "stop"},
		},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: "sb-run-stopreq-pod",
			PVCName: "sb-run-stopreq-pvc",
		},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "sb-run-stopreq-pod", Namespace: "default"}}
	r, c := lifecycleReconciler(t, sandbox, pod)

	if _, err := r.reconcileRunning(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-run-stopreq", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped {
		t.Errorf("phase = %s, want Stopped after honoring the stop annotation", got.Status.Phase)
	}
	if _, ok := got.Annotations["agenttier.io/action"]; ok {
		t.Error("expected the stop annotation to be cleared to prevent replay loops")
	}
}

func TestReconcileRunning_PodDisappearedIsInfrastructureFailure(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-vanished", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: "sb-run-vanished-pod", // never created in the fake client
		},
	}
	r, c := lifecycleReconciler(t, sandbox)

	if _, err := r.reconcileRunning(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-run-vanished", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	// isInfrastructureFailure(nil) == true, so a vanished pod goes through
	// handleInfrastructureFailure's restart path, not straight to terminal Error.
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseError {
		t.Errorf("phase = %s, want Error (restart-eligible) after pod disappeared", got.Status.Phase)
	}
	if got.Status.RestartCount != 1 {
		t.Errorf("restartCount = %d, want 1 after first infra failure", got.Status.RestartCount)
	}
}

func TestReconcileRunning_PodFailedRoutesToInfrastructureFailureHandler(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-podfailed", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: "sb-run-podfailed-pod",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-podfailed-pod", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase: corev1.PodFailed,
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
				},
			}},
		},
	}
	r, c := lifecycleReconciler(t, sandbox, pod)

	if _, err := r.reconcileRunning(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-run-podfailed", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseError {
		t.Errorf("phase = %s, want Error after OOMKilled pod failure", got.Status.Phase)
	}
	if got.Status.RestartCount != 1 {
		t.Errorf("restartCount = %d, want 1", got.Status.RestartCount)
	}
}

func TestReconcileRunning_IdleTimeoutStops(t *testing.T) {
	past := metav1.NewTime(time.Now().Add(-2 * time.Hour))
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-idle", Namespace: "default"},
		Spec: agenttierv1alpha1.SandboxSpec{
			IdleTimeout: &metav1.Duration{Duration: 1 * time.Hour},
		},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:                 agenttierv1alpha1.SandboxPhaseRunning,
			PodName:               "sb-run-idle-pod",
			LastActivityTimestamp: &past,
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-idle-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, c := lifecycleReconciler(t, sandbox, pod)

	if _, err := r.reconcileRunning(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-run-idle", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped {
		t.Errorf("phase = %s, want Stopped after idle timeout exceeded", got.Status.Phase)
	}
}

func TestReconcileRunning_MaxRuntimeExceededStops(t *testing.T) {
	past := metav1.NewTime(time.Now().Add(-10 * time.Hour))
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-maxtime", Namespace: "default"},
		Spec: agenttierv1alpha1.SandboxSpec{
			Timeout: &metav1.Duration{Duration: 8 * time.Hour},
		},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:     agenttierv1alpha1.SandboxPhaseRunning,
			PodName:   "sb-run-maxtime-pod",
			StartedAt: &past,
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-maxtime-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, c := lifecycleReconciler(t, sandbox, pod)

	if _, err := r.reconcileRunning(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-run-maxtime", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped {
		t.Errorf("phase = %s, want Stopped after max runtime exceeded", got.Status.Phase)
	}
}

func TestReconcileRunning_RestartCountResetsAfterStableUptime(t *testing.T) {
	past := metav1.NewTime(time.Now().Add(-10 * time.Minute)) // beyond RestartCountResetWindow (5m)
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-reset", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:        agenttierv1alpha1.SandboxPhaseRunning,
			PodName:      "sb-run-reset-pod",
			StartedAt:    &past,
			RestartCount: 3,
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-reset-pod", Namespace: "default"},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	r, c := lifecycleReconciler(t, sandbox, pod)

	result, err := r.reconcileRunning(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}
	if result != (ctrl.Result{Requeue: true}) {
		t.Errorf("expected immediate Requeue after resetting restart count, got %+v", result)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-run-reset", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.RestartCount != 0 {
		t.Errorf("restartCount = %d, want reset to 0 after stable uptime beyond the reset window", got.Status.RestartCount)
	}
}

func TestReconcileRunning_HealthyPodRequeuesAtDefaultDelay(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-healthy", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: "sb-run-healthy-pod",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-run-healthy-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	r, _ := lifecycleReconciler(t, sandbox, pod)

	result, err := r.reconcileRunning(context.Background(), sandbox)
	if err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}
	if result.RequeueAfter != DefaultRequeueDelay {
		t.Errorf("RequeueAfter = %v, want DefaultRequeueDelay for a healthy pod with no timeouts", result.RequeueAfter)
	}
}
