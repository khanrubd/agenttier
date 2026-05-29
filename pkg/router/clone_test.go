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

package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// cloneFixture builds a Server with the snapshot scheme registered so
// VolumeSnapshot writes succeed against the fake client.
func cloneFixture(t *testing.T, objs ...client.Object) (*Server, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		agenttierv1alpha1.AddToScheme,
		snapshotv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatalf("scheme: %v", err)
		}
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()
	s := NewServer(&Config{ListenAddr: ":0"}, c, nil)
	return s, c
}

// runClone invokes the clone handler with claims pre-stamped on the
// context (the dev-mode auth path). The fake bridge isn't exercised — the
// clone handler only touches the API objects, never the in-pod runtime.
func runClone(s *Server, sandboxID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/sandboxes/"+sandboxID+"/clone",
		strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = int64(len(body))
	}
	// Inject an admin claim — same shape the auth middleware would
	// stamp on a successful OIDC verification.
	claims := &Claims{
		Sub:     "u-test",
		Email:   "test@agenttier.local",
		Name:    "Test User",
		IsAdmin: true,
	}
	req = req.WithContext(context.WithValue(req.Context(), ClaimsContextKey, claims))

	// Manually populate the gorilla mux Vars by re-routing through a
	// minimal mux that knows the clone path.
	rec := httptest.NewRecorder()
	r := newCloneRouterForTest(s)
	r.ServeHTTP(rec, req)
	return rec
}

func newCloneRouterForTest(s *Server) http.Handler {
	// Reuse the existing mux registration without authMiddleware, since
	// we manually injected claims above.
	mux := s.router // server's *mux.Router is already populated by NewServer
	return mux
}

func TestClone_HappyPath(t *testing.T) {
	source := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-source", Namespace: "default"},
		Spec: agenttierv1alpha1.SandboxSpec{
			Mode:        agenttierv1alpha1.SandboxModeCode,
			TemplateRef: &agenttierv1alpha1.TemplateReference{Name: "general-coding", Kind: "ClusterSandboxTemplate"},
		},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PVCName: "sbx-source-workspace",
		},
	}
	s, c := cloneFixture(t, source)

	rec := runClone(s, "sbx-source", `{"name":"sbx-clone"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v body=%s", err, rec.Body.String())
	}
	if resp["name"] != "sbx-clone" {
		t.Errorf("name = %v, want sbx-clone", resp["name"])
	}
	if resp["clonedFrom"] != "sbx-source" {
		t.Errorf("clonedFrom = %v, want sbx-source", resp["clonedFrom"])
	}

	// Verify the VolumeSnapshot landed in the cluster.
	snapList := &snapshotv1.VolumeSnapshotList{}
	if err := c.List(context.Background(), snapList, client.InNamespace("default")); err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(snapList.Items) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapList.Items))
	}
	snap := snapList.Items[0]
	if snap.Spec.Source.PersistentVolumeClaimName == nil || *snap.Spec.Source.PersistentVolumeClaimName != "sbx-source-workspace" {
		t.Errorf("snapshot source PVC = %v, want sbx-source-workspace", snap.Spec.Source.PersistentVolumeClaimName)
	}

	// Verify the cloned Sandbox CR landed with CloneFromSnapshot pointing
	// at the snapshot we just created.
	clone := &agenttierv1alpha1.Sandbox{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "sbx-clone", Namespace: "default"}, clone); err != nil {
		t.Fatalf("get clone: %v", err)
	}
	if clone.Spec.CloneFromSnapshot != snap.Name {
		t.Errorf("clone CloneFromSnapshot = %q, want %q", clone.Spec.CloneFromSnapshot, snap.Name)
	}
	if clone.Spec.TemplateRef == nil || clone.Spec.TemplateRef.Name != "general-coding" {
		t.Errorf("clone template not inherited from source: %+v", clone.Spec.TemplateRef)
	}
}

func TestClone_RejectsSourceWithoutPVC(t *testing.T) {
	source := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-source", Namespace: "default"},
		Status:     agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseCreating},
	}
	s, _ := cloneFixture(t, source)

	rec := runClone(s, "sbx-source", `{"name":"sbx-clone"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (no PVC to clone), got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no PVC") {
		t.Errorf("expected error to mention missing PVC, got %s", rec.Body.String())
	}
}

func TestClone_RejectsInvalidName(t *testing.T) {
	source := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: "sbx-source", Namespace: "default"},
		Status: agenttierv1alpha1.SandboxStatus{
			Phase:   agenttierv1alpha1.SandboxPhaseRunning,
			PVCName: "sbx-source-workspace",
		},
	}
	s, _ := cloneFixture(t, source)

	rec := runClone(s, "sbx-source", `{"name":"Bad Name With Spaces"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (invalid name), got %d body=%s", rec.Code, rec.Body.String())
	}
}
