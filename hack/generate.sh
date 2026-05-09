#!/usr/bin/env bash

# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

# Generate deepcopy functions and CRD manifests.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "Generating deepcopy functions..."
controller-gen object:headerFile="${ROOT_DIR}/hack/boilerplate.go.txt" paths="${ROOT_DIR}/api/..."

echo "Generating CRD manifests..."
controller-gen crd:generateEmbeddedObjectMeta=true \
  rbac:roleName=agenttier-controller \
  webhook \
  paths="${ROOT_DIR}/api/..." \
  output:crd:artifacts:config="${ROOT_DIR}/config/crd" \
  output:rbac:artifacts:config="${ROOT_DIR}/config/rbac"

echo "Done."
