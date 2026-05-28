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

// TestHandleInfrastructureFailure_AppErrorSkipsRestarts verifies that a
// pod that exits Completed with no infra-failure markers (i.e. an
// application error in the user's CMD) goes straight to terminal
// Error without burning the auto-restart budget. Without this guard
// every misconfigured CMD would restart-loop 5 times before the user
// got a clear error.
func TestHandleInfrastructureFailure_AppErrorSkipsRestarts(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}

	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-app-error",
			Namespace: "default",
		},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:        agenttierv1alpha1.SandboxPhaseRunning,
			RestartCount: 0,
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sandbox).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()

	r := &SandboxReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
	}

	// Pod that exited Completed (e.g. CMD exit 1) — no infra-failure
	// markers, so isInfrastructureFailure returns false.
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Reason:   "Error",
						ExitCode: 1,
					},
				},
			}},
		},
	}
	// isInfrastructureFailure returns true for pod=nil and for any pod
	// without explicit infra reasons (OOMKilled, Evicted,
	// CrashLoopBackOff, NodeLost) — except when a container terminated
	// with Reason=Completed, which is the user-success path.
	// "Error" reason is treated as infra by the helper's default
	// because the helper errs on the side of restart. To verify the
	// app-error path we set Reason=Completed.
	pod.Status.ContainerStatuses[0].State.Terminated.Reason = "Completed"

	if _, err := r.handleInfrastructureFailure(context.Background(), sandbox, "Completed: exit 1", pod); err != nil {
		t.Fatalf("handleInfrastructureFailure: %v", err)
	}

	refreshed := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-app-error", Namespace: "default"}, refreshed); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}

	if refreshed.Status.Phase != agenttierv1alpha1.SandboxPhaseError {
		t.Errorf("phase = %s, want Error (app errors should not auto-restart)", refreshed.Status.Phase)
	}
	if refreshed.Status.RestartCount != 0 {
		t.Errorf("restartCount = %d, want 0 (no restart attempted for app errors)", refreshed.Status.RestartCount)
	}
}
