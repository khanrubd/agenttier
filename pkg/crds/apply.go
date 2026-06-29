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

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

// Load decodes every embedded CRD manifest into typed objects. Each file holds
// a single CustomResourceDefinition, but multi-document files (separated by
// "---") are tolerated.
func Load() ([]*apiextensionsv1.CustomResourceDefinition, error) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("read embedded CRD dir: %w", err)
	}
	var out []*apiextensionsv1.CustomResourceDefinition
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := FS.ReadFile(e.Name())
		if err != nil {
			return nil, fmt.Errorf("read embedded CRD %s: %w", e.Name(), err)
		}
		for _, doc := range bytes.Split(raw, []byte("\n---")) {
			if len(bytes.TrimSpace(doc)) == 0 {
				continue
			}
			crd := &apiextensionsv1.CustomResourceDefinition{}
			if err := yaml.Unmarshal(doc, crd); err != nil {
				return nil, fmt.Errorf("decode CRD in %s: %w", e.Name(), err)
			}
			if crd.Name == "" || crd.Kind != "CustomResourceDefinition" {
				continue // skip empty trailers / non-CRD docs
			}
			out = append(out, crd)
		}
	}
	return out, nil
}

// Apply create-or-updates every embedded CRD against the cluster. It is
// idempotent and safe to run on every controller start: a missing CRD is
// created, an existing one is updated in place (preserving its
// resourceVersion). Intended to run before the manager starts so the Sandbox
// informer always finds an up-to-date schema.
func Apply(ctx context.Context, cfg *rest.Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	cs, err := apiextensionsclient.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("build apiextensions client: %w", err)
	}
	defs, err := Load()
	if err != nil {
		return err
	}
	client := cs.ApiextensionsV1().CustomResourceDefinitions()
	for _, crd := range defs {
		existing, getErr := client.Get(ctx, crd.Name, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(getErr):
			if _, err := client.Create(ctx, crd, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("create CRD %s: %w", crd.Name, err)
			}
			logger.Info("installed CRD", "name", crd.Name)
		case getErr != nil:
			return fmt.Errorf("get CRD %s: %w", crd.Name, getErr)
		default:
			// Carry the live resourceVersion so the update is accepted.
			crd.ResourceVersion = existing.ResourceVersion
			if _, err := client.Update(ctx, crd, metav1.UpdateOptions{}); err != nil {
				return fmt.Errorf("update CRD %s: %w", crd.Name, err)
			}
			logger.Info("updated CRD", "name", crd.Name)
		}
	}
	return nil
}
