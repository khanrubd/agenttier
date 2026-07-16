#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# scripts/quickstart.sh — compatibility shim for deploy.sh.
#
# This script used to be the main deploy entrypoint. It is now a thin shim
# that delegates to ./deploy.sh, the canonical single-entrypoint (D10).
#
# Usage:
#   ./scripts/quickstart.sh            → equivalent to ./deploy.sh --target=eks
#   ./scripts/quickstart.sh destroy    → equivalent to ./deploy.sh --target=eks --teardown
#
# Use ./deploy.sh directly for the full API (--target=local, --help, etc.).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

if [[ "${1:-}" == "destroy" ]]; then
  exec "${REPO_ROOT}/deploy.sh" --target=eks --teardown
else
  exec "${REPO_ROOT}/deploy.sh" --target=eks "$@"
fi
