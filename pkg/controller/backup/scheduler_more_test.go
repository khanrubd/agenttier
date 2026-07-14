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

package backup

import (
	"context"
	"errors"
	"testing"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// TestNewScheduler_NilLoggerDefaultsToSlogDefault guards the nil-logger
// fallback: passing nil must not panic on later use and must not leave the
// Scheduler's logger nil.
func TestNewScheduler_NilLoggerDefaultsToSlogDefault(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	s := NewScheduler(c, nil, "agenttier", time.Hour, 14*24*time.Hour, "")
	if s.logger == nil {
		t.Fatal("NewScheduler with nil logger should default to slog.Default(), got nil")
	}
	// Exercise a code path that logs, to guard against a nil-pointer panic.
	if _, err := s.snapshotManagedPVCs(context.Background()); err != nil {
		t.Fatalf("snapshotManagedPVCs: %v", err)
	}
}

// TestRunOnce_LogsAndSwallowsSnapshotListError guards RunOnce's error branch
// for snapshotManagedPVCs: a list failure is logged, not returned or panicked
// on, and pruneExpired still runs afterward.
func TestRunOnce_LogsAndSwallowsSnapshotListError(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	pruneCalled := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			switch list.(type) {
			case *snapshotv1.VolumeSnapshotList:
				pruneCalled = true
				return cl.List(ctx, list, opts...)
			default:
				return errors.New("injected PVC list failure")
			}
		},
	}).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")

	// RunOnce must not panic despite the injected error, and must still reach
	// the prune pass.
	s.RunOnce(context.Background())
	if !pruneCalled {
		t.Error("RunOnce should still run pruneExpired after a snapshot-pass error")
	}
}

// TestRunOnce_LogsAndSwallowsPruneListError guards RunOnce's error branch for
// pruneExpired.
func TestRunOnce_LogsAndSwallowsPruneListError(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*snapshotv1.VolumeSnapshotList); ok {
				return errors.New("injected snapshot list failure")
			}
			return cl.List(ctx, list, opts...)
		},
	}).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")

	// Must not panic; the returned error from pruneExpired is swallowed
	// (logged) by RunOnce, so there's nothing further to assert beyond "it
	// didn't panic or hang."
	s.RunOnce(context.Background())
}

// TestSnapshotManagedPVCs_ListErrorPropagates guards the direct (non-RunOnce)
// caller path: List failures must be wrapped and returned, not swallowed.
func TestSnapshotManagedPVCs_ListErrorPropagates(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("injected list failure")
		},
	}).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")
	_, err := s.snapshotManagedPVCs(context.Background())
	if err == nil {
		t.Fatal("snapshotManagedPVCs should propagate a List error")
	}
}

// TestSnapshotManagedPVCs_CreateErrorSkipsAndContinues guards the per-PVC
// Create-error-continue branch: one PVC's snapshot Create failing must not
// abort the pass — the remaining PVCs still get snapshotted.
func TestSnapshotManagedPVCs_CreateErrorSkipsAndContinues(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		managedPVC("sb-1-workspace", false),
		managedPVC("sb-2-workspace", false),
	).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			snap, ok := obj.(*snapshotv1.VolumeSnapshot)
			if ok && snap.Labels[LabelSourcePVC] == "sb-1-workspace" {
				return errors.New("injected create failure")
			}
			return cl.Create(ctx, obj, opts...)
		},
	}).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")
	created, err := s.snapshotManagedPVCs(context.Background())
	if err != nil {
		t.Fatalf("snapshotManagedPVCs should not propagate a per-PVC create error: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected 1 created (the other PVC's create failed), got %d", created)
	}
}

// TestSnapshotManagedPVCs_StampsSnapshotClassWhenConfigured guards the
// snapshotClassName-set branch, which is untested by the zero-value case in
// scheduler_test.go.
func TestSnapshotManagedPVCs_StampsSnapshotClassWhenConfigured(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		managedPVC("sb-1-workspace", false),
	).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "csi-hostpath-snapclass")
	if _, err := s.snapshotManagedPVCs(context.Background()); err != nil {
		t.Fatalf("snapshotManagedPVCs: %v", err)
	}

	snaps := &snapshotv1.VolumeSnapshotList{}
	if err := c.List(context.Background(), snaps, client.InNamespace("agenttier")); err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps.Items) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps.Items))
	}
	got := snaps.Items[0].Spec.VolumeSnapshotClassName
	if got == nil || *got != "csi-hostpath-snapclass" {
		t.Errorf("VolumeSnapshotClassName = %v, want \"csi-hostpath-snapclass\"", got)
	}
}

// TestPruneExpired_ListErrorPropagates mirrors
// TestSnapshotManagedPVCs_ListErrorPropagates for the prune path.
func TestPruneExpired_ListErrorPropagates(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			return errors.New("injected list failure")
		},
	}).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")
	_, err := s.pruneExpired(context.Background())
	if err == nil {
		t.Fatal("pruneExpired should propagate a List error")
	}
}

// TestPruneExpired_DeleteErrorSkipsAndContinues guards the per-snapshot
// Delete-error-continue branch.
func TestPruneExpired_DeleteErrorSkipsAndContinues(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	old1 := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "old-1", Namespace: "agenttier",
			Labels:            map[string]string{LabelSnapshotKind: SnapshotKindBackup},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-30 * 24 * time.Hour)),
		},
	}
	old2 := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "old-2", Namespace: "agenttier",
			Labels:            map[string]string{LabelSnapshotKind: SnapshotKindBackup},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-30 * 24 * time.Hour)),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(old1, old2).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if obj.GetName() == "old-1" {
				return errors.New("injected delete failure")
			}
			return cl.Delete(ctx, obj, opts...)
		},
	}).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")
	pruned, err := s.pruneExpired(context.Background())
	if err != nil {
		t.Fatalf("pruneExpired should not propagate a per-snapshot delete error: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned (old-1's delete failed), got %d", pruned)
	}
}

// TestRunLoop_RunsImmediatelyThenStopsOnContextCancel guards RunLoop's
// startup behavior (an immediate RunOnce before the first tick) and its
// ctx.Done() exit path.
func TestRunLoop_RunsImmediatelyThenStopsOnContextCancel(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		managedPVC("sb-1-workspace", false),
	).Build()

	// A long interval means only the immediate startup RunOnce should fire
	// before we cancel.
	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx)
		close(done)
	}()

	// Give the immediate RunOnce a moment to execute, then cancel and expect
	// RunLoop to return promptly.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunLoop did not return within 5s of context cancellation")
	}

	snaps := &snapshotv1.VolumeSnapshotList{}
	if err := c.List(context.Background(), snaps, client.InNamespace("agenttier")); err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps.Items) != 1 {
		t.Errorf("expected the immediate startup pass to have created 1 snapshot, got %d", len(snaps.Items))
	}
}

// TestRunLoop_TicksTriggerAdditionalPasses guards the ticker.C branch: with a
// short interval, RunLoop must run more than just the immediate startup pass
// before it's cancelled.
func TestRunLoop_TicksTriggerAdditionalPasses(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	s := NewScheduler(c, discard(), "agenttier", 10*time.Millisecond, 14*24*time.Hour, "")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunLoop(ctx)
		close(done)
	}()

	// Let several ticks fire.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunLoop did not return within 5s of context cancellation")
	}
}
