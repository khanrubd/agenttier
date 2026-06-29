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

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func snapStopReconciler(t *testing.T, sandbox *agenttierv1alpha1.Sandbox) (*SandboxReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme, agenttierv1alpha1.AddToScheme, snapshotv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sandbox).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()
	return &SandboxReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}, c
}

func runningSandboxWithStorage(name string, snapshotOnStop bool) *agenttierv1alpha1.Sandbox {
	return &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: agenttierv1alpha1.SandboxSpec{
			Storage: &agenttierv1alpha1.StorageSpec{SnapshotOnStop: snapshotOnStop},
		},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PodName: name + "-pod",
			PVCName: name + "-pvc",
		},
	}
}

// TestStopSandbox_SnapshotOnStop_Creates is the regression guard that the
// previously-inert StorageSpec.SnapshotOnStop field now produces a
// VolumeSnapshot of the workspace PVC when a sandbox is stopped.
func TestStopSandbox_SnapshotOnStop_Creates(t *testing.T) {
	sandbox := runningSandboxWithStorage("sb-snap", true)
	r, c := snapStopReconciler(t, sandbox)

	if _, err := r.stopSandbox(context.Background(), sandbox, "test stop"); err != nil {
		t.Fatalf("stopSandbox: %v", err)
	}

	snapList := &snapshotv1.VolumeSnapshotList{}
	if err := c.List(context.Background(), snapList, client.InNamespace("default")); err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapList.Items) != 1 {
		t.Fatalf("expected 1 VolumeSnapshot on stop, got %d", len(snapList.Items))
	}
	src := snapList.Items[0].Spec.Source.PersistentVolumeClaimName
	if src == nil || *src != "sb-snap-pvc" {
		t.Errorf("snapshot source PVC = %v, want sb-snap-pvc", src)
	}

	// Sandbox still transitions to Stopped.
	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-snap", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped {
		t.Errorf("phase = %s, want Stopped", got.Status.Phase)
	}
}

// TestStopSandbox_NoSnapshotWhenDisabled confirms the default (false) path
// takes no snapshot.
func TestStopSandbox_NoSnapshotWhenDisabled(t *testing.T) {
	sandbox := runningSandboxWithStorage("sb-nosnap", false)
	r, c := snapStopReconciler(t, sandbox)

	if _, err := r.stopSandbox(context.Background(), sandbox, "test stop"); err != nil {
		t.Fatalf("stopSandbox: %v", err)
	}

	snapList := &snapshotv1.VolumeSnapshotList{}
	if err := c.List(context.Background(), snapList, client.InNamespace("default")); err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapList.Items) != 0 {
		t.Errorf("expected no snapshot when SnapshotOnStop=false, got %d", len(snapList.Items))
	}
}
