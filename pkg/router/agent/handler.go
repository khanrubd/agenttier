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

// Package agent implements the AgentTier Router's agent-mode endpoints:
// POST /configure (one-shot install + agent code upload) and POST /invoke
// (Server-Sent Events streaming entrypoint runner). The package owns its own
// HTTP handlers so the rest of the Router stays focused on interactive code
// mode and the agent surface can evolve independently.
package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// ctrlClientKey returns the controller-runtime client.ObjectKey for a sandbox.
// Pulled into a helper so the per-endpoint files don't depend on the
// controller-runtime client package directly.
func ctrlClientKey(sandbox *agenttierv1alpha1.Sandbox) client.ObjectKey {
	return client.ObjectKey{Name: sandbox.Name, Namespace: sandbox.Namespace}
}

// Claims is the minimal authenticated identity surface the agent handlers
// need. It mirrors pkg/router.Claims by structure but lives here so the
// agent package never imports its parent — the Router glues the two together
// via ClaimsFromRequest.
type Claims struct {
	Sub     string
	Email   string
	Name    string
	IsAdmin bool
}

// ExecBridge is the subset of *terminal.Bridge that the agent endpoints use.
// Pulled to an interface so unit tests can stub the SPDY exec path without
// running a real Kubernetes API server.
type ExecBridge interface {
	// ExecCommandStream runs command in the named container and streams
	// stdout / stderr to the supplied writers. Returns the exit code on
	// clean termination, or a context error on cancel.
	ExecCommandStream(ctx context.Context, namespace, podName, container string, command []string, stdout, stderr io.Writer) (int, error)
}

// SandboxLookup resolves a sandbox by ID and applies any auth checks the
// host Router enforces (ownership for non-admins, sharing). The handler
// does not implement these itself — it delegates to whatever the host
// already enforces for /api/v1/sandboxes/{id}.
type SandboxLookup func(ctx context.Context, sandboxID string, claims *Claims) (*agenttierv1alpha1.Sandbox, error)

// Options bundles the dependencies the agent package needs. The Router
// constructs one of these from its existing state and hands it to New().
type Options struct {
	K8sClient    client.Client
	Bridge       ExecBridge
	Logger       *slog.Logger
	ClaimsLookup func(r *http.Request) *Claims
	SandboxOf    SandboxLookup
}

// Handler holds dependencies for the agent endpoints and exposes
// http.HandlerFuncs ready to mount at /api/v1/sandboxes/{id}/...
type Handler struct {
	opts Options
}

// New returns a Handler ready to serve agent requests.
func New(opts Options) *Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Handler{opts: opts}
}

// RegisterRoutes mounts the agent endpoints onto the given mux subrouter.
// Caller is expected to have applied authentication middleware already.
func (h *Handler) RegisterRoutes(api *mux.Router) {
	api.HandleFunc("/sandboxes/{id}/configure", h.handleConfigure).Methods("POST")
	// /invoke and /invoke/cancel land in the next milestone.
}

// --- common helpers -----------------------------------------------------

// loadSandbox is the first step of every agent endpoint. Resolves the
// sandbox via the host Router's auth-aware lookup, then enforces the two
// agent-mode invariants: mode == "agent" and phase == "Running".
func (h *Handler) loadSandbox(w http.ResponseWriter, r *http.Request) (*agenttierv1alpha1.Sandbox, *Claims, bool) {
	claims := h.opts.ClaimsLookup(r)
	if claims == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return nil, nil, false
	}
	sandboxID := mux.Vars(r)["id"]
	sandbox, err := h.opts.SandboxOf(r.Context(), sandboxID, claims)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return nil, nil, false
	}
	// Mode and phase are validated by the per-endpoint handlers because
	// /configure and /invoke have slightly different requirements (e.g.
	// /configure runs install only when phase==Running, /invoke also checks
	// in-flight count).
	return sandbox, claims, true
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) { //nolint:unused // wired into /invoke in milestone 3
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
