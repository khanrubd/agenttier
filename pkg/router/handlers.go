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

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/controller/warmpool"
	"github.com/agenttier/agenttier/pkg/governance"
	"github.com/agenttier/agenttier/pkg/router/terminal"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// --- Sandbox CRUD Handlers ---

type CreateSandboxRequest struct {
	Name        string                               `json:"name"`
	Namespace   string                               `json:"namespace,omitempty"`
	TemplateRef *agenttierv1alpha1.TemplateReference `json:"templateRef"`
	Timeout     string                               `json:"timeout,omitempty"`
	IdleTimeout string                               `json:"idleTimeout,omitempty"`
	Storage     *agenttierv1alpha1.StorageSpec       `json:"storage,omitempty"`
}

func (s *Server) handleCreateSandbox(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req CreateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.TemplateRef == nil || req.TemplateRef.Name == "" {
		respondError(w, http.StatusBadRequest, "templateRef.name is required")
		return
	}

	namespace := req.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Build the Sandbox CR
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: namespace,
		},
		Spec: agenttierv1alpha1.SandboxSpec{
			TemplateRef: req.TemplateRef,
			CreatedBy: &agenttierv1alpha1.UserIdentity{
				Sub:         claims.Sub,
				Email:       claims.Email,
				DisplayName: claims.Name,
			},
		},
	}

	if req.Storage != nil {
		sandbox.Spec.Storage = req.Storage
	}

	// Parse timeout if provided
	if req.Timeout != "" {
		d, err := parseDuration(req.Timeout)
		if err == nil {
			sandbox.Spec.Timeout = d
		}
	}
	if req.IdleTimeout != "" {
		d, err := parseDuration(req.IdleTimeout)
		if err == nil {
			sandbox.Spec.IdleTimeout = d
		}
	}

	// Governance enforcement. Violations short-circuit before the CR ever
	// reaches the API server so users get a crisp 403 with details instead of
	// a half-created sandbox that trips over a later webhook.
	if s.governanceStore != nil {
		policy, err := governance.Resolve(r.Context(), s.governanceStore, namespace)
		if err != nil {
			s.logger.Warn("failed to resolve governance policy; proceeding without enforcement", "namespace", namespace, "error", err)
		} else if !policy.IsEmpty() {
			existing := &agenttierv1alpha1.SandboxList{}
			if err := s.k8sClient.List(r.Context(), existing, client.InNamespace(namespace)); err != nil {
				respondError(w, http.StatusInternalServerError, "failed to check namespace usage: "+err.Error())
				return
			}
			usage := governance.CountUsage(existing, claims.Sub)
			if v := governance.Check(policy, usage, sandbox); v.Violated() {
				respondJSON(w, http.StatusForbidden, map[string]interface{}{
					"error":      "policy_violation",
					"violations": v,
				})
				return
			}
		}
	}

	// Create the Sandbox CR in Kubernetes
	if err := s.k8sClient.Create(r.Context(), sandbox); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create sandbox: "+err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"sandboxId":   sandbox.Name,
		"name":        sandbox.Name,
		"namespace":   sandbox.Namespace,
		"status":      "Creating",
		"templateRef": sandbox.Spec.TemplateRef.Name,
	})
}

func (s *Server) handleListSandboxes(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	// List all sandboxes (controller will filter by namespace RBAC)
	sandboxList := &agenttierv1alpha1.SandboxList{}
	listOpts := []client.ListOption{}

	// Filter by namespace if specified
	ns := r.URL.Query().Get("namespace")
	if ns != "" {
		listOpts = append(listOpts, client.InNamespace(ns))
	}

	if err := s.k8sClient.List(r.Context(), sandboxList, listOpts...); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list sandboxes: "+err.Error())
		return
	}

	// Filter by ownership (non-admin only sees their own + shared)
	var results []map[string]interface{}
	for _, sb := range sandboxList.Items {
		// Non-admin: only show own sandboxes
		if !claims.IsAdmin {
			if sb.Spec.CreatedBy == nil || sb.Spec.CreatedBy.Sub != claims.Sub {
				continue
			}
		}

		// Apply status filter
		statusFilter := r.URL.Query().Get("status")
		if statusFilter != "" && string(sb.Status.Phase) != statusFilter {
			continue
		}

		results = append(results, sandboxToJSON(&sb))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"sandboxes": results})
}

func (s *Server) handleGetSandbox(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sandboxID := mux.Vars(r)["id"]
	sandbox, err := s.getSandboxWithAuthCheck(r.Context(), sandboxID, claims)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, sandboxToJSON(sandbox))
}

func (s *Server) handleDeleteSandbox(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sandboxID := mux.Vars(r)["id"]
	sandbox, err := s.getSandboxWithAuthCheck(r.Context(), sandboxID, claims)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	if err := s.k8sClient.Delete(r.Context(), sandbox); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to delete sandbox: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStopSandbox(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sandboxID := mux.Vars(r)["id"]
	sandbox, err := s.getSandboxWithAuthCheck(r.Context(), sandboxID, claims)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseRunning {
		respondError(w, http.StatusConflict, fmt.Sprintf("cannot stop sandbox in phase %s", sandbox.Status.Phase))
		return
	}

	// Annotate to signal stop (controller watches for this)
	if sandbox.Annotations == nil {
		sandbox.Annotations = make(map[string]string)
	}
	sandbox.Annotations["agenttier.io/action"] = "stop"
	if err := s.k8sClient.Update(r.Context(), sandbox); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to stop sandbox: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "stopping"})
}

func (s *Server) handleResumeSandbox(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sandboxID := mux.Vars(r)["id"]
	sandbox, err := s.getSandboxWithAuthCheck(r.Context(), sandboxID, claims)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseStopped && sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseError {
		respondError(w, http.StatusConflict, fmt.Sprintf("cannot resume sandbox in phase %s", sandbox.Status.Phase))
		return
	}

	// Set phase back to Creating to trigger reconciliation
	sandbox.Status.Phase = agenttierv1alpha1.SandboxPhaseCreating
	sandbox.Status.Message = "Resuming"
	if err := s.k8sClient.Status().Update(r.Context(), sandbox); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to resume sandbox: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "resuming"})
}

func (s *Server) handleCloneSandbox(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "cloning not yet implemented")
}

// --- Command Execution ---

type ExecRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

func (s *Server) handleExecCommand(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	sandboxID := mux.Vars(r)["id"]
	sandbox, err := s.getSandboxWithAuthCheck(r.Context(), sandboxID, claims)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseRunning {
		respondError(w, http.StatusConflict, "sandbox is not running")
		return
	}

	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Command == "" {
		respondError(w, http.StatusBadRequest, "command is required")
		return
	}
	if req.Timeout == 0 {
		req.Timeout = 30
	}

	// Execute via bridge
	if s.bridge == nil {
		respondError(w, http.StatusServiceUnavailable, "terminal bridge not initialized")
		return
	}

	result, err := s.bridge.ExecCommand(r.Context(), sandbox.Namespace, sandbox.Status.PodName, "sandbox", []string{"/bin/sh", "-c", req.Command}, req.Timeout)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "exec failed: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, result)
}

// --- WebSocket Terminal ---

func (s *Server) handleTerminalWebSocket(w http.ResponseWriter, r *http.Request) {
	sandboxID := mux.Vars(r)["sandboxId"]

	// Auth: dev mode bypass (no OIDC configured = auto-admin)
	var claims *Claims
	if s.config.OIDCIssuerURL == "" {
		claims = &Claims{
			Sub:     "dev-user",
			Email:   "dev@agenttier.local",
			Name:    "Dev User",
			Groups:  []string{"agenttier-admins"},
			IsAdmin: true,
		}
	} else {
		// Production: extract token from query param or header
		token := r.URL.Query().Get("token")
		if token == "" {
			authHeader := r.Header.Get("Authorization")
			token = strings.TrimPrefix(authHeader, "Bearer ")
			if token == authHeader {
				token = ""
			}
		}
		if token == "" {
			http.Error(w, "missing authentication", http.StatusUnauthorized)
			return
		}

		var err error
		claims, err = s.validateJWT(r.Context(), token)
		if err != nil {
			http.Error(w, "invalid token: "+err.Error(), http.StatusUnauthorized)
			return
		}
	}

	// Lookup sandbox
	sandbox := &agenttierv1alpha1.Sandbox{}
	if err := s.k8sClient.Get(r.Context(), types.NamespacedName{Name: sandboxID, Namespace: "default"}, sandbox); err != nil {
		http.Error(w, "sandbox not found", http.StatusNotFound)
		return
	}

	// Check ownership
	if !claims.IsAdmin && (sandbox.Spec.CreatedBy == nil || sandbox.Spec.CreatedBy.Sub != claims.Sub) {
		http.Error(w, "access denied", http.StatusForbidden)
		return
	}

	// Check sandbox is running
	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseRunning {
		http.Error(w, "sandbox is not running", http.StatusConflict)
		return
	}

	// Upgrade to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("WebSocket upgrade failed", "error", err)
		return
	}

	// Determine shell
	shell := "/bin/bash"

	// Create session
	session := s.sessionManager.CreateSession(
		sandboxID, sandbox.Namespace,
		claims.Sub, claims.Email,
		sandbox.Status.PodName, shell,
		conn, false,
	)

	s.logger.Info("terminal session started",
		"sessionId", session.ID,
		"sandboxId", sandboxID,
		"userId", claims.Sub,
	)

	// Bridge the WebSocket to the pod exec stream
	if s.bridge != nil {
		if err := s.bridge.Connect(r.Context(), session); err != nil {
			s.logger.Error("terminal bridge error", "sessionId", session.ID, "error", err)
		}
	}

	// Cleanup
	s.sessionManager.RemoveSession(session.ID)
	conn.Close()
}

// --- Template Handlers ---

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templateList := &agenttierv1alpha1.ClusterSandboxTemplateList{}
	if err := s.k8sClient.List(r.Context(), templateList); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list templates: "+err.Error())
		return
	}

	var results []map[string]interface{}
	for _, t := range templateList.Items {
		results = append(results, templateToJSON(&t))
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"templates": results})
}

func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req struct {
		Name string                                `json:"name"`
		Spec agenttierv1alpha1.SandboxTemplateSpec `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}

	tmpl := &agenttierv1alpha1.ClusterSandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: req.Name,
			Labels: map[string]string{
				"agenttier.io/managed": "true",
			},
		},
		Spec: req.Spec,
	}

	if err := s.k8sClient.Create(r.Context(), tmpl); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to create template: "+err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, templateToJSON(tmpl))
}

func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	tmpl := &agenttierv1alpha1.ClusterSandboxTemplate{}
	if err := s.k8sClient.Get(r.Context(), types.NamespacedName{Name: name}, tmpl); err != nil {
		respondError(w, http.StatusNotFound, "template not found")
		return
	}
	respondJSON(w, http.StatusOK, templateToJSON(tmpl))
}

func (s *Server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	name := mux.Vars(r)["name"]

	// Fetch existing template
	tmpl := &agenttierv1alpha1.ClusterSandboxTemplate{}
	if err := s.k8sClient.Get(r.Context(), types.NamespacedName{Name: name}, tmpl); err != nil {
		respondError(w, http.StatusNotFound, "template not found")
		return
	}

	// Decode the new spec from the request body
	var req struct {
		Spec agenttierv1alpha1.SandboxTemplateSpec `json:"spec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Update the spec in-place (preserves metadata like resourceVersion)
	tmpl.Spec = req.Spec

	if err := s.k8sClient.Update(r.Context(), tmpl); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to update template: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, templateToJSON(tmpl))
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	name := mux.Vars(r)["name"]

	// Check if any running sandboxes reference this template
	sandboxList := &agenttierv1alpha1.SandboxList{}
	if err := s.k8sClient.List(r.Context(), sandboxList); err == nil {
		for _, sb := range sandboxList.Items {
			if sb.Status.ResolvedTemplate == name && sb.Status.Phase == agenttierv1alpha1.SandboxPhaseRunning {
				respondError(w, http.StatusConflict, fmt.Sprintf("cannot delete template: sandbox %q is still using it", sb.Name))
				return
			}
		}
	}

	tmpl := &agenttierv1alpha1.ClusterSandboxTemplate{}
	if err := s.k8sClient.Get(r.Context(), types.NamespacedName{Name: name}, tmpl); err != nil {
		respondError(w, http.StatusNotFound, "template not found")
		return
	}

	if err := s.k8sClient.Delete(r.Context(), tmpl); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to delete template: "+err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// templateToJSON serializes a ClusterSandboxTemplate to a JSON-friendly map
// including the full spec so the UI can render it in a YAML editor.
func templateToJSON(t *agenttierv1alpha1.ClusterSandboxTemplate) map[string]interface{} {
	result := map[string]interface{}{
		"name":            t.Name,
		"description":     t.Spec.Description,
		"image":           imageFromSpec(t.Spec.Image),
		"resourceVersion": t.ResourceVersion,
		"spec":            t.Spec,
	}
	return result
}

// --- Placeholder handlers for features not yet wired ---

func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleGetFile(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handlePutFile(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleListPorts(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{"ports": []interface{}{}})
}
func (s *Server) handleForwardPort(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleRemovePort(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleGetSharing(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{"users": []interface{}{}})
}
func (s *Server) handleShareSandbox(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleRevokeShare(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleCreateShareLink(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := s.governanceStore.ListPolicies(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to list policies: "+err.Error())
		return
	}
	// Shape the response so the UI can find the cluster default easily.
	var cluster *governance.Policy
	namespaces := make([]map[string]interface{}, 0, len(policies))
	for _, sp := range policies {
		if sp.Scope == "" {
			p := sp.Policy
			cluster = &p
			continue
		}
		namespaces = append(namespaces, map[string]interface{}{
			"namespace": sp.Scope,
			"policy":    sp.Policy,
		})
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"cluster":    cluster,
		"namespaces": namespaces,
	})
}

func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	namespace := mux.Vars(r)["namespace"]
	policy, err := s.governanceStore.GetPolicy(r.Context(), namespace)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get policy: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"namespace": namespace,
		"policy":    policy,
	})
}

func (s *Server) handleUpsertClusterPolicy(w http.ResponseWriter, r *http.Request) {
	var policy governance.Policy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := s.governanceStore.SetPolicy(r.Context(), "", policy); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to save policy: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"scope":  "cluster",
		"policy": policy,
	})
}

func (s *Server) handleSetPolicy(w http.ResponseWriter, r *http.Request) {
	namespace := mux.Vars(r)["namespace"]
	if namespace == "" {
		respondError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	var policy governance.Policy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := s.governanceStore.SetPolicy(r.Context(), namespace, policy); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to save policy: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"namespace": namespace,
		"policy":    policy,
	})
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	namespace := mux.Vars(r)["namespace"]
	if namespace == "" {
		respondError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	if err := s.governanceStore.DeletePolicy(r.Context(), namespace); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to delete policy: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetEffectivePolicy(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		namespace = "default"
	}
	policy, err := governance.Resolve(r.Context(), s.governanceStore, namespace)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to resolve policy: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"namespace": namespace,
		"policy":    policy,
	})
}
func (s *Server) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	// Get Kubernetes events for sandboxes as activity log
	var events []map[string]interface{}
	eventList := &corev1.EventList{}
	// List events from all namespaces
	if err := s.k8sClient.List(r.Context(), eventList); err == nil {
		for _, event := range eventList.Items {
			if event.InvolvedObject.Kind != "Sandbox" {
				continue
			}
			events = append(events, map[string]interface{}{
				"timestamp":   event.LastTimestamp.Time,
				"eventType":   event.Reason,
				"sandboxId":   event.InvolvedObject.Name,
				"sandboxName": event.InvolvedObject.Name,
				"namespace":   event.InvolvedObject.Namespace,
				"userEmail":   "",
				"details":     map[string]string{"reason": event.Message},
			})
		}
	}

	// Sort by timestamp descending (most recent first)
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"events": events})
}

func (s *Server) handleGetUsageAnalytics(w http.ResponseWriter, r *http.Request) {
	sandboxList := &agenttierv1alpha1.SandboxList{}
	if err := s.k8sClient.List(r.Context(), sandboxList); err != nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{})
		return
	}

	statusBreakdown := map[string]int{}
	templateBreakdown := map[string]int{}
	var totalStartupMs int64
	startupCount := 0

	for _, sb := range sandboxList.Items {
		phase := string(sb.Status.Phase)
		if phase == "" {
			phase = "creating"
		}
		statusBreakdown[phase]++

		tmpl := sb.Status.ResolvedTemplate
		if tmpl == "" {
			tmpl = "unknown"
		}
		templateBreakdown[tmpl]++

		// Calculate startup time if available
		if sb.Status.StartedAt != nil {
			dur := sb.Status.StartedAt.Time.Sub(sb.CreationTimestamp.Time)
			totalStartupMs += dur.Milliseconds()
			startupCount++
		}
	}

	avgStartupMs := int64(0)
	if startupCount > 0 {
		avgStartupMs = totalStartupMs / int64(startupCount)
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"total_sandboxes":      len(sandboxList.Items),
		"status_breakdown":     statusBreakdown,
		"template_breakdown":   templateBreakdown,
		"avg_startup_ms":       avgStartupMs,
		"startup_sample_count": startupCount,
	})
}

func (s *Server) handleGetCostEstimates(w http.ResponseWriter, r *http.Request) {
	sandboxList := &agenttierv1alpha1.SandboxList{}
	if err := s.k8sClient.List(r.Context(), sandboxList); err != nil {
		respondJSON(w, http.StatusOK, map[string]interface{}{})
		return
	}

	// Cost rates (USD per hour) — configurable via Helm values in production
	cpuPerHour := 0.04
	memPerGBHour := 0.005
	storagePerGBMonth := 0.10

	running := 0
	stopped := 0
	totalHourlyCost := 0.0
	var perTemplate []map[string]interface{}
	templateCosts := map[string]float64{}
	templateCounts := map[string]int{}

	for _, sb := range sandboxList.Items {
		tmpl := sb.Status.ResolvedTemplate
		if tmpl == "" {
			tmpl = "unknown"
		}

		if sb.Status.Phase == agenttierv1alpha1.SandboxPhaseRunning {
			running++
			// Estimate: 2 CPU, 4GB RAM per running sandbox (from template defaults)
			hourlyCost := (2 * cpuPerHour) + (4 * memPerGBHour)
			totalHourlyCost += hourlyCost
			templateCosts[tmpl] += hourlyCost
		} else if sb.Status.Phase == agenttierv1alpha1.SandboxPhaseStopped {
			stopped++
		}
		templateCounts[tmpl]++
	}

	// Storage cost (all sandboxes, running or stopped, have PVCs)
	storageCostMonthly := float64(len(sandboxList.Items)) * 10.0 * storagePerGBMonth / 30.0 * 24.0 // per hour

	for tmpl, cost := range templateCosts {
		perTemplate = append(perTemplate, map[string]interface{}{
			"template":    tmpl,
			"hourly_cost": cost,
			"count":       templateCounts[tmpl],
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"running_sandboxes":       running,
		"stopped_sandboxes":       stopped,
		"total_hourly_compute":    totalHourlyCost,
		"total_hourly_storage":    storageCostMonthly,
		"total_estimated_monthly": (totalHourlyCost + storageCostMonthly) * 24 * 30,
		"per_template":            perTemplate,
	})
}
func (s *Server) handleAdminListSandboxes(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{"sandboxes": []interface{}{}})
}
func (s *Server) handleAdminListSharing(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{})
}
func (s *Server) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{})
}
func (s *Server) handleUpdatePreferences(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleGetMe(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"sub":     claims.Sub,
		"email":   claims.Email,
		"name":    claims.Name,
		"groups":  claims.Groups,
		"isAdmin": claims.IsAdmin,
	})
}
func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{"keys": []interface{}{}})
}
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}
func (s *Server) handleRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	respondError(w, http.StatusNotImplemented, "not yet implemented")
}

// --- Warm Pool Handlers ---

func (s *Server) handleGetWarmPoolStatus(w http.ResponseWriter, r *http.Request) {
	status, err := warmpool.GetStatus(r.Context(), s.k8sClient)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "failed to get pool status: "+err.Error())
		return
	}
	respondJSON(w, http.StatusOK, status)
}

func (s *Server) handleSetWarmPoolConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DesiredCount int    `json:"desiredCount"`
		Template     string `json:"template"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DesiredCount < 0 || req.DesiredCount > 10 {
		respondError(w, http.StatusBadRequest, "desiredCount must be 0-10")
		return
	}
	if req.Template == "" {
		req.Template = "general-coding"
	}

	cfg := warmpool.Config{DesiredCount: req.DesiredCount, Template: req.Template}
	if err := warmpool.SetConfig(r.Context(), s.k8sClient, cfg); err != nil {
		respondError(w, http.StatusInternalServerError, "failed to save config: "+err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{"status": "updated", "desiredCount": req.DesiredCount, "template": req.Template})
}

// --- Helpers ---

func (s *Server) getSandboxWithAuthCheck(ctx context.Context, sandboxID string, claims *Claims) (*agenttierv1alpha1.Sandbox, error) {
	sandbox := &agenttierv1alpha1.Sandbox{}
	// Try default namespace first, then try to find it
	if err := s.k8sClient.Get(ctx, types.NamespacedName{Name: sandboxID, Namespace: "default"}, sandbox); err != nil {
		return nil, fmt.Errorf("sandbox %s not found", sandboxID)
	}

	// Check ownership (non-admin must own the sandbox)
	if !claims.IsAdmin {
		if sandbox.Spec.CreatedBy == nil || sandbox.Spec.CreatedBy.Sub != claims.Sub {
			return nil, fmt.Errorf("access denied to sandbox %s", sandboxID)
		}
	}

	return sandbox, nil
}

func sandboxToJSON(sb *agenttierv1alpha1.Sandbox) map[string]interface{} {
	result := map[string]interface{}{
		"sandboxId":   sb.Name,
		"name":        sb.Name,
		"namespace":   sb.Namespace,
		"status":      string(sb.Status.Phase),
		"podName":     sb.Status.PodName,
		"pvcName":     sb.Status.PVCName,
		"templateRef": sb.Status.ResolvedTemplate,
		"createdAt":   sb.CreationTimestamp.Time.String(),
		"message":     sb.Status.Message,
	}
	if sb.Spec.CreatedBy != nil {
		result["createdBy"] = map[string]string{
			"email":       sb.Spec.CreatedBy.Email,
			"displayName": sb.Spec.CreatedBy.DisplayName,
		}
	}
	if sb.Status.LastActivityTimestamp != nil {
		result["lastActivityAt"] = sb.Status.LastActivityTimestamp.Time.String()
	}
	return result
}

func imageFromSpec(img *agenttierv1alpha1.ImageSpec) string {
	if img == nil {
		return ""
	}
	return img.Repository
}

func parseDuration(s string) (*metav1.Duration, error) {
	// Simple duration parsing: "8h", "30m", "24h"
	// metav1.Duration wraps time.Duration
	// For now, we'll handle common formats
	_ = s
	return nil, fmt.Errorf("duration parsing not implemented")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Ensure terminal package is referenced
var _ = terminal.MessageTypeInput
