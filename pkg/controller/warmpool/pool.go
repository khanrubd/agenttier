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

// Package warmpool manages a pool of pre-created idle sandbox pods
// that can be instantly claimed when a user creates a new sandbox.
//
// Architecture:
//   - Config is stored in a ConfigMap (agenttier-warmpool-config in the
//     install namespace, configurable via Reconciler.Namespace).
//   - The Controller reconciles the pool (has leader election, single writer)
//   - The Router API reads/writes the ConfigMap (stateless)
//   - Uses gp3-immediate StorageClass for pre-provisioned EBS volumes
//
// Namespace is plumbed through every operation. The pool ConfigMap, the pool
// Pods, and the pool PVCs all live in the same namespace as the AgentTier
// install. Sandboxes from any namespace can claim from the pool — the
// controller relabels the claimed Pod to belong to the requesting Sandbox.
package warmpool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

const (
	// Labels for identifying warm pool resources
	LabelPooled   = "agenttier.io/pooled"
	LabelTemplate = "agenttier.io/pool-template"

	// ConfigMapName is the well-known ConfigMap that stores the warm pool
	// configuration. The namespace is configurable per install via
	// Reconciler.Namespace / Options.Namespace.
	ConfigMapName = "agenttier-warmpool-config"

	// DefaultNamespace is used as a last-resort fallback when no install
	// namespace is provided. New installs should always pass a namespace
	// explicitly via the controller's POD_NAMESPACE env var.
	DefaultNamespace = "agenttier"

	// StorageClass for warm pool PVCs (Immediate binding)
	PoolStorageClass = "gp3-immediate"

	// How often the controller reconciles the pool
	ReconcileInterval = 15 * time.Second

	// claimMaxAttempts caps how many fresh-list retries Claim makes when
	// it loses an optimistic-concurrency race. Three is plenty in practice:
	// each retry re-Lists and walks every Ready pod, so even with high
	// burst contention the chance of three consecutive losses is
	// vanishingly small. More retries would just amplify apiserver load
	// without changing the outcome (caller falls through to cold start).
	claimMaxAttempts = 3
)

// Config is persisted in the ConfigMap.
type Config struct {
	DesiredCount int    `json:"desiredCount"`
	Template     string `json:"template"`
}

// Status reports the current pool state (computed live, not stored).
type Status struct {
	DesiredCount int    `json:"desiredCount"`
	ReadyCount   int    `json:"readyCount"`
	PendingCount int    `json:"pendingCount"`
	Template     string `json:"template"`
}

// Reconciler is run by the Controller (which has leader election).
// It reads config from the ConfigMap and converges the pool to the desired state.
type Reconciler struct {
	client client.Client
	logger *slog.Logger
	// Namespace is where the warm pool lives — pool ConfigMap, pool Pods,
	// pool PVCs. Set from POD_NAMESPACE in the controller deployment.
	namespace string
}

// NewReconciler creates a warm pool reconciler. Namespace defaults to
// DefaultNamespace ("agenttier") when empty, but production installs should
// always pass the install namespace explicitly via POD_NAMESPACE.
func NewReconciler(k8sClient client.Client, logger *slog.Logger, namespace string) *Reconciler {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	return &Reconciler{
		client:    k8sClient,
		logger:    logger,
		namespace: namespace,
	}
}

// Namespace returns the namespace the reconciler operates in. Useful for
// callers (router handlers) that need to read/write the pool ConfigMap from
// the same namespace.
func (r *Reconciler) Namespace() string { return r.namespace }

// RunLoop starts the reconcile loop. Call this from the controller's main goroutine.
func (r *Reconciler) RunLoop(ctx context.Context) {
	r.logger.Info("warm pool reconciler started", "namespace", r.namespace)
	ticker := time.NewTicker(ReconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("warm pool reconciler stopped")
			return
		case <-ticker.C:
			if err := r.Reconcile(ctx); err != nil {
				r.logger.Error("warm pool reconcile error", "error", err)
			}
		}
	}
}

// Reconcile reads config and converges the pool.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	cfg, err := r.ReadConfig(ctx)
	if err != nil {
		return nil // No config = pool disabled, not an error
	}

	if cfg.DesiredCount <= 0 || cfg.Template == "" {
		// Pool disabled — scale down any existing pool pods
		return r.scaleDown(ctx, cfg.Template, 0)
	}

	// Count existing pool pods for this template
	pods, err := r.listPoolPods(ctx, cfg.Template)
	if err != nil {
		return err
	}

	currentCount := len(pods)

	if currentCount < cfg.DesiredCount {
		// Scale up — create one pod per reconcile cycle (avoids bursts)
		return r.createPoolPod(ctx, cfg.Template)
	} else if currentCount > cfg.DesiredCount {
		// Scale down
		return r.scaleDown(ctx, cfg.Template, cfg.DesiredCount)
	}

	return nil
}

// ReadConfig reads the warm pool config from the ConfigMap.
func (r *Reconciler) ReadConfig(ctx context.Context) (*Config, error) {
	cm := &corev1.ConfigMap{}
	err := r.client.Get(ctx, client.ObjectKey{
		Name:      ConfigMapName,
		Namespace: r.namespace,
	}, cm)
	if err != nil {
		return nil, err
	}

	data, ok := cm.Data["config"]
	if !ok {
		return &Config{}, nil
	}

	var cfg Config
	if err := json.Unmarshal([]byte(data), &cfg); err != nil {
		return nil, fmt.Errorf("invalid warmpool config: %w", err)
	}
	return &cfg, nil
}

// GetStatus computes the current pool status. Called by the Router API and
// must be passed the namespace where AgentTier is installed.
func GetStatus(ctx context.Context, k8sClient client.Client, namespace string) (*Status, error) {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	// Read config
	cm := &corev1.ConfigMap{}
	err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      ConfigMapName,
		Namespace: namespace,
	}, cm)

	cfg := &Config{DesiredCount: 0, Template: ""}
	if err == nil {
		if data, ok := cm.Data["config"]; ok {
			_ = json.Unmarshal([]byte(data), cfg)
		}
	}

	// Count pods
	podList := &corev1.PodList{}
	labels := client.MatchingLabels{LabelPooled: "true"}
	if cfg.Template != "" {
		labels[LabelTemplate] = cfg.Template
	}
	if err := k8sClient.List(ctx, podList, client.InNamespace(namespace), labels); err != nil {
		return &Status{DesiredCount: cfg.DesiredCount, Template: cfg.Template}, nil
	}

	ready := 0
	pending := 0
	for _, pod := range podList.Items {
		if isPodReady(&pod) {
			ready++
		} else {
			pending++
		}
	}

	return &Status{
		DesiredCount: cfg.DesiredCount,
		ReadyCount:   ready,
		PendingCount: pending,
		Template:     cfg.Template,
	}, nil
}

// SetConfig writes the warm pool config to the ConfigMap. Called by the
// Router API and must be passed the namespace where AgentTier is installed.
func SetConfig(ctx context.Context, k8sClient client.Client, namespace string, cfg Config) error {
	if namespace == "" {
		namespace = DefaultNamespace
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	cm := &corev1.ConfigMap{}
	err = k8sClient.Get(ctx, client.ObjectKey{
		Name:      ConfigMapName,
		Namespace: namespace,
	}, cm)

	if errors.IsNotFound(err) {
		// Create the ConfigMap
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ConfigMapName,
				Namespace: namespace,
			},
			Data: map[string]string{"config": string(data)},
		}
		return k8sClient.Create(ctx, cm)
	} else if err != nil {
		return err
	}

	// Update existing
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["config"] = string(data)
	return k8sClient.Update(ctx, cm)
}

// Claim finds a ready pool pod and removes it from the pool (marks it as claimed).
// Returns pod name and PVC name, or empty strings if no pod available.
//
// The pool itself lives in `namespace` (where AgentTier is installed). After
// claiming, the controller relabels the Pod to belong to the requesting
// Sandbox — which can live in any namespace.
//
// Concurrency model: two callers can race on the same pool pod. We use the
// resourceVersion-based optimistic concurrency Kubernetes provides — the
// kube-apiserver rejects an Update with errors.IsConflict if another writer
// committed first. On conflict we re-list and retry up to claimMaxAttempts
// times with a fresh List, so the loser of a race always either grabs a
// different ready pod or returns empty (and the requester falls through to
// a normal cold start). The conflicts metric lets operators see contention.
func Claim(ctx context.Context, k8sClient client.Client, namespace, template string) (podName, pvcName string, err error) {
	if namespace == "" {
		namespace = DefaultNamespace
	}

	// We'll loop up to claimMaxAttempts times on conflict. Each attempt
	// re-lists from the apiserver so we always see the freshest state and
	// don't keep retrying the same pod another claimer just grabbed.
	for attempt := 0; attempt < claimMaxAttempts; attempt++ {
		podList := &corev1.PodList{}
		if err := k8sClient.List(ctx, podList,
			client.InNamespace(namespace),
			client.MatchingLabels{LabelPooled: "true", LabelTemplate: template},
		); err != nil {
			return "", "", err
		}

		// Try each ready pod. Conflicts on a specific pod mean another
		// claimer won that pod — keep walking the list rather than
		// jumping straight to a re-list, since the same List response
		// likely has more candidates.
		var sawConflict bool
		for i := range podList.Items {
			pod := &podList.Items[i]
			if !isPodReady(pod) {
				continue
			}

			// CAS-style claim: copy with label removal, attempt Update.
			// The apiserver rejects with IsConflict when our copy's
			// resourceVersion is stale relative to a concurrent winner.
			podCopy := pod.DeepCopy()
			delete(podCopy.Labels, LabelPooled)
			delete(podCopy.Labels, LabelTemplate)

			if err := k8sClient.Update(ctx, podCopy); err != nil {
				if errors.IsConflict(err) {
					ClaimConflictsTotal.Inc()
					sawConflict = true
					continue // Another claimer won this pod; try next
				}
				if errors.IsNotFound(err) {
					// Pod was deleted between our List and Update —
					// e.g. by a scaleDown. Treat like a conflict and
					// keep walking.
					sawConflict = true
					continue
				}
				return "", "", fmt.Errorf("update pool pod %s: %w", pod.Name, err)
			}

			// Won the claim. Find the PVC name and return.
			pvc := ""
			for _, vol := range pod.Spec.Volumes {
				if vol.PersistentVolumeClaim != nil {
					pvc = vol.PersistentVolumeClaim.ClaimName
					break
				}
			}
			return pod.Name, pvc, nil
		}

		// Walked the entire list. If we never saw a conflict, there are
		// genuinely no ready pods to claim — bail out without wasting
		// another round-trip.
		if !sawConflict {
			return "", "", nil
		}
		// Otherwise we lost every race we tried; fresh List on the next
		// loop iteration may surface pods that just became Ready.
	}

	// Exhausted retries — every Ready pod we saw was claimed by someone
	// else. Return empty (caller falls through to cold start).
	return "", "", nil
}

// createPoolPod creates a single idle pod for the warm pool.
func (r *Reconciler) createPoolPod(ctx context.Context, templateName string) error {
	// Resolve template
	tmpl := &agenttierv1alpha1.ClusterSandboxTemplate{}
	if err := r.client.Get(ctx, client.ObjectKey{Name: templateName}, tmpl); err != nil {
		return fmt.Errorf("template %s not found: %w", templateName, err)
	}

	image := "ghcr.io/agenttier/sandbox-general:latest"
	if tmpl.Spec.Image != nil && tmpl.Spec.Image.Repository != "" {
		image = tmpl.Spec.Image.Repository
	}

	// Generate unique names
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	podName := fmt.Sprintf("pool-%s-%s", templateName[:min(len(templateName), 12)], suffix)
	pvcName := podName + "-pvc"

	// Determine storage size
	storageSize := "10Gi"
	if tmpl.Spec.Storage != nil && !tmpl.Spec.Storage.Size.IsZero() {
		storageSize = tmpl.Spec.Storage.Size.String()
	}

	// Create PVC with Immediate binding (key difference from regular sandboxes)
	storageClass := PoolStorageClass
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: r.namespace,
			Labels: map[string]string{
				LabelPooled:   "true",
				LabelTemplate: templateName,
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: mustParseQuantity(storageSize),
				},
			},
		},
	}
	if err := r.client.Create(ctx, pvc); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create pool PVC: %w", err)
	}

	// Build env vars from template
	var envVars []corev1.EnvVar
	envVars = append(envVars, tmpl.Spec.Env...)

	// Create Pod
	var user int64 = 1000
	var group int64 = 1000
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: r.namespace,
			Labels: map[string]string{
				LabelPooled:            "true",
				LabelTemplate:          templateName,
				"agenttier.io/managed": "true",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsUser:  &user,
				RunAsGroup: &group,
				FSGroup:    &group,
			},
			Containers: []corev1.Container{
				{
					Name:  "sandbox",
					Image: image,
					Stdin: true,
					TTY:   true,
					Env:   envVars,
					VolumeMounts: []corev1.VolumeMount{
						{Name: "workspace", MountPath: "/workspace"},
					},
					ImagePullPolicy: corev1.PullIfNotPresent,
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "workspace",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
		},
	}

	if err := r.client.Create(ctx, pod); err != nil {
		return fmt.Errorf("failed to create pool pod: %w", err)
	}

	r.logger.Info("created warm pool pod", "pod", podName, "pvc", pvcName, "template", templateName, "namespace", r.namespace)
	return nil
}

// scaleDown removes excess pool pods to reach the target count.
func (r *Reconciler) scaleDown(ctx context.Context, template string, targetCount int) error {
	pods, err := r.listPoolPods(ctx, template)
	if err != nil {
		return err
	}

	toDelete := len(pods) - targetCount
	if toDelete <= 0 {
		return nil
	}

	deleted := 0
	for i := range pods {
		if deleted >= toDelete {
			break
		}
		if err := r.deletePodAndPVC(ctx, &pods[i]); err != nil {
			r.logger.Error("failed to delete pool pod", "pod", pods[i].Name, "error", err)
			continue
		}
		deleted++
	}

	if deleted > 0 {
		r.logger.Info("scaled down warm pool", "deleted", deleted, "remaining", len(pods)-deleted)
	}
	return nil
}

func (r *Reconciler) deletePodAndPVC(ctx context.Context, pod *corev1.Pod) error {
	if err := r.client.Delete(ctx, pod); err != nil && !errors.IsNotFound(err) {
		return err
	}
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      vol.PersistentVolumeClaim.ClaimName,
					Namespace: pod.Namespace,
				},
			}
			_ = r.client.Delete(ctx, pvc)
		}
	}
	return nil
}

func (r *Reconciler) listPoolPods(ctx context.Context, template string) ([]corev1.Pod, error) {
	podList := &corev1.PodList{}
	labels := client.MatchingLabels{LabelPooled: "true"}
	if template != "" {
		labels[LabelTemplate] = template
	}
	if err := r.client.List(ctx, podList, client.InNamespace(r.namespace), labels); err != nil {
		return nil, err
	}
	return podList.Items, nil
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func mustParseQuantity(s string) resource.Quantity {
	q, _ := resource.ParseQuantity(s)
	return q
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
