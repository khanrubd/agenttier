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

package router

// Cluster-level status endpoint for the Web UI's left-nav glance widget.
//
// Returns ready/total node counts plus pod counts so the operator can see
// at a glance whether the autoscaler scaled the cluster correctly. Mirrors
// the warm pool status widget pattern that lives next to it in the nav.
//
// **Why an HTTP endpoint instead of just kubectl?** The Web UI runs on the
// browser, has no kubeconfig, and the operator viewing it doesn't always
// have cluster-admin. The Router has a ClusterRole with node + pod read
// access already (used for warm pool status), so this is the natural
// surface to expose.
//
// **Auth.** Behind the standard /api/v1 auth middleware. Anyone who can
// list sandboxes can also see cluster headcount — same blast radius.

import (
	"context"
	"net/http"

	corev1 "k8s.io/api/core/v1"
)

// ClusterStatus is the wire shape /api/v1/cluster/status returns.
type ClusterStatus struct {
	// Nodes is the total cluster node count (Ready and NotReady combined).
	// We surface both states so a node coming up shows as 4/5 instead of
	// silently dropping until ready.
	Nodes      int `json:"nodes"`
	NodesReady int `json:"nodesReady"`

	// Pods is the count of all running pods cluster-wide. Useful for
	// gauging cluster utilization at a glance.
	Pods int `json:"pods"`

	// SandboxPods is the subset of Pods that are user sandboxes (label
	// `app.kubernetes.io/component: sandbox`). Operators care about this
	// number more than the global one because it tracks user load
	// directly.
	SandboxPods int `json:"sandboxPods"`

	// HeadroomReady is the count of headroom pause Pods currently
	// running. When > 0 it means the chart's optional `headroom`
	// Deployment is enabled and at least one spare-node reservation is
	// active; the UI uses this to know whether to show the autoscaling
	// status block at all.
	HeadroomReady int `json:"headroomReady"`

	// AutoscalerEnabled is true when the chart's optional Cluster
	// Autoscaler Deployment exists and has at least one Ready replica.
	// Used by the UI to decide whether to label the cluster as
	// "autoscaling on" vs "fixed-size".
	AutoscalerEnabled bool `json:"autoscalerEnabled"`
}

// handleGetClusterStatus is the GET /api/v1/cluster/status handler. Cheap:
// two K8s List calls (nodes + pods) plus one ConfigMap-cached label
// selector match. Cached at the apiserver layer so repeated calls are
// near-instant.
func (s *Server) handleGetClusterStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.computeClusterStatus(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to compute cluster status: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, status)
}

// computeClusterStatus is the work the handler delegates to. Pulled out
// so it's testable in isolation against a fake k8s client.
func (s *Server) computeClusterStatus(ctx context.Context) (*ClusterStatus, error) {
	out := &ClusterStatus{}

	// Nodes — cheap; cluster-wide List of Node is cached by the manager.
	nodes := &corev1.NodeList{}
	if err := s.k8sClient.List(ctx, nodes); err != nil {
		return nil, err
	}
	out.Nodes = len(nodes.Items)
	for _, n := range nodes.Items {
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				out.NodesReady++
				break
			}
		}
	}

	// Pods — single cluster-wide List. We loop once and bucket by label
	// to fill SandboxPods + HeadroomReady in the same pass.
	pods := &corev1.PodList{}
	if err := s.k8sClient.List(ctx, pods); err != nil {
		return nil, err
	}
	for _, p := range pods.Items {
		// Only count Running pods toward Pods total. Pending and Failed
		// pods inflate the gauge in ways that don't match the operator's
		// mental model ("how busy is my cluster right now?").
		if p.Status.Phase != corev1.PodRunning {
			continue
		}
		out.Pods++
		switch p.Labels["app.kubernetes.io/component"] {
		case "sandbox":
			out.SandboxPods++
		case "headroom":
			out.HeadroomReady++
		case "cluster-autoscaler":
			out.AutoscalerEnabled = true
		}
	}
	return out, nil
}
