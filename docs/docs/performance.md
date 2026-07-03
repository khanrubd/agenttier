# Performance and regression guards

AgentTier commits to a small set of performance budgets. This page states them,
shows how to measure them, and describes the guard that keeps the Web UI bundle
from regressing silently.

## Budgets

| Metric | Budget | Why |
| --- | --- | --- |
| Cold sandbox start (no warm pool) | ≤ 10 s | A `kubectl apply` / API create to a usable `Running` sandbox. |
| Warm sandbox start (warm pool hit) | ≤ 1 s | A pre-provisioned pool pod is claimed in place. |
| Web UI JS bundle | ≤ 750 KB minified | Keeps first paint fast; enforced in CI. |
| Reconciler queue depth (steady state) | ~0 | A queue sitting > 0 for more than a few seconds is a controller bug, not load. |

## Bundle-size gate (enforced in CI)

The CI `build` job runs `scripts/check-bundle-size.sh` after `npm run build` and
**fails the build** if the emitted Vite JS exceeds 750 KB. Run it locally the
same way:

```bash
(cd web-ui && npm ci && npm run build) && scripts/check-bundle-size.sh
```

Override the limit deliberately with `BUNDLE_LIMIT_KB=…` (and write down why).
If you blow the budget, split the heavy feature behind a dynamic `import()`
rather than raising the ceiling.

## Cold vs. warm start (`scripts/perf-smoke.sh`)

Measures p50/p99 time-to-`Running` against a live cluster (kind or the e2e
cluster). Run it twice to compare:

```bash
# Cold: ensure the template's warm pool is at 0, then:
COUNT=10 NS=agenttier TEMPLATE=general-coding scripts/perf-smoke.sh

# Warm: pre-warm the pool (Web UI Settings → warm pools, or the warmpool API),
# wait for the pool to report Ready, then run the same command.
```

It prints p50/p99/max and cleans up the sandboxes it created. The warm number
should land sub-second when a pool pod is claimed; the cold number is dominated
by image pull + pod scheduling and should stay within the 10 s budget on a
warm-image node.

## Load / saturation (`scripts/load-test.sh`)

Drives the Router API with [`hey`](https://github.com/rakyll/hey) to exercise
the opt-in rate limiter and find where a single Router replica saturates:

```bash
kubectl -n agenttier port-forward svc/agenttier-router 8080:8080 &
BASE=http://localhost:8080 TOKEN=<api-key> N=1000 C=50 scripts/load-test.sh
```

A burst of `429`s confirms the rate limiter engaging (when enabled); p99 latency
climbing sharply as concurrency rises is the signal that the single Router
replica is the bottleneck — the data point that justifies and sizes a
multi-replica / HPA rollout.

## Reference numbers

These are indicative measurements on the `agentloft-e2e` cluster (2× t3.large,
EKS 1.30); reproduce with the scripts above on your own cluster.

| Scenario | p50 | p99 |
| --- | --- | --- |
| Warm-pool claim (`general-coding`) | ~0.8 s | ~1.0 s |
| Cold start, image already on node | ~6 s | ~9 s |
| Cold start, image pull required | dominated by pull | dominated by pull |

Numbers are tracked over time so a regression (a heavy init step, a bundle
blow-up) shows up against this baseline rather than going unnoticed.
