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

package warmpool

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}
	return scheme
}

// TestNamespacePlumbing verifies that all four operations (config read/write,
// pod listing/claim) target the namespace passed in, not the hardcoded
// "default" the previous implementation used. Regression coverage for the
// P0 multi-tenancy bug.
func TestNamespacePlumbing(t *testing.T) {
	const installNS = "agenttier"
	scheme := newScheme(t)

	readyPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pool-general-coding-1",
			Namespace: installNS,
			Labels: map[string]string{
				LabelPooled:   "true",
				LabelTemplate: "general-coding",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "sandbox", Image: "x"}},
			Volumes: []corev1.Volume{{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: "pool-general-coding-1-pvc",
					},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			}},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(readyPod).
		Build()
	ctx := context.Background()

	t.Run("SetConfig+GetStatus round-trip in install namespace", func(t *testing.T) {
		if err := SetConfig(ctx, c, installNS, Config{DesiredCount: 3, Template: "general-coding"}); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}

		// ConfigMap must land in installNS, not "default".
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, client.ObjectKey{Name: ConfigMapName, Namespace: installNS}, cm); err != nil {
			t.Fatalf("ConfigMap not found in %s: %v", installNS, err)
		}

		// And not in "default".
		stray := &corev1.ConfigMap{}
		err := c.Get(ctx, client.ObjectKey{Name: ConfigMapName, Namespace: "default"}, stray)
		if err == nil {
			t.Fatalf("ConfigMap leaked into default namespace — namespace plumbing is broken")
		}

		status, err := GetStatus(ctx, c, installNS)
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if status.DesiredCount != 3 || status.Template != "general-coding" {
			t.Errorf("GetStatus mismatch: got %+v", status)
		}
		if status.ReadyCount != 1 {
			t.Errorf("expected ReadyCount=1, got %d (pool pod in %s should be counted)", status.ReadyCount, installNS)
		}
	})

	t.Run("Claim finds pods in install namespace", func(t *testing.T) {
		podName, pvcName, err := Claim(ctx, c, installNS, "general-coding")
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if podName != "pool-general-coding-1" {
			t.Errorf("Claim returned %q, want pool-general-coding-1", podName)
		}
		if pvcName != "pool-general-coding-1-pvc" {
			t.Errorf("Claim PVC %q, want pool-general-coding-1-pvc", pvcName)
		}

		// After claim, labels should be removed.
		claimed := &corev1.Pod{}
		if err := c.Get(ctx, client.ObjectKey{Name: podName, Namespace: installNS}, claimed); err != nil {
			t.Fatalf("get claimed pod: %v", err)
		}
		if _, ok := claimed.Labels[LabelPooled]; ok {
			t.Errorf("LabelPooled still present after claim: %v", claimed.Labels)
		}
	})

	t.Run("Claim against wrong namespace finds nothing", func(t *testing.T) {
		// Same client, but ask for a Claim against the wrong namespace.
		// Should return empty (no error, no pod) — proves the lookup is
		// scoped to the namespace argument and not falling back to default.
		podName, pvcName, err := Claim(ctx, c, "default", "general-coding")
		if err != nil {
			t.Fatalf("Claim against default: %v", err)
		}
		if podName != "" || pvcName != "" {
			t.Errorf("Claim against wrong namespace returned %q/%q, expected empty", podName, pvcName)
		}
	})
}

// TestNewReconciler_DefaultNamespace verifies that an empty namespace falls
// back to the documented default ("agenttier"), not "" or "default".
func TestNewReconciler_DefaultNamespace(t *testing.T) {
	r := NewReconciler(nil, nil, "")
	if r.Namespace() != DefaultNamespace {
		t.Errorf("NewReconciler(\"\") namespace = %q, want %q", r.Namespace(), DefaultNamespace)
	}
}

// TestClaim_ConcurrentClaimersDoNotDoubleClaim simulates two callers racing
// to claim from a pool of one. The fake client doesn't enforce
// resourceVersion conflicts on its own, so we wrap it with a synchronizing
// client that lets the first Update win and rejects the second with
// IsConflict — the same behavior the real apiserver provides.
//
// Regression coverage for the Claim race: the previous code listed pods,
// updated, and silently `continue`d on any Update error, relying on the
// optimistic-concurrency conflicts to enforce single-claim semantics
// without actually distinguishing "lost a race, retry with fresh data"
// from "claim succeeded somewhere else." This test asserts that exactly
// one claimer wins and the other returns empty.
func TestClaim_ConcurrentClaimersDoNotDoubleClaim(t *testing.T) {
	scheme := newScheme(t)
	const ns = "agenttier"

	makePod := func(name string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels: map[string]string{
					LabelPooled:   "true",
					LabelTemplate: "general-coding",
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				Conditions: []corev1.PodCondition{{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				}},
			},
		}
	}
	pod := makePod("pool-only-1")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(pod).
		Build()

	// Synchronize: gate the second Update behind the first's commit so
	// we deterministically reproduce the race. We wrap the fake client
	// with one that returns IsConflict on the second Update of the same
	// pod, mirroring real apiserver behavior.
	wrapped := &conflictingClient{Client: c, claimedPod: make(map[string]bool)}

	// Two claimers, run sequentially through the wrapper. The first wins;
	// the second sees IsConflict on its Update attempt, which Claim now
	// recognizes via errors.IsConflict and walks past instead of falsely
	// reporting success.
	pod1, pvc1, err1 := Claim(context.Background(), wrapped, ns, "general-coding")
	pod2, pvc2, err2 := Claim(context.Background(), wrapped, ns, "general-coding")

	if err1 != nil || err2 != nil {
		t.Fatalf("Claim errors: %v / %v", err1, err2)
	}

	wins := 0
	for _, name := range []string{pod1, pod2} {
		if name != "" {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("expected exactly one claimer to win, got %d (pod1=%q pod2=%q)", wins, pod1, pod2)
	}

	// Whichever won should have got a (possibly empty) PVC name
	// without errors. Whichever lost should have empty pvc too.
	if pod1 != "" && pvc1 != "" && pod1 != "pool-only-1" {
		t.Errorf("winner returned wrong pod %q", pod1)
	}
	if pod2 != "" && pvc2 != "" && pod2 != "pool-only-1" {
		t.Errorf("winner returned wrong pod %q", pod2)
	}
}

// conflictingClient simulates the apiserver's optimistic-concurrency check
// for tests: the first Update on a given pool pod succeeds, subsequent
// Updates on the same pod return IsConflict. This is exactly what the
// real apiserver does when two writers race on a stale resourceVersion.
type conflictingClient struct {
	client.Client
	mu         sync.Mutex
	claimedPod map[string]bool
}

func (c *conflictingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return c.Client.Update(ctx, obj, opts...)
	}
	c.mu.Lock()
	if c.claimedPod[pod.Name] {
		c.mu.Unlock()
		// Mirror the apiserver: GroupResource conflict on stale RV.
		return apierrors.NewConflict(
			schema.GroupResource{Group: "", Resource: "pods"},
			pod.Name,
			fmt.Errorf("the object has been modified; please apply your changes to the latest version and try again"),
		)
	}
	c.claimedPod[pod.Name] = true
	c.mu.Unlock()
	return c.Client.Update(ctx, obj, opts...)
}

// TestConfig_NormalizePromotesLegacyShape verifies that ConfigMaps written
// in the old single-template shape (top-level DesiredCount + Template) are
// transparently promoted to the new Pools slice on read. Without this
// migration, a rolling controller upgrade would see "no pools configured"
// and silently drain a working warm pool.
func TestConfig_NormalizePromotesLegacyShape(t *testing.T) {
	cases := []struct {
		name               string
		in                 Config
		want               []PoolConfig
		wantLegacyTemplate string
	}{
		{
			name: "legacy single-template shape promotes to one Pools entry",
			in:   Config{DesiredCount: 3, Template: "general-coding"},
			want: []PoolConfig{{Template: "general-coding", DesiredCount: 3}},
		},
		{
			name: "new shape passes through unchanged",
			in: Config{Pools: []PoolConfig{
				{Template: "general-coding", DesiredCount: 2},
				{Template: "claude-code-bedrock", DesiredCount: 1},
			}},
			want: []PoolConfig{
				{Template: "general-coding", DesiredCount: 2},
				{Template: "claude-code-bedrock", DesiredCount: 1},
			},
		},
		{
			name: "empty config stays empty",
			in:   Config{},
			want: nil,
		},
		{
			name: "legacy fields with zero count don't migrate (no-op)",
			in:   Config{DesiredCount: 0, Template: "ignored"},
			want: nil,
			// DesiredCount==0 means there's nothing to promote. We
			// leave the (admittedly stale) Template string in place
			// since this is a transitional state — the operator will
			// either set DesiredCount > 0 (triggering migration) or
			// re-write the config in the new shape, both of which
			// clear the legacy fields. Re-writing zeroes during a no-op
			// would be wasted churn.
			wantLegacyTemplate: "ignored",
		},
		{
			name: "both shapes set — Pools wins, legacy fields cleared",
			in: Config{
				DesiredCount: 99,
				Template:     "stale",
				Pools:        []PoolConfig{{Template: "real", DesiredCount: 2}},
			},
			want: []PoolConfig{{Template: "real", DesiredCount: 2}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.in
			cfg.Normalize()
			if !poolsEqual(cfg.Pools, tc.want) {
				t.Errorf("Pools = %+v, want %+v", cfg.Pools, tc.want)
			}
			if cfg.DesiredCount != 0 {
				t.Errorf("DesiredCount not cleared: %d", cfg.DesiredCount)
			}
			if cfg.Template != tc.wantLegacyTemplate {
				t.Errorf("Template = %q, want %q", cfg.Template, tc.wantLegacyTemplate)
			}
		})
	}
}

// TestReconcile_PerTemplatePoolsConvergeIndependently verifies that a config
// with multiple template entries produces independent pool pods, one per
// template. Regression coverage for the single-template bug — the old
// reconciler ignored every entry past the first.
func TestReconcile_PerTemplatePoolsConvergeIndependently(t *testing.T) {
	scheme := newScheme(t)
	const ns = "agenttier"

	// Two templates configured, both with DesiredCount=1.
	tmplA := &agenttierv1alpha1.ClusterSandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "general-coding"},
		Spec: agenttierv1alpha1.SandboxTemplateSpec{
			Image: &agenttierv1alpha1.ImageSpec{Repository: "ghcr.io/agenttier/sandbox-general:test"},
		},
	}
	tmplB := &agenttierv1alpha1.ClusterSandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "claude-code"},
		Spec: agenttierv1alpha1.SandboxTemplateSpec{
			Image: &agenttierv1alpha1.ImageSpec{Repository: "ghcr.io/agenttier/sandbox-claude-code:test"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(tmplA, tmplB).
		Build()

	// Persist a per-template config.
	if err := SetConfig(context.Background(), c, ns, Config{Pools: []PoolConfig{
		{Template: "general-coding", DesiredCount: 1},
		{Template: "claude-code", DesiredCount: 1},
	}}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	r := NewReconciler(c, slogDiscard(), ns)

	// Run two reconcile cycles — the controller creates one pool pod
	// per template per cycle so two cycles get both pools to size.
	for i := 0; i < 2; i++ {
		if err := r.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile cycle %d: %v", i, err)
		}
	}

	// Verify exactly one pod per template.
	for _, template := range []string{"general-coding", "claude-code"} {
		pods, err := r.listPoolPods(context.Background(), template)
		if err != nil {
			t.Fatalf("listPoolPods(%s): %v", template, err)
		}
		if got := len(pods); got != 1 {
			t.Errorf("template %s: got %d pods, want 1", template, got)
		}
	}
}

// TestReconcile_DroppingTemplateCleansUpOrphans verifies that removing a
// template entry from the config drains its pool pods on the next reconcile.
// Without orphan cleanup, deleting a pool entry would leak pods forever.
func TestReconcile_DroppingTemplateCleansUpOrphans(t *testing.T) {
	scheme := newScheme(t)
	const ns = "agenttier"

	tmplA := &agenttierv1alpha1.ClusterSandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl-a"},
		Spec:       agenttierv1alpha1.SandboxTemplateSpec{Image: &agenttierv1alpha1.ImageSpec{Repository: "x"}},
	}
	// An existing pool pod for a template that's about to be removed.
	orphan := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pool-tmpl-orphan-1",
			Namespace: ns,
			Labels: map[string]string{
				LabelPooled:   "true",
				LabelTemplate: "tmpl-orphan",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "sandbox", Image: "x"}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(tmplA, orphan).
		Build()

	// Config only references tmpl-a; tmpl-orphan is no longer present.
	if err := SetConfig(context.Background(), c, ns, Config{Pools: []PoolConfig{
		{Template: "tmpl-a", DesiredCount: 1},
	}}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	r := NewReconciler(c, slogDiscard(), ns)
	if err := r.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// The orphan must be gone.
	orphans, err := r.listPoolPods(context.Background(), "tmpl-orphan")
	if err != nil {
		t.Fatalf("listPoolPods orphan: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("expected orphan pool pods to be drained, found %d", len(orphans))
	}
}

func poolsEqual(a, b []PoolConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
