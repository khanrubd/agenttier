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

// Package sandboxruntime implements the in-pod HTTP server every sandbox
// container runs in addition to the user's shell or agent entrypoint.
//
// **Why this exists.** The Router today reaches every sandbox via SPDY
// exec — kubectl-style streaming through the API server and kubelet —
// for /invoke, /exec, file PUT/GET, and the terminal WebSocket. That
// works at small scale but puts the API server in the hot path of every
// request and makes cross-replica cancel impossible (a per-Router
// in-process registry can't see invokes from other replicas). Replacing
// it with a small HTTP server inside the pod lets the Router proxy
// directly, removes apiserver from the request path, and makes cancel
// semantics naturally cross-replica because the cancel hits the pod
// itself.
//
// **Phase 1 scope (this file).** Bootstraps the server with /healthz +
// /exec endpoints, bearer-token auth, and graceful shutdown. The binary
// isn't yet baked into any sandbox image and the Router has no client.
// Pure foundation — zero risk to existing sandboxes.
package sandboxruntime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DefaultListenAddr is the wire-level bind address the runtime defaults to.
// The pod's network namespace is shared with the cluster — NetworkPolicy
// (added in Phase 3) restricts which peers can dial port 9000, so listening
// on 0.0.0.0 is the right call: localhost-only would prevent the Router
// from connecting at all, and binding to a Pod IP requires runtime DNS
// resolution that complicates startup.
const DefaultListenAddr = "0.0.0.0:9000"

// DefaultShutdownTimeout caps how long graceful shutdown waits for in-flight
// requests to drain before forcing close. 5 seconds matches Kubernetes'
// default `terminationGracePeriodSeconds` window for typical pods; longer
// would be ignored by the kubelet anyway.
const DefaultShutdownTimeout = 5 * time.Second

// Config configures the runtime server.
type Config struct {
	// ListenAddr is the host:port the HTTP server binds to. Empty defaults
	// to DefaultListenAddr.
	ListenAddr string

	// AuthToken is the bearer token clients must present on every
	// authenticated request. Empty disables auth — only useful for
	// integration tests; production deployments must always set this.
	AuthToken string

	// Logger is the structured logger the server writes to. Nil falls back
	// to slog.Default().
	Logger *slog.Logger

	// ShutdownTimeout caps graceful-shutdown drain time. Zero defaults to
	// DefaultShutdownTimeout.
	ShutdownTimeout time.Duration
}

// Server is the in-pod HTTP server.
type Server struct {
	cfg        Config
	httpServer *http.Server
	executor   commandExecutor
	logger     *slog.Logger
}

// commandExecutor is the abstraction the /exec handler uses to run user
// commands. The default implementation in exec.go shells out via
// os/exec.Command. Tests inject a stub.
type commandExecutor interface {
	Execute(ctx context.Context, req ExecRequest) ExecResponse
}

// New constructs a Server with the given configuration. Caller still has
// to call Start.
func New(cfg Config) *Server {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = DefaultShutdownTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		cfg:      cfg,
		executor: defaultExecutor{},
		logger:   logger,
	}

	mux := http.NewServeMux()
	// /healthz is intentionally unauthenticated. kubelet's liveness probe
	// dials it without a token and a kubelet that can't reach a known-good
	// path would restart the whole pod — losing user state.
	mux.HandleFunc("/healthz", s.handleHealthz)
	// /exec runs user commands; auth is mandatory.
	mux.HandleFunc("/exec", s.requireAuth(s.handleExec))

	s.httpServer = &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
		// Match the Router's slowloris-defense posture — bound the
		// header phase but leave full-body reads at the handler level
		// so streamed exec stdin (Phase 4) works correctly.
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return s
}

// SetExecutor replaces the command executor. Tests use this to inject a
// stub; production code should leave the default.
func (s *Server) SetExecutor(e commandExecutor) {
	s.executor = e
}

// Start begins serving. Blocks until ctx is cancelled or the server errors,
// then drains in-flight requests and returns. Returns nil on a clean
// shutdown (the caller's context cancellation).
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("sandbox runtime listening", "addr", s.cfg.ListenAddr)
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		s.logger.Info("sandbox runtime shutting down")
		if err := s.httpServer.Shutdown(shutCtx); err != nil {
			s.logger.Warn("graceful shutdown timed out", "error", err)
			return err
		}
		return nil
	}
}

// requireAuth wraps a handler so requests without a valid bearer token get
// a 401. Constant-time comparison via crypto/subtle to avoid timing
// side channels on the token.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Empty AuthToken disables auth — test-only path.
		if s.cfg.AuthToken == "" {
			next(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		got := strings.TrimPrefix(auth, prefix)
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.AuthToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next(w, r)
	}
}

// handleHealthz is the unauthenticated liveness probe.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// writeError emits a structured error response. Stable wire format:
// `{"error": "...", "code": N}`.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": msg,
		"code":  status,
	})
}
