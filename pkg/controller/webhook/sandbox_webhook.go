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

// Package webhook implements the AgentTier validating + mutating admission
// webhook for Sandbox resources.
//
// It closes the kubectl-bypass: the Router enforces governance + sets
// CreatedBy on POST /sandboxes, but anyone with direct cluster access could
// `kubectl apply` a Sandbox with a forged CreatedBy and skip every governance
// check (which only ran in the Router). This webhook moves both controls to
// admission time, where they apply to ALL writers — Router, kubectl, GitOps,
// in-cluster controllers.
//
// On CREATE it:
//   - overwrites spec.createdBy from the authenticated AdmissionRequest
//     UserInfo (the request body can no longer impersonate another user), and
//   - runs the same governance.Check used by the Router, rejecting
//     over-quota / disallowed-template / disallowed-image creates with a
//     structured message.
//
// On UPDATE it rejects changes to immutable fields (mode, templateRef,
// cloneFromSnapshot) once the sandbox exists.
package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/governance"
)

// SandboxValidator is the admission handler for Sandbox resources. It both
// mutates (stamping CreatedBy) and validates (governance + immutability), so
// it is registered as a MUTATING webhook — the API server applies the patch it
// returns, and a denial still rejects the request.
type SandboxValidator struct {
	client  client.Client
	store   governance.Store
	decoder admission.Decoder
}

// NewSandboxValidator builds the handler. The store is used to resolve the
// effective namespace policy; pass a ConfigMap-backed store (the same one the
// controller + router use).
func NewSandboxValidator(c client.Client, store governance.Store, decoder admission.Decoder) *SandboxValidator {
	return &SandboxValidator{client: c, store: store, decoder: decoder}
}

// Handle implements admission.Handler.
func (v *SandboxValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	switch req.Operation {
	case admissionv1.Create:
		return v.handleCreate(ctx, req)
	case admissionv1.Update:
		return v.handleUpdate(ctx, req)
	default:
		// Delete / Connect — nothing to validate here.
		return admission.Allowed("")
	}
}

func (v *SandboxValidator) handleCreate(ctx context.Context, req admission.Request) admission.Response {
	sandbox := &agenttierv1alpha1.Sandbox{}
	if err := v.decoder.Decode(req, sandbox); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// 1. Authoritative identity: overwrite spec.createdBy from the
	//    authenticated user in the AdmissionRequest. This is the whole point
	//    of the webhook — the request body can claim any createdBy it likes;
	//    we replace it with who the API server says is making the call.
	sandbox.Spec.CreatedBy = &agenttierv1alpha1.UserIdentity{
		Sub:         req.UserInfo.Username, // K8s username (OIDC sub, SA name, etc.)
		Email:       userInfoEmail(req),
		DisplayName: req.UserInfo.Username,
	}

	// 2. Governance: run the same Check the Router runs, against the
	//    namespace-resolved policy. This closes the kubectl bypass.
	if v.store != nil {
		policy, err := governance.Resolve(ctx, v.store, sandbox.Namespace)
		if err == nil && !policy.IsEmpty() {
			existing := &agenttierv1alpha1.SandboxList{}
			if err := v.client.List(ctx, existing, client.InNamespace(sandbox.Namespace)); err != nil {
				return admission.Errored(http.StatusInternalServerError,
					fmt.Errorf("failed to check namespace usage: %w", err))
			}
			usage := governance.CountUsage(existing, sandbox.Spec.CreatedBy.Sub)
			if vio := governance.Check(policy, usage, sandbox); vio.Violated() {
				return admission.Denied(fmt.Sprintf("governance policy violation: %s", vio.Error()))
			}
		}
	}

	// Return the mutated object as a JSON patch.
	marshaled, err := json.Marshal(sandbox)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, err)
	}
	return admission.PatchResponseFromRaw(req.Object.Raw, marshaled)
}

func (v *SandboxValidator) handleUpdate(_ context.Context, req admission.Request) admission.Response {
	newSandbox := &agenttierv1alpha1.Sandbox{}
	if err := v.decoder.Decode(req, newSandbox); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	oldSandbox := &agenttierv1alpha1.Sandbox{}
	if err := v.decoder.DecodeRaw(req.OldObject, oldSandbox); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Immutable fields: once a sandbox exists, these define its identity and
	// storage lineage. Changing them after the fact would orphan the pod /
	// PVC contract or rewrite ownership.
	if oldSandbox.Spec.Mode != "" && newSandbox.Spec.Mode != oldSandbox.Spec.Mode {
		return admission.Denied(fmt.Sprintf("spec.mode is immutable (was %q, got %q)", oldSandbox.Spec.Mode, newSandbox.Spec.Mode))
	}
	if templateName(oldSandbox) != "" && templateName(newSandbox) != templateName(oldSandbox) {
		return admission.Denied(fmt.Sprintf("spec.templateRef is immutable once set (was %q)", templateName(oldSandbox)))
	}
	if oldSandbox.Spec.CloneFromSnapshot != "" && newSandbox.Spec.CloneFromSnapshot != oldSandbox.Spec.CloneFromSnapshot {
		return admission.Denied("spec.cloneFromSnapshot is immutable once set")
	}

	// Prevent re-stamping CreatedBy on update (e.g. to hijack ownership).
	if oldSandbox.Spec.CreatedBy != nil && oldSandbox.Spec.CreatedBy.Sub != "" {
		if newSandbox.Spec.CreatedBy == nil || newSandbox.Spec.CreatedBy.Sub != oldSandbox.Spec.CreatedBy.Sub {
			return admission.Denied("spec.createdBy is immutable")
		}
	}

	return admission.Allowed("")
}

func templateName(s *agenttierv1alpha1.Sandbox) string {
	if s.Spec.TemplateRef == nil {
		return ""
	}
	return s.Spec.TemplateRef.Name
}

// userInfoEmail extracts an email from the AdmissionRequest extra claims when
// the IdP forwards one. Falls back to empty — email is best-effort metadata,
// not an identity key (Sub is the key).
func userInfoEmail(req admission.Request) string {
	if req.UserInfo.Extra == nil {
		return ""
	}
	if vals, ok := req.UserInfo.Extra["email"]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}
