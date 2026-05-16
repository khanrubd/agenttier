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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
