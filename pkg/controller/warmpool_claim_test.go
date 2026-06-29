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
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrl "sigs.k8s.io/controller-runtime/pkg/reconcile"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/controller/warmpool"
)

// TestWarmPoolClaim_AdoptsPodAndPVC is the regression guard for the bug where
// a warm-pool-claimed Pod and PVC got no ownerReference to the Sandbox —
// breaking watch-driven self-healing and leaking the Pod + EBS PVC on a
// status-update failure. After a claim, both the Pod and the PVC must carry a
// controller ownerReference to the claiming Sandbox.
func TestWarmPoolClaim_AdoptsPodAndPVC(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}

	const ns = "default"
	const tmpl = "general-coding"
	const poolPod = "pool-general-1"
	const poolPVC = "pool-general-1-pvc"

	template := &agenttierv1alpha1.ClusterSandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: tmpl},
		Spec:       agenttierv1alpha1.SandboxTemplateSpec{},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      poolPod,
			Namespace: ns,
			Labels: map[string]string{
				warmpool.LabelPooled:   "true",
				warmpool.LabelTemplate: tmpl,
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "busybox"}},
			Volumes: []corev1.Volume{{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: poolPVC},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      poolPVC,
			Namespace: ns,
			Labels: map[string]string{
				warmpool.LabelPooled:   "true",
				warmpool.LabelTemplate: tmpl,
			},
		},
	}

	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sb-warm",
			Namespace:  ns,
			Finalizers: []string{FinalizerName}, // pre-set so reconcile runs the claim path in one pass
		},
		Spec: agenttierv1alpha1.SandboxSpec{
			Mode:        agenttierv1alpha1.SandboxModeCode,
			TemplateRef: &agenttierv1alpha1.TemplateReference{Name: tmpl, Kind: "ClusterSandboxTemplate"},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(template, pod, pvc, sandbox).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()

	r := &SandboxReconciler{
		Client:               c,
		Scheme:               scheme,
		Recorder:             record.NewFakeRecorder(20),
		PoolSandboxNamespace: ns,
		InstallNamespace:     "agenttier",
	}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "sb-warm", Namespace: ns},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// The Sandbox should have claimed our pool pod.
	got := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-warm", Namespace: ns}, got); err != nil {
		t.Fatalf("get sandbox: %v", err)
	}
	if got.Status.PodName != poolPod {
		t.Fatalf("expected sandbox to claim %s, got PodName=%q phase=%q", poolPod, got.Status.PodName, got.Status.Phase)
	}

	assertOwnedBySandbox := func(t *testing.T, refs []metav1.OwnerReference, kind string) {
		t.Helper()
		for _, ref := range refs {
			if ref.Kind == "Sandbox" && ref.Name == "sb-warm" {
				if ref.Controller == nil || !*ref.Controller {
					t.Errorf("%s: Sandbox owner reference is not the controller", kind)
				}
				return
			}
		}
		t.Errorf("%s: REGRESSION — no ownerReference to the claiming Sandbox (refs=%+v)", kind, refs)
	}

	claimedPod := &corev1.Pod{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: poolPod, Namespace: ns}, claimedPod); err != nil {
		t.Fatalf("get claimed pod: %v", err)
	}
	assertOwnedBySandbox(t, claimedPod.OwnerReferences, "claimed pod")

	claimedPVC := &corev1.PersistentVolumeClaim{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: poolPVC, Namespace: ns}, claimedPVC); err != nil {
		t.Fatalf("get claimed pvc: %v", err)
	}
	assertOwnedBySandbox(t, claimedPVC.OwnerReferences, "claimed pvc")

	// The claimed PVC must no longer carry pool labels — otherwise a reaper
	// that deletes pool PVCs by label would destroy a running sandbox's disk.
	if _, ok := claimedPVC.Labels[warmpool.LabelPooled]; ok {
		t.Errorf("claimed PVC still has %s label (data-loss landmine)", warmpool.LabelPooled)
	}
	if _, ok := claimedPVC.Labels[warmpool.LabelTemplate]; ok {
		t.Errorf("claimed PVC still has %s label", warmpool.LabelTemplate)
	}
}
