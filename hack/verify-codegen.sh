#!/usr/bin/env bash

# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

# Verify that generated code is up to date.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Run generation
"${SCRIPT_DIR}/generate.sh"

# Check for uncommitted changes
if [ -n "$(git status --porcelain api/ config/)" ]; then
  echo "ERROR: Generated files are out of date."
  echo "Run 'make generate manifests' and commit the changes."
  echo ""
  echo "Changed files:"
  git status --porcelain api/ config/
  echo ""
  git diff api/ config/
  exit 1
fi

echo "Generated code is up to date."
