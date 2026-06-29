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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TestReconcileRunning_PodSucceededTransitionsToStopped is the regression
// guard for the bug where a pod that exited 0 (phase Succeeded under
// RestartPolicy:Never) left the Sandbox reporting Running forever. The
// reconciler must move it to a terminal Stopped phase.
func TestReconcileRunning_PodSucceededTransitionsToStopped(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}

	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-done", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: "sb-done-pod",
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-done-pod", Namespace: "default"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sandbox, pod).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()

	r := &SandboxReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	if _, err := r.reconcileRunning(context.Background(), sandbox); err != nil {
		t.Fatalf("reconcileRunning: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-done", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase == agenttierv1alpha1.SandboxPhaseRunning {
		t.Fatal("REGRESSION: sandbox still Running after pod Succeeded")
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped {
		t.Errorf("phase = %s, want Stopped after pod exited 0", got.Status.Phase)
	}
}

// TestIsInfrastructureFailure_ErrorIsAppFailure guards the fix that a non-zero
// app exit (terminated reason "Error") is classified as an application
// failure, not infrastructure — so it goes terminal instead of burning the
// auto-restart budget.
func TestIsInfrastructureFailure_ErrorIsAppFailure(t *testing.T) {
	mkPod := func(reason string) *corev1.Pod {
		return &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{{
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: reason},
					},
				}},
			},
		}
	}

	if isInfrastructureFailure(mkPod("Error")) {
		t.Error("reason=Error (non-zero app exit) should NOT be classified as infrastructure failure")
	}
	if isInfrastructureFailure(mkPod("Completed")) {
		t.Error("reason=Completed should not be an infrastructure failure")
	}
	// Genuine infra reasons still classify as infra.
	if !isInfrastructureFailure(mkPod("OOMKilled")) {
		t.Error("OOMKilled should be an infrastructure failure")
	}
	if !isInfrastructureFailure(nil) {
		t.Error("a vanished pod (nil) should be an infrastructure failure")
	}
}
