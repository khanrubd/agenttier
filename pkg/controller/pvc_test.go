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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func TestNewPVCBuilder_Defaults(t *testing.T) {
	b := NewPVCBuilder("", "", "gp3")
	if b.DefaultStorageSize != defaultStorageSize {
		t.Errorf("DefaultStorageSize = %q, want %q", b.DefaultStorageSize, defaultStorageSize)
	}
	if b.DefaultMountPath != defaultMountPath {
		t.Errorf("DefaultMountPath = %q, want %q", b.DefaultMountPath, defaultMountPath)
	}
	if b.DefaultStorageClass != "gp3" {
		t.Errorf("DefaultStorageClass = %q, want gp3", b.DefaultStorageClass)
	}
}

func TestNewPVCBuilder_ExplicitOverrides(t *testing.T) {
	b := NewPVCBuilder("20Gi", "/data", "")
	if b.DefaultStorageSize != "20Gi" {
		t.Errorf("DefaultStorageSize = %q, want 20Gi", b.DefaultStorageSize)
	}
	if b.DefaultMountPath != "/data" {
		t.Errorf("DefaultMountPath = %q, want /data", b.DefaultMountPath)
	}
}

func TestPVCBuilder_Build_DefaultsWhenNoStorageSpec(t *testing.T) {
	b := NewPVCBuilder("", "", "")
	sandbox := &agenttierv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb1", Namespace: "default"}}

	pvc := b.Build(sandbox, nil)

	if pvc.Name != "sb1-workspace" {
		t.Errorf("name = %q, want sb1-workspace", pvc.Name)
	}
	if pvc.Namespace != "default" {
		t.Errorf("namespace = %q, want default", pvc.Namespace)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("access modes = %v, want [ReadWriteOnce]", pvc.Spec.AccessModes)
	}
	got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	want := resource.MustParse(defaultStorageSize)
	if got.Cmp(want) != 0 {
		t.Errorf("storage size = %v, want %v", got, want)
	}
	if pvc.Spec.StorageClassName != nil {
		t.Errorf("storageClassName = %v, want nil (cluster default)", *pvc.Spec.StorageClassName)
	}
	if pvc.Labels["agenttier.io/sandbox"] != "sb1" || pvc.Labels["agenttier.io/managed"] != "true" {
		t.Errorf("unexpected labels: %+v", pvc.Labels)
	}
}

func TestPVCBuilder_Build_SharedUsesReadWriteMany(t *testing.T) {
	b := NewPVCBuilder("", "", "")
	sandbox := &agenttierv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb2", Namespace: "default"}}
	storageSpec := &agenttierv1alpha1.StorageSpec{Shared: true}

	pvc := b.Build(sandbox, storageSpec)

	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteMany {
		t.Errorf("access modes = %v, want [ReadWriteMany] when Shared=true", pvc.Spec.AccessModes)
	}
}

func TestPVCBuilder_Build_StorageSpecOverridesSizeAndClass(t *testing.T) {
	b := NewPVCBuilder("10Gi", "", "default-class")
	sandbox := &agenttierv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb3", Namespace: "default"}}
	storageSpec := &agenttierv1alpha1.StorageSpec{
		Size:         resource.MustParse("50Gi"),
		StorageClass: "fast-ssd",
	}

	pvc := b.Build(sandbox, storageSpec)

	got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	want := resource.MustParse("50Gi")
	if got.Cmp(want) != 0 {
		t.Errorf("storage size = %v, want %v (spec override)", got, want)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast-ssd" {
		t.Errorf("storageClassName = %v, want fast-ssd", pvc.Spec.StorageClassName)
	}
}

func TestPVCBuilder_Build_CloneFromSnapshotSetsDataSource(t *testing.T) {
	b := NewPVCBuilder("", "", "")
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sb4", Namespace: "default"},
		Spec:       agenttierv1alpha1.SandboxSpec{CloneFromSnapshot: "snap-abc"},
	}

	pvc := b.Build(sandbox, nil)

	if pvc.Spec.DataSource == nil {
		t.Fatal("expected DataSource to be set for CloneFromSnapshot")
	}
	if pvc.Spec.DataSource.Kind != "VolumeSnapshot" || pvc.Spec.DataSource.Name != "snap-abc" {
		t.Errorf("dataSource = %+v, want VolumeSnapshot/snap-abc", pvc.Spec.DataSource)
	}
	if pvc.Spec.DataSource.APIGroup == nil || *pvc.Spec.DataSource.APIGroup != "snapshot.storage.k8s.io" {
		t.Errorf("dataSource APIGroup = %v, want snapshot.storage.k8s.io", pvc.Spec.DataSource.APIGroup)
	}
	if pvc.Labels["agenttier.io/cloned-from-snapshot"] != "snap-abc" {
		t.Errorf("expected cloned-from-snapshot label, got: %+v", pvc.Labels)
	}
}

func TestPVCBuilder_MountPath(t *testing.T) {
	b := NewPVCBuilder("", "/default-mount", "")

	if got := b.MountPath(nil); got != "/default-mount" {
		t.Errorf("MountPath(nil) = %q, want /default-mount", got)
	}
	custom := &agenttierv1alpha1.StorageSpec{MountPath: "/custom"}
	if got := b.MountPath(custom); got != "/custom" {
		t.Errorf("MountPath(custom) = %q, want /custom", got)
	}
	empty := &agenttierv1alpha1.StorageSpec{}
	if got := b.MountPath(empty); got != "/default-mount" {
		t.Errorf("MountPath(empty spec) = %q, want default /default-mount", got)
	}
}

func TestPVCBuilder_ResolveSize(t *testing.T) {
	b := NewPVCBuilder("15Gi", "", "")

	if got := b.resolveSize(nil); got.Cmp(resource.MustParse("15Gi")) != 0 {
		t.Errorf("resolveSize(nil) = %v, want 15Gi default", got)
	}
	spec := &agenttierv1alpha1.StorageSpec{Size: resource.MustParse("5Gi")}
	if got := b.resolveSize(spec); got.Cmp(resource.MustParse("5Gi")) != 0 {
		t.Errorf("resolveSize(spec) = %v, want 5Gi from spec", got)
	}
	zeroSpec := &agenttierv1alpha1.StorageSpec{}
	if got := b.resolveSize(zeroSpec); got.Cmp(resource.MustParse("15Gi")) != 0 {
		t.Errorf("resolveSize(zero-value spec) = %v, want fallback to default 15Gi", got)
	}
}

func TestPVCBuilder_ResolveStorageClass(t *testing.T) {
	b := NewPVCBuilder("", "", "cluster-default")

	if got := b.resolveStorageClass(nil); got != "cluster-default" {
		t.Errorf("resolveStorageClass(nil) = %q, want cluster-default", got)
	}
	spec := &agenttierv1alpha1.StorageSpec{StorageClass: "premium"}
	if got := b.resolveStorageClass(spec); got != "premium" {
		t.Errorf("resolveStorageClass(spec) = %q, want premium", got)
	}
}

func TestEnsurePVC_CreatesWhenAbsent(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-pvc-new", Namespace: "default"}}
	r, c := lifecycleReconciler(t, sandbox)

	pvc, err := r.ensurePVC(context.Background(), sandbox, nil)
	if err != nil {
		t.Fatalf("ensurePVC: %v", err)
	}
	if pvc.Name != "sb-pvc-new-workspace" {
		t.Errorf("name = %q, want sb-pvc-new-workspace", pvc.Name)
	}

	got := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(pvc), got); err != nil {
		t.Fatalf("expected PVC to be created and gettable: %v", err)
	}
	if len(got.OwnerReferences) != 1 || got.OwnerReferences[0].Kind != "Sandbox" {
		t.Errorf("owner reference missing or wrong kind: %+v", got.OwnerReferences)
	}
}

func TestEnsurePVC_IdempotentReturnsExisting(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: "sb-pvc-idem", Namespace: "default"}}
	r, _ := lifecycleReconciler(t, sandbox)

	first, err := r.ensurePVC(context.Background(), sandbox, nil)
	if err != nil {
		t.Fatalf("first ensurePVC: %v", err)
	}
	second, err := r.ensurePVC(context.Background(), sandbox, nil)
	if err != nil {
		t.Fatalf("second ensurePVC: %v", err)
	}
	if first.Name != second.Name {
		t.Errorf("expected idempotent PVC name, got %q then %q", first.Name, second.Name)
	}
}
