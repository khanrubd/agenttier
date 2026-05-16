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

// Package router implements the AgentTier Router — the HTTP/WebSocket server
// providing REST API, terminal access, port forwarding, and authentication.
package router

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/agenttier/agenttier/pkg/governance"
	"github.com/agenttier/agenttier/pkg/router/agent"
	"github.com/agenttier/agenttier/pkg/router/portforward"
	"github.com/agenttier/agenttier/pkg/router/terminal"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// Config holds the Router server configuration.
type Config struct {
	ListenAddr       string
	MetricsAddr      string
	OIDCIssuerURL    string
	OIDCClientID     string
	AdminGroup       string
	GroupClaim       string
	PreviewDomain    string
	GatewayName      string
	IngressClassName string
	KubeConfig       string
	// InstallNamespace is where AgentTier itself runs (and therefore where
	// the warm pool ConfigMap, pool Pods, and pool PVCs live). Set from
	// POD_NAMESPACE in the router deployment.
	InstallNamespace string
	// RateLimit configures per-IP and per-user request throttling. The
	// zero value disables rate limiting entirely (today's behavior); set
	// to DefaultRateLimitConfig() or a customized variant to enforce
	// limits.
	RateLimit RateLimitConfig
}

// Server is the main Router HTTP server.
type Server struct {
	config          *Config
	router          *mux.Router
	httpServer      *http.Server
	logger          *slog.Logger
	k8sClient       client.Client
	bridge          *terminal.Bridge
	sessionManager  *terminal.Manager
	governanceStore governance.Store
	portForward     *portforward.Manager
	rateLimiter     *rateLimiter
}

// NewServer creates a new Router server with all routes registered.
func NewServer(config *Config, k8sClient client.Client, bridge *terminal.Bridge) *Server {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	r := mux.NewRouter()
	s := &Server{
		config:          config,
		router:          r,
		logger:          logger,
		k8sClient:       k8sClient,
		bridge:          bridge,
		sessionManager:  terminal.NewManager(logger),
		governanceStore: governance.NewConfigMapStore(k8sClient),
		portForward: portforward.New(k8sClient, portforward.Options{
			PreviewDomain:    config.PreviewDomain,
			IngressClassName: config.IngressClassName,
		}),
	}
	// Rate limiter: zero-config = disabled (today's behavior). Operators
	// opt in by setting Helm values that populate config.RateLimit, which
	// the cmd/router main flag parser threads through.
	if config.RateLimit.PerIPRate > 0 || config.RateLimit.PerUserRate > 0 {
		s.rateLimiter = newRateLimiter(config.RateLimit)
	}

	// Register middleware
	r.Use(s.requestIDMiddleware)
	r.Use(s.corsMiddleware)
	r.Use(s.loggingMiddleware)
	// Per-IP rate limiting runs before auth so anonymous abuse gets
	// throttled even without a valid token. No-op when rateLimiter is nil.
	r.Use(s.rateLimitMiddleware)

	// Health and metrics (no auth required)
	r.HandleFunc("/healthz", s.handleHealthz).Methods("GET")
	r.HandleFunc("/readyz", s.handleReadyz).Methods("GET")
	r.HandleFunc("/metrics", promhttp.Handler().ServeHTTP).Methods("GET")

	// API routes (auth required)
	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(s.authMiddleware)
	// Per-user rate limit overlays the per-IP cap for authenticated
	// callers. Mounted after authMiddleware so claims are available.
	api.Use(s.rateLimitAuthenticatedMiddleware)

	// Sandbox CRUD
	api.HandleFunc("/sandboxes", s.handleListSandboxes).Methods("GET")
	api.HandleFunc("/sandboxes", s.handleCreateSandbox).Methods("POST")
	api.HandleFunc("/sandboxes/{id}", s.handleGetSandbox).Methods("GET")
	api.HandleFunc("/sandboxes/{id}", s.handleDeleteSandbox).Methods("DELETE")
	api.HandleFunc("/sandboxes/{id}/stop", s.handleStopSandbox).Methods("POST")
	api.HandleFunc("/sandboxes/{id}/resume", s.handleResumeSandbox).Methods("POST")
	api.HandleFunc("/sandboxes/{id}/clone", s.handleCloneSandbox).Methods("POST")

	// Command execution
	api.HandleFunc("/sandboxes/{id}/exec", s.handleExecCommand).Methods("POST")

	// File transfer
	api.HandleFunc("/sandboxes/{id}/files/", s.handleListFiles).Methods("GET")
	api.HandleFunc("/sandboxes/{id}/files/{path:.*}", s.handleGetFile).Methods("GET")
	api.HandleFunc("/sandboxes/{id}/files/{path:.*}", s.handlePutFile).Methods("PUT")

	// Port forwarding
	api.HandleFunc("/sandboxes/{id}/ports", s.handleListPorts).Methods("GET")
	api.HandleFunc("/sandboxes/{id}/ports", s.handleForwardPort).Methods("POST")
	api.HandleFunc("/sandboxes/{id}/ports/{port}", s.handleRemovePort).Methods("DELETE")
	// Authenticated HTTP proxy into a forwarded port. Works even without a
	// public Ingress — useful for dev clusters and end-to-end testing.
	api.PathPrefix("/sandboxes/{id}/preview/{port}/").HandlerFunc(s.handlePortPreview)
	api.HandleFunc("/sandboxes/{id}/preview/{port}", s.handlePortPreview)

	// Sharing
	api.HandleFunc("/sandboxes/{id}/share", s.handleGetSharing).Methods("GET")
	api.HandleFunc("/sandboxes/{id}/share", s.handleShareSandbox).Methods("POST")
	api.HandleFunc("/sandboxes/{id}/share/{userId}", s.handleRevokeShare).Methods("DELETE")
	api.HandleFunc("/sandboxes/{id}/share-links", s.handleCreateShareLink).Methods("POST")

	// Templates
	api.HandleFunc("/templates", s.handleListTemplates).Methods("GET")
	api.HandleFunc("/templates", s.handleCreateTemplate).Methods("POST")
	api.HandleFunc("/templates/{name}", s.handleGetTemplate).Methods("GET")
	api.HandleFunc("/templates/{name}", s.handleUpdateTemplate).Methods("PUT")
	api.HandleFunc("/templates/{name}", s.handleDeleteTemplate).Methods("DELETE")

	// Governance
	api.HandleFunc("/governance/policies", s.handleListPolicies).Methods("GET")
	api.Handle("/governance/policies", s.requireAdmin(http.HandlerFunc(s.handleUpsertClusterPolicy))).Methods("PUT")
	api.HandleFunc("/governance/policies/{namespace}", s.handleGetPolicy).Methods("GET")
	api.Handle("/governance/policies/{namespace}", s.requireAdmin(http.HandlerFunc(s.handleSetPolicy))).Methods("PUT")
	api.Handle("/governance/policies/{namespace}", s.requireAdmin(http.HandlerFunc(s.handleDeletePolicy))).Methods("DELETE")
	api.HandleFunc("/governance/effective", s.handleGetEffectivePolicy).Methods("GET")

	// Audit
	api.HandleFunc("/audit/events", s.handleListAuditEvents).Methods("GET")

	// Analytics
	api.HandleFunc("/analytics/usage", s.handleGetUsageAnalytics).Methods("GET")
	api.HandleFunc("/analytics/costs", s.handleGetCostEstimates).Methods("GET")

	// Admin
	api.HandleFunc("/admin/sandboxes", s.handleAdminListSandboxes).Methods("GET")
	api.HandleFunc("/admin/sharing", s.handleAdminListSharing).Methods("GET")

	// User preferences
	api.HandleFunc("/user/me", s.handleGetMe).Methods("GET")
	api.HandleFunc("/user/preferences", s.handleGetPreferences).Methods("GET")
	api.HandleFunc("/user/preferences", s.handleUpdatePreferences).Methods("PUT")
	api.HandleFunc("/user/api-keys", s.handleListAPIKeys).Methods("GET")
	api.HandleFunc("/user/api-keys", s.handleCreateAPIKey).Methods("POST")
	api.HandleFunc("/user/api-keys/{keyId}", s.handleRevokeAPIKey).Methods("DELETE")

	// Warm pool
	api.HandleFunc("/warmpool/status", s.handleGetWarmPoolStatus).Methods("GET")
	api.HandleFunc("/warmpool/config", s.handleSetWarmPoolConfig).Methods("PUT")

	// WebSocket terminal (auth handled inside handler)
	r.HandleFunc("/ws/terminal/{sandboxId}", s.handleTerminalWebSocket)

	// Agent-mode endpoints (POST /sandboxes/{id}/configure today; /invoke
	// in the next milestone). Lives in pkg/router/agent so it can evolve
	// independently of the interactive code-mode surface.
	agentHandler := agent.New(agent.Options{
		K8sClient:    s.k8sClient,
		Bridge:       s.bridge,
		Logger:       s.logger,
		ClaimsLookup: s.agentClaims,
		SandboxOf:    s.agentSandboxOf,
		PolicyOf: func(ctx context.Context, namespace string) (governance.Policy, error) {
			if s.governanceStore == nil {
				return governance.Policy{}, nil
			}
			return governance.Resolve(ctx, s.governanceStore, namespace)
		},
	})
	agentHandler.RegisterRoutes(api)

	s.httpServer = &http.Server{
		Addr:    config.ListenAddr,
		Handler: r,
		// ReadTimeout: 0 is required so WebSocket upgrades can hold a
		// connection open beyond any HTTP read deadline. But a global
		// zero ReadTimeout exposes every endpoint (REST, SSE, file
		// PUT/GET) to slowloris — a client can dribble bytes out
		// indefinitely and pin a goroutine + fd.
		//
		// ReadHeaderTimeout bounds the request-line + headers phase
		// only and is allowed alongside ReadTimeout: 0. Five seconds
		// is enough for any legitimate client (curl over a slow
		// network finishes headers in well under a second) and short
		// enough that slowloris can't hold a connection waiting for
		// header data. WebSocket upgrades complete their handshake
		// within this window, then escape into the long-lived
		// hijacked path.
		ReadTimeout:       0,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s
}

// Start begins serving HTTP requests and blocks until shutdown.
func (s *Server) Start() error {
	// Graceful shutdown on SIGTERM/SIGINT
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		s.logger.Info("router starting", "addr", s.config.ListenAddr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	s.logger.Info("shutting down gracefully")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.httpServer.Shutdown(ctx)
}

// --- Health endpoints ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	// Try a cheap, unprivileged read against the API server cache. If the
	// Router has lost its connection to the K8s API (network partition,
	// service-account token rotation glitch, kubelet churn) every real
	// endpoint will return 5xx; reporting healthy here would keep the
	// Service routing traffic to a broken pod.
	//
	// Limit=1 keeps the round-trip tiny. We use a short context timeout
	// so a hung apiserver can't pin this handler — Kubernetes fails the
	// probe and reroutes traffic.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	list := &agenttierv1alpha1.SandboxList{}
	if err := s.k8sClient.List(ctx, list, client.Limit(1)); err != nil {
		s.logger.Warn("readyz: K8s API unreachable", "error", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "not ready: %v", err)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// agentClaims adapts the Router's request-scoped Claims into the minimal
// shape the agent subpackage consumes. Returns nil when no claims are set
// (handler returns 401).
func (s *Server) agentClaims(r *http.Request) *agent.Claims {
	c := GetClaims(r.Context())
	if c == nil {
		return nil
	}
	return &agent.Claims{Sub: c.Sub, Email: c.Email, Name: c.Name, IsAdmin: c.IsAdmin}
}

// agentSandboxOf reuses the existing ownership-aware sandbox lookup so the
// agent subpackage doesn't reimplement RBAC. The agent.Claims received here
// must round-trip through a router.Claims for the helper to apply admin /
// ownership rules consistently.
func (s *Server) agentSandboxOf(ctx context.Context, sandboxID string, ac *agent.Claims) (*agenttierv1alpha1.Sandbox, error) {
	if ac == nil {
		return nil, fmt.Errorf("authentication required")
	}
	rc := &Claims{Sub: ac.Sub, Email: ac.Email, Name: ac.Name, IsAdmin: ac.IsAdmin}
	return s.getSandboxWithAuthCheck(ctx, sandboxID, rc)
}
