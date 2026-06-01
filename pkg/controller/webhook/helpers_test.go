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

	jsonpatch "github.com/evanphx/json-patch/v5"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/agenttier/agenttier/pkg/governance"
)

// applyJSONPatch applies the JSONPatch from a mutating admission response to
// the original object bytes and returns the result.
func applyJSONPatch(t *testing.T, original []byte, resp admission.Response) []byte {
	t.Helper()
	if len(resp.Patches) == 0 {
		return original
	}
	patchBytes, err := json.Marshal(resp.Patches)
	if err != nil {
		t.Fatalf("marshal patches: %v", err)
	}
	patch, err := jsonpatch.DecodePatch(patchBytes)
	if err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	out, err := patch.Apply(original)
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	return out
}

// fakeStore is an in-memory governance.Store for webhook tests.
type fakeStore struct {
	policies map[string]governance.Policy
}

func (f *fakeStore) GetPolicy(_ context.Context, namespace string) (governance.Policy, error) {
	return f.policies[namespace], nil
}

func (f *fakeStore) SetPolicy(_ context.Context, namespace string, p governance.Policy) error {
	if f.policies == nil {
		f.policies = map[string]governance.Policy{}
	}
	f.policies[namespace] = p
	return nil
}

func (f *fakeStore) DeletePolicy(_ context.Context, namespace string) error {
	delete(f.policies, namespace)
	return nil
}

func (f *fakeStore) ListPolicies(_ context.Context) ([]governance.ScopedPolicy, error) {
	out := make([]governance.ScopedPolicy, 0, len(f.policies))
	for scope, p := range f.policies {
		out = append(out, governance.ScopedPolicy{Scope: scope, Policy: p})
	}
	return out, nil
}
