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

package webhook

import (
	"context"
	"encoding/json"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/governance"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := agenttierv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("scheme: %v", err)
	}
	return s
}

func newValidator(t *testing.T, store governance.Store, objs ...client.Object) *SandboxValidator {
	t.Helper()
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return NewSandboxValidator(c, store, admission.NewDecoder(scheme))
}

func createRequest(t *testing.T, sb *agenttierv1alpha1.Sandbox, username, email string) admission.Request {
	t.Helper()
	raw, _ := json.Marshal(sb)
	req := admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Create,
		Object:    runtime.RawExtension{Raw: raw},
		UserInfo:  authenticationv1.UserInfo{Username: username},
	}}
	if email != "" {
		req.UserInfo.Extra = map[string]authenticationv1.ExtraValue{
			"email": {email},
		}
	}
	return req
}

func sandbox(name, ns string, mut func(*agenttierv1alpha1.Sandbox)) *agenttierv1alpha1.Sandbox {
	sb := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: agenttierv1alpha1.SandboxSpec{
			TemplateRef: &agenttierv1alpha1.TemplateReference{Name: "general-coding", Kind: "ClusterSandboxTemplate"},
		},
	}
	if mut != nil {
		mut(sb)
	}
	return sb
}

// TestCreate_OverwritesForgedCreatedBy is the core security assertion: a
// request body that claims createdBy = "victim" must come back stamped with
// the AUTHENTICATED username, not the forged value.
func TestCreate_OverwritesForgedCreatedBy(t *testing.T) {
	v := newValidator(t, nil)
	sb := sandbox("sb1", "default", func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.CreatedBy = &agenttierv1alpha1.UserIdentity{Sub: "victim@evil", Email: "victim@evil"}
	})
	resp := v.Handle(context.Background(), createRequest(t, sb, "alice@corp", "alice@corp"))

	if !resp.Allowed {
		t.Fatalf("expected allowed (with patch), got denied: %v", resp.Result)
	}
	// Apply the returned patch and confirm createdBy.sub == authenticated user.
	patched := applyPatch(t, sb, resp)
	if patched.Spec.CreatedBy == nil || patched.Spec.CreatedBy.Sub != "alice@corp" {
		t.Fatalf("createdBy.sub = %v, want alice@corp (forged value must be overwritten)",
			patched.Spec.CreatedBy)
	}
}

func TestCreate_GovernanceDenied(t *testing.T) {
	// Policy allows only "claude-code-bedrock"; the sandbox uses
	// "general-coding" → must be denied.
	store := &fakeStore{policies: map[string]governance.Policy{
		"": {AllowedTemplates: []string{"claude-code-bedrock"}},
	}}
	v := newValidator(t, store)
	sb := sandbox("sb1", "default", nil)

	resp := v.Handle(context.Background(), createRequest(t, sb, "alice@corp", ""))
	if resp.Allowed {
		t.Fatal("expected denial for a disallowed template")
	}
}

func TestCreate_GovernanceAllowed(t *testing.T) {
	store := &fakeStore{policies: map[string]governance.Policy{
		"": {AllowedTemplates: []string{"general-coding"}},
	}}
	v := newValidator(t, store)
	sb := sandbox("sb1", "default", nil)

	resp := v.Handle(context.Background(), createRequest(t, sb, "alice@corp", ""))
	if !resp.Allowed {
		t.Fatalf("expected allow for permitted template, got: %v", resp.Result)
	}
}

func TestUpdate_RejectsModeChange(t *testing.T) {
	v := newValidator(t, nil)
	oldSB := sandbox("sb1", "default", func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Mode = agenttierv1alpha1.SandboxModeCode
	})
	newSB := sandbox("sb1", "default", func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Mode = agenttierv1alpha1.SandboxModeAgent
	})
	resp := v.Handle(context.Background(), updateRequest(t, newSB, oldSB))
	if resp.Allowed {
		t.Fatal("expected denial for mode change")
	}
}

func TestUpdate_RejectsCreatedByChange(t *testing.T) {
	v := newValidator(t, nil)
	oldSB := sandbox("sb1", "default", func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.CreatedBy = &agenttierv1alpha1.UserIdentity{Sub: "alice@corp"}
	})
	newSB := sandbox("sb1", "default", func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.CreatedBy = &agenttierv1alpha1.UserIdentity{Sub: "attacker@evil"}
	})
	resp := v.Handle(context.Background(), updateRequest(t, newSB, oldSB))
	if resp.Allowed {
		t.Fatal("expected denial for createdBy hijack on update")
	}
}

func TestUpdate_AllowsBenignChange(t *testing.T) {
	v := newValidator(t, nil)
	oldSB := sandbox("sb1", "default", func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Mode = agenttierv1alpha1.SandboxModeCode
		s.Spec.CreatedBy = &agenttierv1alpha1.UserIdentity{Sub: "alice@corp"}
	})
	newSB := sandbox("sb1", "default", func(s *agenttierv1alpha1.Sandbox) {
		s.Spec.Mode = agenttierv1alpha1.SandboxModeCode
		s.Spec.CreatedBy = &agenttierv1alpha1.UserIdentity{Sub: "alice@corp"}
		// change something mutable
		s.Spec.Storage = &agenttierv1alpha1.StorageSpec{MountPath: "/data"}
	})
	resp := v.Handle(context.Background(), updateRequest(t, newSB, oldSB))
	if !resp.Allowed {
		t.Fatalf("expected allow for a benign update, got: %v", resp.Result)
	}
}

// --- helpers ---

func updateRequest(t *testing.T, newSB, oldSB *agenttierv1alpha1.Sandbox) admission.Request {
	t.Helper()
	nraw, _ := json.Marshal(newSB)
	oraw, _ := json.Marshal(oldSB)
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: admissionv1.Update,
		Object:    runtime.RawExtension{Raw: nraw},
		OldObject: runtime.RawExtension{Raw: oraw},
		UserInfo:  authenticationv1.UserInfo{Username: "alice@corp"},
	}}
}

func applyPatch(t *testing.T, original *agenttierv1alpha1.Sandbox, resp admission.Response) *agenttierv1alpha1.Sandbox {
	t.Helper()
	// The mutating response patches createdBy; rather than run a full JSON
	// patch library, re-decode the patch's intent by reconstructing from the
	// patches. Simplest faithful approach: the handler marshals the whole
	// mutated object, so PatchResponseFromRaw produces replace ops we can
	// apply with a tiny JSON-patch.
	orig, _ := json.Marshal(original)
	patched := applyJSONPatch(t, orig, resp)
	out := &agenttierv1alpha1.Sandbox{}
	if err := json.Unmarshal(patched, out); err != nil {
		t.Fatalf("unmarshal patched: %v", err)
	}
	return out
}
