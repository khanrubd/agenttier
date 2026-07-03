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

package crds

import "testing"

// TestLoad_DecodesAllBundledCRDs guards that the embedded manifests decode and
// cover the three AgentTier CRDs. If `make manifests` ever fails to sync a new
// CRD into pkg/crds, this catches it.
func TestLoad_DecodesAllBundledCRDs(t *testing.T) {
	defs, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := map[string]bool{
		"sandboxes.agenttier.io":               false,
		"sandboxtemplates.agenttier.io":        false,
		"clustersandboxtemplates.agenttier.io": false,
	}
	for _, crd := range defs {
		if crd.Kind != "CustomResourceDefinition" {
			t.Errorf("decoded a non-CRD object: kind=%q name=%q", crd.Kind, crd.Name)
		}
		if crd.Spec.Group != "agenttier.io" {
			t.Errorf("CRD %s has group %q, want agenttier.io", crd.Name, crd.Spec.Group)
		}
		if _, ok := want[crd.Name]; ok {
			want[crd.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected CRD %q not present in the embedded set", name)
		}
	}
}
