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

// Headroom — runtime read/write of the spare-node pause-Pod Deployment.
//
// The Helm chart provisions an opt-in `agenttier-headroom` Deployment of
// pause Pods at a deeply-negative PriorityClass; when a real sandbox
// arrives the scheduler preempts a pause Pod, the evicted Pod goes
// Pending, and Cluster Autoscaler adds a fresh node — N+1 spare-node
// proactive scaling. See docs/docs/scaling.md.
//
// This file exposes:
//
//   GET  /api/v1/cluster/headroom — current replicas + per-replica
//        cpu/memory + ready/total counts.
//   PUT  /api/v1/cluster/headroom — admin-gated update of replicas
//        and (optionally) the per-replica cpu/memory.
//
// Why surface this at runtime instead of leaving it to `helm upgrade`?
// Operators want to bump headroom up before a known burst (a release
// announcement, an onboarding cohort) without redeploying the whole
// chart. The Settings → Cluster autoscaling block in the Web UI surfaces
// these knobs alongside the warm pool config.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// headroomDeploymentName is the chart-installed Deployment name. Hard-
// coded because the chart's _helpers.tpl gives every component a stable
// `<release>-<component>` name and the Web UI is the only consumer here.
const headroomDeploymentName = "agenttier-headroom"

// HeadroomConfig is the wire shape both endpoints use. CPU + memory are
// strings to match Kubernetes' resource.Quantity conventions ("500m",
// "1Gi") rather than numbers — the Web UI form mirrors what an operator
// would put in values.yaml.
type HeadroomConfig struct {
	Replicas int    `json:"replicas"`
	CPU      string `json:"cpu"`
	Memory   string `json:"memory"`

	// ReadyReplicas + Replicas are read-only on the GET response; the
	// PUT handler ignores them. Helps the Web UI render "3/4 ready"
	// without a separate status endpoint.
	ReadyReplicas int `json:"readyReplicas,omitempty"`

	// Enabled reports whether the chart's headroom Deployment exists
	// at all. False means the operator hasn't enabled
	// optional.headroom.enabled and the PUT handler will 404.
	Enabled bool `json:"enabled"`
}

func (s *Server) handleGetHeadroomConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.readHeadroom(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to read headroom: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSetHeadroomConfig(w http.ResponseWriter, r *http.Request) {
	var req HeadroomConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Cap replicas in [0, 50]. Higher than 50 is almost certainly a
	// finger-fumble — at $60/mo per always-on spare node, 50 replicas
	// would be $3000/mo of cold capacity. Operators who want more can
	// edit the Helm values directly.
	if req.Replicas < 0 || req.Replicas > 50 {
		respondError(w, http.StatusBadRequest, "replicas must be in [0, 50]")
		return
	}

	// Validate CPU + memory by parsing through resource.Quantity. Empty
	// strings mean "leave the existing value alone", which we honor by
	// only updating the field on the live Deployment when the request
	// supplied a non-empty value.
	if req.CPU != "" {
		if _, err := resource.ParseQuantity(req.CPU); err != nil {
			respondError(w, http.StatusBadRequest, "invalid cpu quantity: "+err.Error())
			return
		}
	}
	if req.Memory != "" {
		if _, err := resource.ParseQuantity(req.Memory); err != nil {
			respondError(w, http.StatusBadRequest, "invalid memory quantity: "+err.Error())
			return
		}
	}

	if err := s.writeHeadroom(r.Context(), req); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to update headroom: "+err.Error())
		return
	}

	// Echo back the post-update state so callers can render immediately.
	out, err := s.readHeadroom(r.Context())
	if err != nil {
		// Update succeeded but read failed — rare. Just acknowledge.
		respondJSON(w, http.StatusOK, map[string]interface{}{"status": "updated"})
		return
	}
	respondJSON(w, http.StatusOK, out)
}

// readHeadroom fetches the live Deployment and projects it into the
// HeadroomConfig wire shape. Returns Enabled=false (with zero values)
// when the Deployment is missing — the chart's headroom add-on isn't
// installed.
func (s *Server) readHeadroom(ctx context.Context) (*HeadroomConfig, error) {
	dep := &appsv1.Deployment{}
	err := s.k8sClient.Get(ctx, client.ObjectKey{
		Name:      headroomDeploymentName,
		Namespace: s.config.InstallNamespace,
	}, dep)
	if err != nil {
		// 404 is the "headroom add-on not installed" case. Surface as
		// Enabled=false so the UI can render a "headroom is off" hint
		// instead of an error.
		return &HeadroomConfig{Enabled: false}, nil
	}

	cfg := &HeadroomConfig{
		Enabled:       true,
		ReadyReplicas: int(dep.Status.ReadyReplicas),
	}
	if dep.Spec.Replicas != nil {
		cfg.Replicas = int(*dep.Spec.Replicas)
	}
	if len(dep.Spec.Template.Spec.Containers) > 0 {
		c := dep.Spec.Template.Spec.Containers[0]
		if cpu, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			cfg.CPU = cpu.String()
		}
		if mem, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			cfg.Memory = mem.String()
		}
	}
	return cfg, nil
}

// writeHeadroom updates the Deployment's replica count and the first
// container's resource requests/limits. Empty CPU/Memory in the request
// leave the existing values intact.
//
// We use a get-modify-update pattern instead of strategic merge patch so
// the validation + clamp logic stays in one place; the update is racy
// against the chart's own helm-driven updates, but on conflict the
// caller can simply re-fire (the UI already re-reads on every poll).
func (s *Server) writeHeadroom(ctx context.Context, req HeadroomConfig) error {
	dep := &appsv1.Deployment{}
	if err := s.k8sClient.Get(ctx, client.ObjectKey{
		Name:      headroomDeploymentName,
		Namespace: s.config.InstallNamespace,
	}, dep); err != nil {
		return fmt.Errorf("headroom Deployment not found (is optional.headroom.enabled in your Helm values?): %w", err)
	}

	replicas := int32(req.Replicas) //nolint:gosec // req.Replicas is bounded to [0,50] before this call
	dep.Spec.Replicas = &replicas

	if len(dep.Spec.Template.Spec.Containers) > 0 {
		c := &dep.Spec.Template.Spec.Containers[0]
		if c.Resources.Requests == nil {
			c.Resources.Requests = corev1.ResourceList{}
		}
		if c.Resources.Limits == nil {
			c.Resources.Limits = corev1.ResourceList{}
		}
		// Per-replica cpu/memory: keep request == limit so headroom
		// reservations are firm (a pause Pod can't accidentally
		// consume more than its declared share, which would defeat
		// the spare-capacity contract).
		if req.CPU != "" {
			cpu := resource.MustParse(req.CPU)
			c.Resources.Requests[corev1.ResourceCPU] = cpu
			c.Resources.Limits[corev1.ResourceCPU] = cpu
		}
		if req.Memory != "" {
			mem := resource.MustParse(req.Memory)
			c.Resources.Requests[corev1.ResourceMemory] = mem
			c.Resources.Limits[corev1.ResourceMemory] = mem
		}
	}

	return s.k8sClient.Update(ctx, dep)
}
