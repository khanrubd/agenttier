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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TestDispatch_NotFoundIsSwallowed covers the "sandbox already gone" path —
// a stale reconcile request for an object that's since been deleted must
// not error.
func TestDispatch_NotFoundIsSwallowed(t *testing.T) {
	r, _ := lifecycleReconciler(t)

	result, err := r.dispatch(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "does-not-exist", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected empty result for a not-found sandbox, got %+v", result)
	}
}

// TestDispatch_AddsFinalizerWhenMissing covers the finalizer-bootstrap path:
// a brand-new sandbox with no finalizer yet gets one added and an immediate
// requeue, before any phase-specific logic runs.
func TestDispatch_AddsFinalizerWhenMissing(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-nofinalizer", Namespace: "default"},
	}
	r, c := lifecycleReconciler(t, sandbox)

	result, err := r.dispatch(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "sb-nofinalizer", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result != (ctrl.Result{Requeue: true}) {
		t.Errorf("expected immediate Requeue after adding the finalizer, got %+v", result)
	}

	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-nofinalizer", Namespace: "default"}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	found := false
	for _, f := range got.Finalizers {
		if f == FinalizerName {
			found = true
		}
	}
	if !found {
		t.Errorf("expected finalizer %q to be added, got: %v", FinalizerName, got.Finalizers)
	}
}

// TestDispatch_DeletionTimestampRoutesToReconcileDelete covers the deletion
// branch: once DeletionTimestamp is set, dispatch must route straight to
// reconcileDelete regardless of Status.Phase.
func TestDispatch_DeletionTimestampRoutesToReconcileDelete(t *testing.T) {
	now := metav1.Now()
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sb-deleting",
			Namespace:         "default",
			DeletionTimestamp: &now,
			Finalizers:        []string{FinalizerName},
		},
		Status: agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseRunning},
	}
	r, c := lifecycleReconciler(t, sandbox)

	if _, err := r.dispatch(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "sb-deleting", Namespace: "default"},
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// reconcileDelete removes the finalizer once cleanup completes.
	got := &agenttierv1alpha1.Sandbox{}
	err := c.Get(context.Background(), client.ObjectKey{Name: "sb-deleting", Namespace: "default"}, got)
	if err == nil && len(got.Finalizers) != 0 {
		t.Errorf("expected finalizer removed by reconcileDelete, got: %v", got.Finalizers)
	}
}

// TestDispatch_UnknownPhaseIsNoop covers the default branch of the phase
// switch — an unrecognized Status.Phase value logs and returns an empty
// result rather than erroring.
func TestDispatch_UnknownPhaseIsNoop(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-unknown-phase", Namespace: "default", Finalizers: []string{FinalizerName}},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: "SomethingUnrecognized"},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	result, err := r.dispatch(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "sb-unknown-phase", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected empty result for an unknown phase, got %+v", result)
	}
}

// TestDispatch_StoppedPhaseRoutesToReconcileStopped covers the Stopped
// branch of the phase switch (reconcileStopped is itself a pure no-op, but
// this exercises dispatch's routing to it).
func TestDispatch_StoppedPhaseRoutesToReconcileStopped(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb-stopped-phase", Namespace: "default", Finalizers: []string{FinalizerName}},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseStopped},
	}
	r, _ := lifecycleReconciler(t, sandbox)

	result, err := r.dispatch(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "sb-stopped-phase", Namespace: "default"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Errorf("expected empty result routing through reconcileStopped, got %+v", result)
	}
}
