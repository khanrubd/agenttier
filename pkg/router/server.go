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
	"github.com/agenttier/agenttier/pkg/router/terminal"
)

// Config holds the Router server configuration.
type Config struct {
	ListenAddr    string
	MetricsAddr   string
	OIDCIssuerURL string
	OIDCClientID  string
	AdminGroup    string
	GroupClaim    string
	PreviewDomain string
	GatewayName   string
	KubeConfig    string
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
	}

	// Register middleware
	r.Use(s.requestIDMiddleware)
	r.Use(s.corsMiddleware)
	r.Use(s.loggingMiddleware)

	// Health and metrics (no auth required)
	r.HandleFunc("/healthz", s.handleHealthz).Methods("GET")
	r.HandleFunc("/readyz", s.handleReadyz).Methods("GET")
	r.HandleFunc("/metrics", promhttp.Handler().ServeHTTP).Methods("GET")

	// API routes (auth required)
	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(s.authMiddleware)

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

	s.httpServer = &http.Server{
		Addr:        config.ListenAddr,
		Handler:     r,
		ReadTimeout: 0, // Disabled for WebSocket support
		IdleTimeout: 120 * time.Second,
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
	// TODO: Check K8s API reachability
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}
