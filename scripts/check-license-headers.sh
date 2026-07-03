#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# Verify that every first-party source file carries the Apache 2.0 license
# header. Third-party and generated code are skipped.
#
# Checked extensions: .go, .py, .ts, .tsx
set -euo pipefail

MISSING=()

while IFS= read -r -d '' file; do
  # Skip generated deepcopy and tools directories.
  case "$file" in
    */zz_generated_*|*/zz_generated.*) continue ;;
    */vendor/*|*/_output/*|*/bin/*) continue ;;
  esac

  # Read the first 20 lines and look for the Apache header or SPDX tag.
  head -20 "$file" | grep -qE 'Licensed under the Apache License, Version 2.0|SPDX-License-Identifier:\s*Apache-2\.0' \
    || MISSING+=("$file")
done < <(find . \
  -path ./vendor -prune -o \
  -path ./.git -prune -o \
  -path ./.venv -prune -o \
  -path ./web-ui/node_modules -prune -o \
  -name 'node_modules' -prune -o \
  -name '.venv' -prune -o \
  -name '__pycache__' -prune -o \
  -name '.mypy_cache' -prune -o \
  -name '.ruff_cache' -prune -o \
  -name '.pytest_cache' -prune -o \
  -name 'dist' -prune -o \
  -name '*.egg-info' -prune -o \
  -path './_output' -prune -o \
  -path './bin' -prune -o \
  -type f \( -name '*.go' -o -name '*.py' -o -name '*.ts' -o -name '*.tsx' \) -print0)

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "The following files are missing an Apache 2.0 license header:"
  printf '  %s\n' "${MISSING[@]}"
  echo ""
  echo "Add the standard header from scripts/boilerplate.go.txt to each file above."
  echo "(For .py files use '# Copyright ...' comments; for .ts/.tsx use '// Copyright ...')"
  exit 1
fi

go_count=$(git ls-files '*.go' 2>/dev/null | wc -l | tr -d ' ')
py_count=$(git ls-files '*.py' 2>/dev/null | wc -l | tr -d ' ')
ts_count=$(git ls-files '*.ts' '*.tsx' 2>/dev/null | wc -l | tr -d ' ')
echo "License headers OK (scanned ${go_count} Go, ${py_count} Python, ${ts_count} TypeScript/TSX files)."
