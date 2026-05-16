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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TestHandleInfrastructureFailure_RestartLimit verifies that the infrastructure
// restart counter and the Error-phase enforcer (reconcileError) agree on the
// boundary. Previously handleInfrastructureFailure used `>` (allowing
// MaxRestartCount + 1 attempts) while reconcileError used `>=` (refusing
// at MaxRestartCount). The fix standardizes on `>=` in both places so a
// sandbox cannot squeeze in an extra restart past the documented cap.
//
// Boundary cases tested:
//   - count = MaxRestartCount-1 → next attempt allowed (transitions to Creating)
//   - count = MaxRestartCount-1, ++ to MaxRestartCount → terminal Error
//   - count starts at MaxRestartCount → already terminal Error
func TestHandleInfrastructureFailure_RestartLimit(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}

	cases := []struct {
		name             string
		startCount       int
		wantPhase        agenttierv1alpha1.SandboxPhase
		wantRestartCount int
	}{
		{
			name:             "below limit promotes to one more restart",
			startCount:       MaxRestartCount - 2, // 3 → ++ → 4, still below 5
			wantPhase:        agenttierv1alpha1.SandboxPhaseCreating,
			wantRestartCount: MaxRestartCount - 1,
		},
		{
			name:             "incrementing to limit exhausts retries",
			startCount:       MaxRestartCount - 1, // 4 → ++ → 5, hits >= limit
			wantPhase:        agenttierv1alpha1.SandboxPhaseError,
			wantRestartCount: MaxRestartCount,
		},
		{
			name:             "already at limit stays terminal",
			startCount:       MaxRestartCount, // 5 → ++ → 6, well past limit
			wantPhase:        agenttierv1alpha1.SandboxPhaseError,
			wantRestartCount: MaxRestartCount + 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sandbox := &agenttierv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sb-restart",
					Namespace: "default",
				},
				Status: agenttierv1alpha1.SandboxStatus{
					Phase:        agenttierv1alpha1.SandboxPhaseRunning,
					RestartCount: tc.startCount,
				},
			}

			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(sandbox).
				WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
				Build()

			r := &SandboxReconciler{
				Client:   c,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			if _, err := r.handleInfrastructureFailure(context.Background(), sandbox, "fake failure"); err != nil {
				t.Fatalf("handleInfrastructureFailure: %v", err)
			}

			refreshed := &agenttierv1alpha1.Sandbox{}
			if err := c.Get(context.Background(), client.ObjectKey{Name: "sb-restart", Namespace: "default"}, refreshed); err != nil {
				if errors.IsNotFound(err) {
					t.Fatalf("sandbox vanished")
				}
				t.Fatalf("get sandbox: %v", err)
			}

			if refreshed.Status.Phase != tc.wantPhase {
				t.Errorf("phase = %s, want %s", refreshed.Status.Phase, tc.wantPhase)
			}
			if refreshed.Status.RestartCount != tc.wantRestartCount {
				t.Errorf("restartCount = %d, want %d", refreshed.Status.RestartCount, tc.wantRestartCount)
			}
		})
	}
}

// avoid unused imports when only some symbols are used above
var (
	_ = corev1.PodFailed
	_ = errors.IsNotFound
)
