/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package warmpool

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Per the steering rule (no per-sandbox or per-user IDs in label values):
// these counters are unlabeled. Aggregate signal is enough for an operator
// to tell "claims are racing" from "claims aren't racing." Per-pod debug
// belongs in structured logs.

// ClaimConflictsTotal counts Update conflicts during Claim. A non-zero
// value indicates concurrent claimers competed for the same pool pod;
// the loser of each race walks to the next pod or re-lists. Healthy
// pools see this stay near zero; sustained growth means the pool is
// undersized for its concurrent-claim rate.
var ClaimConflictsTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "agenttier_warmpool_claim_conflicts_total",
	Help: "Number of optimistic-concurrency conflicts hit while claiming a warm pool pod. Sustained growth indicates an undersized pool relative to claim concurrency.",
})
