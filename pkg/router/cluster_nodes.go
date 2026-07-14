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

// Per-node cluster capacity for the Metrics page's "Cluster capacity" card.
//
// Where the left-nav glance widget (/cluster/status) answers "how many nodes
// and pods?", this answers "how saturated is the node fleet, and what is it?".
// It returns per-node allocatable CPU/memory, the sum of scheduled Pod
// requests on each node, a requests-based saturation percentage, and — when
// the cloud provider labels them — the instance type and node group so an
// operator can see e.g. two t3.large nodes in the `agenttier-e2e` group.
//
// Admin-only: node fleet detail is operator information, gated behind
// requireAdmin like the audit/analytics/headroom endpoints.

import (
	"context"
	"math"
	"net/http"

	corev1 "k8s.io/api/core/v1"
)

// NodeResources is a CPU (millicores) + memory (bytes) pair.
type NodeResources struct {
	CPUMillis int64 `json:"cpuMillis"`
	MemBytes  int64 `json:"memBytes"`
}

// NodeCapacity is the per-node view returned by /cluster/nodes.
type NodeCapacity struct {
	Name         string        `json:"name"`
	Ready        bool          `json:"ready"`
	InstanceType string        `json:"instanceType,omitempty"`
	NodeGroup    string        `json:"nodeGroup,omitempty"`
	Allocatable  NodeResources `json:"allocatable"`
	Requests     NodeResources `json:"requests"`
}

// NodeCapacitySummary aggregates the fleet for the card header.
type NodeCapacitySummary struct {
	Ready            int           `json:"ready"`
	Total            int           `json:"total"`
	CPUSaturationPct float64       `json:"cpuSaturationPct"`
	MemSaturationPct float64       `json:"memSaturationPct"`
	Allocatable      NodeResources `json:"allocatable"`
	Requests         NodeResources `json:"requests"`
}

// NodeCapacityResponse is the wire shape of GET /api/v1/cluster/nodes.
type NodeCapacityResponse struct {
	Nodes   []NodeCapacity      `json:"nodes"`
	Summary NodeCapacitySummary `json:"summary"`
}

// handleGetClusterNodes is the GET /api/v1/cluster/nodes handler (admin-only).
func (s *Server) handleGetClusterNodes(w http.ResponseWriter, r *http.Request) {
	out, err := s.computeClusterNodes(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to compute node capacity: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, out)
}

// nodeGroupLabels are the well-known labels cloud providers stamp on nodes to
// identify the managed group/pool they belong to. We surface the first match
// rather than reading cloud ASG APIs, keeping the Router cloud-agnostic.
var nodeGroupLabels = []string{
	"eks.amazonaws.com/nodegroup",   // EKS managed node groups
	"karpenter.sh/nodepool",         // Karpenter
	"cloud.google.com/gke-nodepool", // GKE
	"agentpool",                     // AKS
}

func (s *Server) computeClusterNodes(ctx context.Context) (*NodeCapacityResponse, error) {
	nodes := &corev1.NodeList{}
	if err := s.k8sClient.List(ctx, nodes); err != nil {
		return nil, err
	}
	pods := &corev1.PodList{}
	if err := s.k8sClient.List(ctx, pods); err != nil {
		return nil, err
	}

	// Sum scheduled Pod requests per node in a single pass. Terminal pods
	// (Succeeded/Failed) no longer hold scheduling reservations, so skip them.
	reqByNode := map[string]NodeResources{}
	for i := range pods.Items {
		p := &pods.Items[i]
		if p.Spec.NodeName == "" || p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		agg := reqByNode[p.Spec.NodeName]
		for c := range p.Spec.Containers {
			req := p.Spec.Containers[c].Resources.Requests
			agg.CPUMillis += req.Cpu().MilliValue()
			agg.MemBytes += req.Memory().Value()
		}
		reqByNode[p.Spec.NodeName] = agg
	}

	out := &NodeCapacityResponse{Nodes: make([]NodeCapacity, 0, len(nodes.Items))}
	var sumAllocCPU, sumAllocMem, sumReqCPU, sumReqMem int64
	for i := range nodes.Items {
		n := &nodes.Items[i]
		nc := NodeCapacity{
			Name:         n.Name,
			Ready:        nodeIsReady(n),
			InstanceType: n.Labels["node.kubernetes.io/instance-type"],
			NodeGroup:    firstLabel(n.Labels, nodeGroupLabels),
			Allocatable: NodeResources{
				CPUMillis: n.Status.Allocatable.Cpu().MilliValue(),
				MemBytes:  n.Status.Allocatable.Memory().Value(),
			},
			Requests: reqByNode[n.Name],
		}
		if nc.InstanceType == "" {
			nc.InstanceType = n.Labels["beta.kubernetes.io/instance-type"]
		}
		out.Nodes = append(out.Nodes, nc)
		if nc.Ready {
			out.Summary.Ready++
		}
		sumAllocCPU += nc.Allocatable.CPUMillis
		sumAllocMem += nc.Allocatable.MemBytes
		sumReqCPU += nc.Requests.CPUMillis
		sumReqMem += nc.Requests.MemBytes
	}
	out.Summary.Total = len(nodes.Items)
	out.Summary.Allocatable = NodeResources{CPUMillis: sumAllocCPU, MemBytes: sumAllocMem}
	out.Summary.Requests = NodeResources{CPUMillis: sumReqCPU, MemBytes: sumReqMem}
	out.Summary.CPUSaturationPct = pct(sumReqCPU, sumAllocCPU)
	out.Summary.MemSaturationPct = pct(sumReqMem, sumAllocMem)
	return out, nil
}

func nodeIsReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func firstLabel(labels map[string]string, keys []string) string {
	for _, k := range keys {
		if v := labels[k]; v != "" {
			return v
		}
	}
	return ""
}

// pct returns num/den as a percentage rounded to one decimal, 0 when den<=0.
func pct(num, den int64) float64 {
	if den <= 0 {
		return 0
	}
	return math.Round(float64(num)/float64(den)*1000) / 10
}
