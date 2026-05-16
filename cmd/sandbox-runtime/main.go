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

// Command sandbox-runtime is the in-pod HTTP server every sandbox container
// runs alongside (or before) the user's shell or agent entrypoint. The
// Router proxies /exec, /files, and /invoke requests to it instead of
// going through SPDY exec via the API server.
//
// This binary is baked into every sandbox image and started as PID 1's
// background companion (Phase 2). The Router is the only authorized
// caller; auth is bearer-token via a per-sandbox secret the controller
// injects as the AGENTTIER_RUNTIME_TOKEN env var (Phase 3).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/agenttier/agenttier/pkg/sandboxruntime"
	"github.com/agenttier/agenttier/pkg/version"
)

func main() {
	var (
		listenAddr  string
		showVersion bool
	)
	flag.StringVar(&listenAddr, "listen-addr", sandboxruntime.DefaultListenAddr,
		"host:port the runtime HTTP server binds to")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("agenttier sandbox-runtime %s (%s)\n", version.Version, version.GitCommit)
		os.Exit(0)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("sandbox runtime starting",
		"version", version.Version,
		"listen", listenAddr,
	)

	// Auth token comes from the env. Empty disables auth (test-only path
	// — production deployments always inject AGENTTIER_RUNTIME_TOKEN via
	// a per-sandbox Secret in Phase 3). We don't read it at flag parsing
	// time so an operator never has to put the secret on a command line.
	token := os.Getenv("AGENTTIER_RUNTIME_TOKEN")
	if token == "" {
		logger.Warn("AGENTTIER_RUNTIME_TOKEN is not set — runtime will accept unauthenticated requests; never run this configuration in production")
	}

	srv := sandboxruntime.New(sandboxruntime.Config{
		ListenAddr: listenAddr,
		AuthToken:  token,
		Logger:     logger,
	})

	// Handle SIGTERM (kubelet shutdown) and SIGINT (local Ctrl-C) by
	// cancelling the server context, which triggers graceful drain.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := srv.Start(ctx); err != nil {
		logger.Error("runtime server exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("sandbox runtime stopped")
}
