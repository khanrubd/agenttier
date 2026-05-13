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

package agent

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Per the steering file: never put per-sandbox or per-user IDs into label
// values; bucket and aggregate instead. We tag by template name (low
// cardinality, useful) and outcome but never by sandboxId / actor.

var (
	invokeRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agenttier_invoke_requests_total",
		Help: "Number of /invoke calls received, partitioned by template and outcome.",
	}, []string{"template", "outcome"})

	invokeDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "agenttier_invoke_duration_seconds",
		Help:    "Wall-clock duration of completed /invoke calls.",
		Buckets: prometheus.ExponentialBuckets(0.5, 2, 12), // 0.5s..~34min
	}, []string{"template", "outcome"})

	invokeThrottledTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "agenttier_invoke_throttled_total",
		Help: "Number of /invoke calls rejected because the per-sandbox concurrency cap was reached.",
	})

	configureRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "agenttier_configure_requests_total",
		Help: "Number of /configure calls received, partitioned by template and outcome.",
	}, []string{"template", "outcome"})

	configureDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "agenttier_configure_duration_seconds",
		Help:    "Wall-clock duration of completed /configure calls.",
		Buckets: prometheus.ExponentialBuckets(0.1, 2, 12), // 100ms..~7min
	}, []string{"template", "outcome"})
)

// templateLabel returns a low-cardinality label for a sandbox's template.
// Empty when the sandbox was created without a template ref (rare but
// possible) so that case still aggregates cleanly.
func templateLabel(resolvedTemplate string) string {
	if resolvedTemplate == "" {
		return "_none"
	}
	return resolvedTemplate
}
