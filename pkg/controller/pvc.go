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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

const (
	defaultStorageSize = "10Gi"
	defaultMountPath   = "/workspace"
)

// PVCBuilder constructs PersistentVolumeClaims for sandboxes.
type PVCBuilder struct {
	DefaultStorageSize  string
	DefaultMountPath    string
	DefaultStorageClass string
}

// NewPVCBuilder creates a PVCBuilder with defaults.
func NewPVCBuilder(defaultSize, defaultMount, defaultClass string) *PVCBuilder {
	if defaultSize == "" {
		defaultSize = defaultStorageSize
	}
	if defaultMount == "" {
		defaultMount = defaultMountPath
	}
	return &PVCBuilder{
		DefaultStorageSize:  defaultSize,
		DefaultMountPath:    defaultMount,
		DefaultStorageClass: defaultClass,
	}
}

// Build creates a PVC spec for the given sandbox using merged storage configuration.
func (b *PVCBuilder) Build(sandbox *agenttierv1alpha1.Sandbox, storageSpec *agenttierv1alpha1.StorageSpec) *corev1.PersistentVolumeClaim {
	pvcName := fmt.Sprintf("%s-workspace", sandbox.Name)

	// Resolve storage size: spec > default
	size := b.resolveSize(storageSpec)

	// Resolve storage class: spec > default
	storageClass := b.resolveStorageClass(storageSpec)

	// Resolve access mode: shared (RWX) or standard (RWO)
	accessMode := corev1.ReadWriteOnce
	if storageSpec != nil && storageSpec.Shared {
		accessMode = corev1.ReadWriteMany
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: sandbox.Namespace,
			Labels: map[string]string{
				"agenttier.io/sandbox": sandbox.Name,
				"agenttier.io/managed": "true",
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: size,
				},
			},
		},
	}

	// Set storage class if specified (empty = cluster default)
	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}

	return pvc
}

// MountPath returns the resolved mount path for the PVC.
func (b *PVCBuilder) MountPath(storageSpec *agenttierv1alpha1.StorageSpec) string {
	if storageSpec != nil && storageSpec.MountPath != "" {
		return storageSpec.MountPath
	}
	return b.DefaultMountPath
}

// resolveSize determines the PVC size from spec or defaults.
func (b *PVCBuilder) resolveSize(storageSpec *agenttierv1alpha1.StorageSpec) resource.Quantity {
	if storageSpec != nil && !storageSpec.Size.IsZero() {
		return storageSpec.Size
	}
	return resource.MustParse(b.DefaultStorageSize)
}

// resolveStorageClass determines the storage class from spec or defaults.
func (b *PVCBuilder) resolveStorageClass(storageSpec *agenttierv1alpha1.StorageSpec) string {
	if storageSpec != nil && storageSpec.StorageClass != "" {
		return storageSpec.StorageClass
	}
	return b.DefaultStorageClass
}

// ensurePVC creates the PVC if it doesn't exist, or returns the existing one.
func (r *SandboxReconciler) ensurePVC(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, storageSpec *agenttierv1alpha1.StorageSpec) (*corev1.PersistentVolumeClaim, error) {
	logger := log.FromContext(ctx)

	builder := NewPVCBuilder(r.DefaultStorageSize, r.DefaultMountPath, "")
	desired := builder.Build(sandbox, storageSpec)

	// Set owner reference
	if err := controllerutil.SetControllerReference(sandbox, desired, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set owner reference on PVC: %w", err)
	}

	// Check if PVC already exists
	existing := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err == nil {
		// PVC already exists
		logger.V(1).Info("PVC already exists", "pvc", existing.Name)
		return existing, nil
	}
	if !errors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to check existing PVC: %w", err)
	}

	// Create PVC
	logger.Info("creating PVC", "pvc", desired.Name, "size", desired.Spec.Resources.Requests.Storage().String())
	if err := r.Create(ctx, desired); err != nil {
		return nil, fmt.Errorf("failed to create PVC: %w", err)
	}

	return desired, nil
}
