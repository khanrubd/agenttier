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
	"sync"

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/governance"
)

// ctrlClientKey returns the controller-runtime client.ObjectKey for a sandbox.
// Pulled into a helper so the per-endpoint files don't depend on the
// controller-runtime client package directly.
func ctrlClientKey(sandbox *agenttierv1alpha1.Sandbox) client.ObjectKey {
	return client.ObjectKey{Name: sandbox.Name, Namespace: sandbox.Namespace}
}

// ctrlClientKeyClusterTemplate returns the key for a cluster-scoped template.
func ctrlClientKeyClusterTemplate(name string) client.ObjectKey {
	return client.ObjectKey{Name: name}
}

// ctrlClientKeyNamespacedTemplate returns the key for a namespace-scoped template.
func ctrlClientKeyNamespacedTemplate(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
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

	// ExecCommandStreamWithStdin behaves like ExecCommandStream but also
	// pipes the supplied stdin reader into the in-pod process. Used by
	// /invoke so request bodies above the shell ARG_MAX (~128 KB) reach
	// the entrypoint via SPDY's stdin channel rather than `printf |
	// base64 -d` shell tricks. Pass nil stdin to mimic ExecCommandStream.
	ExecCommandStreamWithStdin(ctx context.Context, namespace, podName, container string, command []string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
}

// SandboxLookup resolves a sandbox by ID and applies any auth checks the
// host Router enforces (ownership for non-admins, sharing). The handler
// does not implement these itself — it delegates to whatever the host
// already enforces for /api/v1/sandboxes/{id}.
type SandboxLookup func(ctx context.Context, sandboxID string, claims *Claims) (*agenttierv1alpha1.Sandbox, error)

// PolicyResolver returns the effective governance policy for the given
// namespace. Agent endpoints use this to clamp template-supplied caps
// (e.g. maxConcurrentInvokes) at /configure time. Returning an empty
// policy is a valid signal that no limits apply.
type PolicyResolver func(ctx context.Context, namespace string) (governance.Policy, error)

// HTTPExecResolver tries to resolve an HTTP-exec dispatcher for the given
// sandbox. Returns (dispatcher, true) when the sandbox is opted in and
// the in-pod runtime is reachable; (nil, false) means "use SPDY." This
// is the exact same shape s.dispatchExec uses for /exec — pulled into an
// interface so the agent package doesn't have to import the parent
// router package.
type HTTPExecResolver func(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (HTTPExecDispatcher, bool)

// HTTPExecDispatcher is the surface the agent /invoke handler needs from
// the in-pod runtime client. Two methods: stream an invoke, cancel an
// invoke. Both delegate to pkg/router/sandboxhttp.Client in production.
type HTTPExecDispatcher interface {
	// InvokeStream runs the entrypoint via the runtime's /invoke SSE
	// endpoint. The onEvent callback fires for each SSE event the
	// runtime emits (start / log / exit). Returning an error from
	// onEvent aborts the stream.
	InvokeStream(ctx context.Context, req HTTPInvokeRequest, onEvent func(eventType string, data []byte) error) error

	// InvokeCancel terminates an in-flight invoke addressed by ID.
	// Returns nil on success, error otherwise. 404-equivalent (no
	// such invoke) is a non-nil error — the caller distinguishes via
	// strings.Contains so we don't leak http-specific errors here.
	InvokeCancel(ctx context.Context, invokeID string) error
}

// HTTPInvokeRequest is the agent-package-local shape of the HTTP runtime's
// /invoke request body. Mirrors sandboxhttp.InvokeRequest so the Router
// can pass it through unchanged.
type HTTPInvokeRequest struct {
	Command        []string
	Stdin          string
	TimeoutSeconds int
	WorkingDir     string
	Env            map[string]string
	InvokeID       string
}

// Options bundles the dependencies the agent package needs. The Router
// constructs one of these from its existing state and hands it to New().
type Options struct {
	K8sClient    client.Client
	Bridge       ExecBridge
	Logger       *slog.Logger
	ClaimsLookup func(r *http.Request) *Claims
	SandboxOf    SandboxLookup
	// PolicyOf is optional. When nil, no governance clamping is applied
	// at /configure time and the template's values pass through.
	PolicyOf PolicyResolver
	// HTTPExecOf is optional. When set and the sandbox is opted into
	// HTTP-exec, /invoke streams through the in-pod runtime instead of
	// going through SPDY. Cancel routes to the runtime too — fixing
	// the cross-replica cancel bug naturally.
	HTTPExecOf HTTPExecResolver
}

// Handler holds dependencies for the agent endpoints and exposes
// http.HandlerFuncs ready to mount at /api/v1/sandboxes/{id}/...
type Handler struct {
	opts        Options
	concurrency *concurrencyTracker
	invokes     sync.Map // map[invokeID]*invokeRegistryEntry
}

// New returns a Handler ready to serve agent requests.
func New(opts Options) *Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Handler{
		opts:        opts,
		concurrency: newConcurrencyTracker(),
	}
}

// RegisterRoutes mounts the agent endpoints onto the given mux subrouter.
// Caller is expected to have applied authentication middleware already.
func (h *Handler) RegisterRoutes(api *mux.Router) {
	api.HandleFunc("/sandboxes/{id}/configure", h.handleConfigure).Methods("POST")
	api.HandleFunc("/sandboxes/{id}/configure/install-log", h.handleGetInstallLog).Methods("GET")
	api.HandleFunc("/sandboxes/{id}/invoke", h.handleInvoke).Methods("POST")
	api.HandleFunc("/sandboxes/{id}/invoke/cancel", h.handleInvokeCancel).Methods("POST")
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// recordAuditEvent posts a Kubernetes event onto the sandbox CR. Shows up in
// `kubectl describe sandbox <name>` and via the existing audit-log endpoint
// without needing a separate audit store. Reason should be a short
// CamelCase token; Message is human-readable. Best-effort — failures are
// logged but never bubble up to the caller (audit lag must never block
// configure / invoke).
func (h *Handler) recordAuditEvent(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, eventType, reason, message string) {
	if sandbox == nil {
		return
	}
	now := metav1.Now()
	evt := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: sandbox.Name + ".",
			Namespace:    sandbox.Namespace,
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:       "Sandbox",
			APIVersion: agenttierv1alpha1.GroupVersion.String(),
			Namespace:  sandbox.Namespace,
			Name:       sandbox.Name,
			UID:        sandbox.UID,
		},
		Reason:         reason,
		Message:        message,
		Type:           eventType, // Normal | Warning
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
		Source: corev1.EventSource{
			Component: "agenttier-router/agent",
		},
	}
	if err := h.opts.K8sClient.Create(ctx, evt); err != nil {
		h.opts.Logger.Warn("failed to write agent audit event",
			"sandbox", sandbox.Name, "reason", reason, "error", err)
	}
}
