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

// Package crds embeds the generated CustomResourceDefinition manifests so the
// controller can apply them to the cluster on startup. This makes CRDs track
// the running controller version automatically — Helm only installs CRDs on
// `helm install` and never upgrades them on `helm upgrade`, so without this a
// release that adds a CRD field left the field unusable until an operator ran
// `kubectl apply -f config/crd/` by hand.
//
// The YAMLs here are copies of config/crd/*.yaml (the source of truth that
// `make manifests` generates). The `make manifests` target re-syncs them, and
// `make verify-codegen` fails if they drift, so the copies never go stale.
package crds

import "embed"

// FS holds the embedded CRD manifests (one CustomResourceDefinition per file).
//
//go:embed *.yaml
var FS embed.FS
