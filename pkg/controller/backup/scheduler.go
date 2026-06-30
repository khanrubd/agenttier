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

// Package backup implements opt-in scheduled VolumeSnapshot backups of
// sandbox workspace PVCs (disaster-recovery Layer 1).
//
// It runs as a controller-side periodic loop rather than a Helm CronJob: the
// controller already holds a client, the volumesnapshot RBAC (shared with the
// cloning feature), and a leader-elected run loop — and there is no reliable
// public `kubectl` image to base a CronJob on. Restore is the existing
// `spec.cloneFromSnapshot` path: create a Sandbox referencing a backup
// snapshot and the controller provisions its PVC from it.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// LabelManaged marks the PVCs we back up (set by the PVC builder).
	LabelManaged = "agenttier.io/managed"
	// LabelPooled marks warm-pool PVCs, which we skip (they're empty).
	LabelPooled = "agenttier.io/pooled"
	// LabelSnapshotKind tags the snapshots this scheduler creates so the
	// retention sweep only ever prunes its own backups (never clone or
	// snapshot-on-stop snapshots).
	LabelSnapshotKind = "agenttier.io/snapshot-kind"
	// SnapshotKindBackup is the LabelSnapshotKind value for scheduled backups.
	SnapshotKindBackup = "scheduled-backup"
	// LabelSourcePVC records which PVC a backup snapshot came from.
	LabelSourcePVC = "agenttier.io/source-pvc"
)

// Scheduler periodically snapshots managed sandbox PVCs and prunes snapshots
// older than the retention window.
type Scheduler struct {
	client    client.Client
	logger    *slog.Logger
	namespace string // sandbox namespace to scan
	interval  time.Duration
	retention time.Duration
	// snapshotClassName, when set, is stamped on created VolumeSnapshots;
	// empty uses the cluster's default VolumeSnapshotClass.
	snapshotClassName string
	// now is indirected for tests.
	now func() time.Time
}

// NewScheduler builds a backup scheduler. interval is how often a backup pass
// runs; retention is how long backup snapshots are kept before pruning.
func NewScheduler(c client.Client, logger *slog.Logger, namespace string, interval, retention time.Duration, snapshotClassName string) *Scheduler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{
		client:            c,
		logger:            logger,
		namespace:         namespace,
		interval:          interval,
		retention:         retention,
		snapshotClassName: snapshotClassName,
		now:               time.Now,
	}
}

// RunLoop runs backup passes on the configured interval until ctx is cancelled.
// Intended to be registered as a leader-elected manager Runnable.
func (s *Scheduler) RunLoop(ctx context.Context) {
	s.logger.Info("backup scheduler started",
		"namespace", s.namespace, "interval", s.interval.String(), "retention", s.retention.String())
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	// Run once shortly after start so a fresh install gets an early baseline.
	s.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single backup + prune pass. Exported for tests.
func (s *Scheduler) RunOnce(ctx context.Context) {
	created, err := s.snapshotManagedPVCs(ctx)
	if err != nil {
		s.logger.Error("backup snapshot pass failed", "error", err)
	} else if created > 0 {
		s.logger.Info("backup snapshots created", "count", created)
	}
	pruned, err := s.pruneExpired(ctx)
	if err != nil {
		s.logger.Error("backup retention prune failed", "error", err)
	} else if pruned > 0 {
		s.logger.Info("backup snapshots pruned", "count", pruned)
	}
}

// snapshotManagedPVCs creates a VolumeSnapshot for each managed, non-pool PVC.
func (s *Scheduler) snapshotManagedPVCs(ctx context.Context) (int, error) {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := s.client.List(ctx, pvcs,
		client.InNamespace(s.namespace),
		client.MatchingLabels{LabelManaged: "true"},
	); err != nil {
		return 0, fmt.Errorf("list managed PVCs: %w", err)
	}
	count := 0
	for i := range pvcs.Items {
		pvc := &pvcs.Items[i]
		if pvc.Labels[LabelPooled] == "true" {
			continue // skip empty warm-pool PVCs
		}
		pvcName := pvc.Name
		snap := &snapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-backup-%d", pvcName, s.now().Unix()),
				Namespace: s.namespace,
				Labels: map[string]string{
					LabelSnapshotKind: SnapshotKindBackup,
					LabelSourcePVC:    pvcName,
					LabelManaged:      "true",
				},
			},
			Spec: snapshotv1.VolumeSnapshotSpec{
				Source: snapshotv1.VolumeSnapshotSource{PersistentVolumeClaimName: &pvcName},
			},
		}
		if s.snapshotClassName != "" {
			cn := s.snapshotClassName
			snap.Spec.VolumeSnapshotClassName = &cn
		}
		if err := s.client.Create(ctx, snap); err != nil {
			s.logger.Error("create backup snapshot failed", "pvc", pvcName, "error", err)
			continue
		}
		count++
	}
	return count, nil
}

// pruneExpired deletes backup snapshots older than the retention window.
func (s *Scheduler) pruneExpired(ctx context.Context) (int, error) {
	snaps := &snapshotv1.VolumeSnapshotList{}
	if err := s.client.List(ctx, snaps,
		client.InNamespace(s.namespace),
		client.MatchingLabels{LabelSnapshotKind: SnapshotKindBackup},
	); err != nil {
		return 0, fmt.Errorf("list backup snapshots: %w", err)
	}
	cutoff := s.now().Add(-s.retention)
	count := 0
	for i := range snaps.Items {
		snap := &snaps.Items[i]
		// Never prune a snapshot whose creation time isn't known yet (a
		// just-created object before the apiserver stamps it) — treating a
		// zero timestamp as "ancient" would delete fresh backups.
		if snap.CreationTimestamp.IsZero() || snap.CreationTimestamp.Time.After(cutoff) {
			continue
		}
		if err := s.client.Delete(ctx, snap); err != nil {
			s.logger.Error("delete expired backup snapshot failed", "snapshot", snap.Name, "error", err)
			continue
		}
		count++
	}
	return count, nil
}
