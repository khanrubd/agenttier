/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package backup

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func schemeWithSnapshots(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatalf("clientgo scheme: %v", err)
	}
	if err := snapshotv1.AddToScheme(s); err != nil {
		t.Fatalf("snapshot scheme: %v", err)
	}
	return s
}

func managedPVC(name string, pooled bool) *corev1.PersistentVolumeClaim {
	labels := map[string]string{LabelManaged: "true", "agenttier.io/sandbox": name}
	if pooled {
		labels[LabelPooled] = "true"
	}
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agenttier", Labels: labels},
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestScheduler_SnapshotsManagedNonPoolPVCs(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		managedPVC("sb-1-workspace", false),
		managedPVC("sb-2-workspace", false),
		managedPVC("pool-x-pvc", true), // must be skipped
	).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")
	created, err := s.snapshotManagedPVCs(context.Background())
	if err != nil {
		t.Fatalf("snapshotManagedPVCs: %v", err)
	}
	if created != 2 {
		t.Fatalf("expected 2 created (pool PVC skipped), got %d", created)
	}

	snaps := &snapshotv1.VolumeSnapshotList{}
	if err := c.List(context.Background(), snaps, client.InNamespace("agenttier")); err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snaps.Items) != 2 {
		t.Fatalf("expected 2 backup snapshots (pool PVC skipped), got %d", len(snaps.Items))
	}
	for _, snap := range snaps.Items {
		if snap.Labels[LabelSnapshotKind] != SnapshotKindBackup {
			t.Errorf("snapshot %s missing the scheduled-backup label", snap.Name)
		}
		if snap.Spec.Source.PersistentVolumeClaimName == nil {
			t.Errorf("snapshot %s has no source PVC", snap.Name)
		}
	}
}

func TestScheduler_PrunesExpiredBackupsOnly(t *testing.T) {
	scheme := schemeWithSnapshots(t)
	old := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "old-backup", Namespace: "agenttier",
			Labels:            map[string]string{LabelSnapshotKind: SnapshotKindBackup},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-30 * 24 * time.Hour)),
		},
	}
	fresh := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "fresh-backup", Namespace: "agenttier",
			Labels:            map[string]string{LabelSnapshotKind: SnapshotKindBackup},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
		},
	}
	// A non-backup snapshot (e.g. a clone snapshot) must never be pruned.
	clone := &snapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name: "a-clone", Namespace: "agenttier",
			Labels:            map[string]string{"agenttier.io/snapshot-kind": "clone"},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-90 * 24 * time.Hour)),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(old, fresh, clone).Build()

	s := NewScheduler(c, discard(), "agenttier", time.Hour, 14*24*time.Hour, "")
	n, err := s.pruneExpired(context.Background())
	if err != nil {
		t.Fatalf("pruneExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pruned (the old backup), got %d", n)
	}
	remaining := &snapshotv1.VolumeSnapshotList{}
	_ = c.List(context.Background(), remaining, client.InNamespace("agenttier"))
	names := map[string]bool{}
	for _, s := range remaining.Items {
		names[s.Name] = true
	}
	if names["old-backup"] {
		t.Error("old backup should have been pruned")
	}
	if !names["fresh-backup"] || !names["a-clone"] {
		t.Error("fresh backup and the clone snapshot must survive")
	}
}
