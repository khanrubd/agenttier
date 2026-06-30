#!/usr/bin/env bash
#
# perf-smoke.sh — measure sandbox cold/warm start latency against a live
# cluster (kind or the e2e cluster) and report p50/p99 time-to-Running.
#
# It creates COUNT sandboxes from a template, polls each to phase=Running,
# records the wall-clock duration, then prints p50/p99/max and cleans up.
# Run it twice to compare cold vs. warm: once with the template's warm pool
# at 0, once with it pre-warmed.
#
#   COUNT=10 NS=agenttier TEMPLATE=general-coding hack/perf-smoke.sh
#
# Backs the steering-doc performance budget (cold ≤10s, warm ≤1s) with
# reproducible numbers instead of folklore. Requires kubectl + a reachable
# cluster with the AgentTier CRDs installed.

set -euo pipefail

COUNT="${COUNT:-10}"
NS="${NS:-agenttier}"
TEMPLATE="${TEMPLATE:-general-coding}"
TIMEOUT="${TIMEOUT:-120}" # seconds to wait per sandbox before giving up
PREFIX="perf-$(date +%s)"

command -v kubectl >/dev/null || { echo "kubectl not found" >&2; exit 1; }

cleanup() {
  echo "cleaning up ${COUNT} perf sandboxes…"
  for i in $(seq 1 "${COUNT}"); do
    kubectl delete sandbox "${PREFIX}-${i}" -n "${NS}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

durations=()
for i in $(seq 1 "${COUNT}"); do
  name="${PREFIX}-${i}"
  start=$(date +%s.%N)
  cat <<EOF | kubectl apply -f - >/dev/null
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata:
  name: ${name}
  namespace: ${NS}
spec:
  templateRef:
    name: ${TEMPLATE}
    kind: ClusterSandboxTemplate
EOF

  # Poll to Running.
  deadline=$(( $(date +%s) + TIMEOUT ))
  phase=""
  while [[ $(date +%s) -lt ${deadline} ]]; do
    phase=$(kubectl get sandbox "${name}" -n "${NS}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    [[ "${phase}" == "Running" ]] && break
    [[ "${phase}" == "Error" ]] && { echo "  ${name} -> Error" >&2; break; }
    sleep 0.2
  done
  end=$(date +%s.%N)
  if [[ "${phase}" == "Running" ]]; then
    dur=$(awk "BEGIN{printf \"%.3f\", ${end}-${start}}")
    durations+=("${dur}")
    echo "  ${name}: ${dur}s"
  else
    echo "  ${name}: did not reach Running within ${TIMEOUT}s (phase=${phase:-none})" >&2
  fi
done

n=${#durations[@]}
if (( n == 0 )); then
  echo "no sandboxes reached Running — nothing to report" >&2
  exit 1
fi

# Sort and compute percentiles.
sorted=$(printf '%s\n' "${durations[@]}" | sort -n)
pct() { # pct <p> — p in 0..100
  local p="$1"
  echo "${sorted}" | awk -v p="${p}" -v n="${n}" '
    { a[NR]=$1 }
    END { idx=int((p/100.0)*(n-1))+1; if (idx<1) idx=1; if (idx>n) idx=n; printf "%.3f", a[idx] }'
}

echo ""
echo "=== sandbox start latency (template=${TEMPLATE}, n=${n}/${COUNT}) ==="
echo "p50: $(pct 50)s"
echo "p99: $(pct 99)s"
echo "max: $(echo "${sorted}" | tail -1)s"
echo "(warm pool sub-second target ≤1s; cold-start target ≤10s — see docs/docs/performance.md)"
