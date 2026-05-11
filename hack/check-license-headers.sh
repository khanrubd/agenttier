#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# Verify that every first-party Go source file carries the Apache 2.0 license
# header. Third-party and generated code are skipped.
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
  -path ./web-ui/node_modules -prune -o \
  -path './**/node_modules' -prune -o \
  -path './**/.venv' -prune -o \
  -path './**/dist' -prune -o \
  -type f -name '*.go' -print0)

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "The following Go files are missing an Apache 2.0 license header:"
  printf '  %s\n' "${MISSING[@]}"
  echo ""
  echo "Add the standard header from hack/boilerplate.go.txt to each file above."
  exit 1
fi

echo "License headers OK (scanned $(git ls-files '*.go' | wc -l | tr -d ' ') Go files)."
