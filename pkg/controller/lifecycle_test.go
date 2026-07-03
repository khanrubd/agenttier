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

func lifecycleReconciler(t *testing.T, objs ...client.Object) (*SandboxReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()
	return &SandboxReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}, c
}

func TestStopSandbox_DeletesPodAndPreservesPVC(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-stop", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: "sb-stop-pod",
			PVCName: "sb-stop-pvc",
		},
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "sb-stop-pod", Namespace: "default"}}
	r, c := lifecycleReconciler(t, sandbox, pod)

	if err := r.StopSandbox(context.Background(), sandbox, "manual stop"); err != nil {
		t.Fatalf("StopSandbox: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-stop", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped {
		t.Errorf("phase = %s, want Stopped", got.Status.Phase)
	}
	if got.Status.PodName != "" {
		t.Errorf("podName = %q, want empty after stop", got.Status.PodName)
	}
	if got.Status.Message != "manual stop" {
		t.Errorf("message = %q, want %q", got.Status.Message, "manual stop")
	}

	// Pod deleted, PVC preserved (StopSandbox never touches PVCName/PVC objects).
	gotPod := &corev1.Pod{}
	err := c.Get(context.Background(), client.ObjectKey{Name: "sb-stop-pod", Namespace: "default"}, gotPod)
	if err == nil {
		t.Error("expected pod to be deleted")
	}
	if got.Status.PVCName != "sb-stop-pvc" {
		t.Errorf("PVCName = %q, want preserved sb-stop-pvc", got.Status.PVCName)
	}
}

func TestStopSandbox_RejectsNonRunningPhase(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-notrunning", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseStopped},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	err := r.StopSandbox(context.Background(), sandbox, "reason")
	if err == nil {
		t.Fatal("expected error stopping a non-Running sandbox")
	}
}

func TestResumeSandbox_FromStoppedWithExistingPVC(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-resume", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseStopped,
			PVCName: "sb-resume-pvc",
		},
	}
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "sb-resume-pvc", Namespace: "default"}}
	r, c := lifecycleReconciler(t, sandbox, pvc)

	if err := r.ResumeSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("ResumeSandbox: %v", err)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-resume", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseCreating {
		t.Errorf("phase = %s, want Creating", got.Status.Phase)
	}
	if got.Status.StartedAt == nil {
		t.Error("expected StartedAt to be set on resume")
	}
}

func TestResumeSandbox_FromErrorPhaseAllowed(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-resume-err", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseError},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	if err := r.ResumeSandbox(context.Background(), sandbox); err != nil {
		t.Fatalf("ResumeSandbox from Error: %v", err)
	}
}

func TestResumeSandbox_RejectsRunningPhase(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-resume-running", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseRunning},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	if err := r.ResumeSandbox(context.Background(), sandbox); err == nil {
		t.Fatal("expected error resuming an already-Running sandbox")
	}
}

func TestResumeSandbox_MissingPVCFails(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-resume-nopvc", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseStopped,
			PVCName: "does-not-exist",
		},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	err := r.ResumeSandbox(context.Background(), sandbox)
	if err == nil {
		t.Fatal("expected error resuming when the PVC is gone")
	}
}

func TestValidateStateTransition(t *testing.T) {
	tests := []struct {
		name    string
		phase   agenttierv1alpha1.SandboxPhase
		action  string
		wantErr bool
	}{
		{"creating to running", agenttierv1alpha1.SandboxPhaseCreating, "running", false},
		{"creating to error", agenttierv1alpha1.SandboxPhaseCreating, "error", false},
		{"creating to stop invalid", agenttierv1alpha1.SandboxPhaseCreating, "stop", true},
		{"running to stop", agenttierv1alpha1.SandboxPhaseRunning, "stop", false},
		{"running to resume invalid", agenttierv1alpha1.SandboxPhaseRunning, "resume", true},
		{"stopped to resume", agenttierv1alpha1.SandboxPhaseStopped, "resume", false},
		{"stopped to stop invalid", agenttierv1alpha1.SandboxPhaseStopped, "stop", true},
		{"error to delete", agenttierv1alpha1.SandboxPhaseError, "delete", false},
		{"unknown phase", agenttierv1alpha1.SandboxPhase("Bogus"), "delete", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStateTransition(tt.phase, tt.action)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %s -> %s, got nil", tt.phase, tt.action)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error for %s -> %s, got: %v", tt.phase, tt.action, err)
			}
		})
	}
}

func TestExecuteHook_EmptyScriptIsNoop(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-hook", Namespace: "default"}}
	r, _ := lifecycleReconciler(t, sandbox)

	if err := r.executeHook(context.Background(), sandbox, "onStart", ""); err != nil {
		t.Errorf("executeHook with empty script: %v", err)
	}
}

func TestExecuteHook_NonEmptyScriptPlaceholder(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-hook2", Namespace: "default"}}
	r, _ := lifecycleReconciler(t, sandbox)

	// executeHook is currently a logging placeholder — it must not error even
	// though it doesn't actually exec into the pod yet.
	if err := r.executeHook(context.Background(), sandbox, "onStop", "echo hi"); err != nil {
		t.Errorf("executeHook with script: %v", err)
	}
}

func TestIsInfrastructureFailure_CrashLoopBackOff(t *testing.T) {
	pod := &corev1.Pod{
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
				},
			}},
		},
	}
	if !isInfrastructureFailure(pod) {
		t.Error("CrashLoopBackOff should be classified as an infrastructure failure")
	}
}

func TestIsInfrastructureFailure_PodLevelReasons(t *testing.T) {
	for _, reason := range []string{"Evicted", "NodeLost", "UnexpectedAdmissionError"} {
		pod := &corev1.Pod{Status: corev1.PodStatus{Reason: reason}}
		if !isInfrastructureFailure(pod) {
			t.Errorf("pod-level reason %q should be classified as infrastructure failure", reason)
		}
	}
}
