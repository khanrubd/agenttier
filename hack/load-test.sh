#!/usr/bin/env bash
#
# load-test.sh — drive the Router API with `hey` to exercise the per-IP/per-user
# rate limiter and surface the single-replica Router as a saturation point
# (informs the HPA / multi-replica sizing).
#
#   BASE=http://localhost:8080 TOKEN=<api-key> N=1000 C=50 hack/load-test.sh
#
# Reads:
#   BASE   Router base URL (default http://localhost:8080 — port-forward the Router Service)
#   TOKEN  API key (X-API-Key) or leave empty for a --dev-auth install
#   N      total requests (default 1000)
#   C      concurrency (default 50)
#
# Requires `hey` (go install github.com/rakyll/hey@latest). Reports latency
# distribution + status-code breakdown; a burst of 429s confirms the rate
# limiter engaging, and p99 climbing with C confirms the single-Router ceiling.

set -euo pipefail

BASE="${BASE:-http://localhost:8080}"
TOKEN="${TOKEN:-}"
N="${N:-1000}"
C="${C:-50}"

command -v hey >/dev/null || {
  echo "hey not found — install with: go install github.com/rakyll/hey@latest" >&2
  exit 1
}

auth=()
[[ -n "${TOKEN}" ]] && auth=(-H "X-API-Key: ${TOKEN}")

echo "=== GET ${BASE}/api/v1/sandboxes  (N=${N} C=${C}) ==="
hey -n "${N}" -c "${C}" "${auth[@]}" "${BASE}/api/v1/sandboxes"

echo ""
echo "=== GET ${BASE}/api/v1/cluster/status  (N=${N} C=${C}) ==="
hey -n "${N}" -c "${C}" "${auth[@]}" "${BASE}/api/v1/cluster/status"

echo ""
echo "Interpretation:"
echo "  - 429s in the status-code breakdown => the rate limiter is engaging (opt-in; enable it to see them)."
echo "  - p99 latency rising sharply with higher C => the single Router replica is the bottleneck (see the HPA item)."
