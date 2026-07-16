#!/usr/bin/env bash
#
# check-bundle-size.sh — performance regression guard for the Web UI bundle.
#
# Fails if the built Vite JS exceeds the budget documented in
# .kiro/steering/project.md ("Vite bundle stays under ~750 KB minified").
# Run after `npm run build`. CI invokes it in the build job; run it locally
# the same way:
#
#   (cd web-ui && npm run build) && scripts/check-bundle-size.sh
#
# Override the directory or limit via args / env:
#   scripts/check-bundle-size.sh web-ui/dist
#   BUNDLE_LIMIT_KB=800 scripts/check-bundle-size.sh

set -euo pipefail

DIST="${1:-web-ui/dist}"
LIMIT_KB="${BUNDLE_LIMIT_KB:-750}"

if [[ ! -d "${DIST}/assets" ]]; then
  echo "bundle gate: ${DIST}/assets not found — run 'npm run build' first" >&2
  exit 1
fi

# Exact byte sum of all emitted JS (portable: cat | wc -c, no per-file block
# rounding). KiB = bytes / 1024.
total_bytes=$(find "${DIST}/assets" -name '*.js' -type f -exec cat {} + | wc -c | tr -d ' ')
total_kb=$(( total_bytes / 1024 ))

echo "bundle gate: web-ui JS = ${total_kb} KB (budget ${LIMIT_KB} KB)"
if (( total_kb > LIMIT_KB )); then
  echo "::error::Web UI JS bundle ${total_kb}KB exceeds the ${LIMIT_KB}KB performance budget." >&2
  echo "Split a heavy feature behind a dynamic import() or raise BUNDLE_LIMIT_KB deliberately." >&2
  exit 1
fi
echo "bundle gate: OK"
