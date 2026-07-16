#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# scripts/lib/version.sh — canonical image-tag derivation.
#
# Usage (sourcing): source scripts/lib/version.sh
#   Sets and exports AGENTTIER_IMAGE_TAG when not already set.
#
# Usage (direct): bash scripts/lib/version.sh
#   Prints the derived tag and exits 0.
#
# Rules (in priority order):
#   1. If AGENTTIER_IMAGE_TAG is already non-empty in the environment, use it.
#   2. If a VERSION file exists at the repo root AND the working tree is clean,
#      use "v<contents of VERSION>" (strips leading/trailing whitespace).
#   3. Otherwise use "sha-<short-git-hash>" (7 hex chars).
#   4. If the working tree has uncommitted changes, append "-dirty" to the tag.
#   Never produce "latest".
set -euo pipefail

_version_sh_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

_at_derive_tag() {
  local base

  # Rule 1: caller-supplied override
  if [[ -n "${AGENTTIER_IMAGE_TAG:-}" ]]; then
    echo "${AGENTTIER_IMAGE_TAG}"
    return
  fi

  # Detect dirty tree (untracked files intentionally excluded — only tracked
  # changes matter for reproducibility).
  local dirty=""
  if ! git -C "${_version_sh_root}" diff --quiet HEAD 2>/dev/null; then
    dirty="-dirty"
  fi

  # Rule 2: VERSION file + clean tree
  local version_file="${_version_sh_root}/VERSION"
  if [[ -f "${version_file}" && -z "${dirty}" ]]; then
    base=$(tr -d '[:space:]' < "${version_file}")
    # Ensure it starts with 'v'.
    if [[ "${base}" != v* ]]; then
      base="v${base}"
    fi
    echo "${base}"
    return
  fi

  # Rule 3: git SHA
  local sha
  sha=$(git -C "${_version_sh_root}" rev-parse --short HEAD 2>/dev/null || echo "unknown")
  base="sha-${sha}"

  echo "${base}${dirty}"
}

# Set AGENTTIER_IMAGE_TAG if not already exported.
AGENTTIER_IMAGE_TAG="$(_at_derive_tag)"
export AGENTTIER_IMAGE_TAG

# When executed directly (not sourced), print the tag and exit.
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
  echo "${AGENTTIER_IMAGE_TAG}"
fi
