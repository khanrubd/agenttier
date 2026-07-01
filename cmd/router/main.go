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

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/controller/warmpool"
	agentotel "github.com/agenttier/agenttier/pkg/otel"
	"github.com/agenttier/agenttier/pkg/router"
	"github.com/agenttier/agenttier/pkg/router/terminal"
	"github.com/agenttier/agenttier/pkg/storage"
	"github.com/agenttier/agenttier/pkg/version"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(agenttierv1alpha1.AddToScheme(scheme))
	// VolumeSnapshot CRDs from external-snapshotter — needed so the
	// Router can create + read VolumeSnapshot objects when cloning a
	// sandbox (POST /sandboxes/{id}/clone).
	utilruntime.Must(snapshotv1.AddToScheme(scheme))
}

func main() {
	var (
		listenAddr         string
		oidcIssuer         string
		oidcClientID       string
		adminGroup         string
		groupClaim         string
		previewDomain      string
		ingressClassName   string
		namespace          string
		sandboxNamespace   string
		rateLimitPerIP     float64
		rateLimitPerUser   float64
		rateLimitTrustXFF  bool
		corsAllowedOrigins string
		devAuth            bool
		showVersion        bool
	)

	flag.StringVar(&listenAddr, "listen-addr", ":8080", "HTTP listen address")
	flag.StringVar(&oidcIssuer, "oidc-issuer", "", "OIDC issuer URL")
	flag.StringVar(&oidcClientID, "oidc-client-id", "", "OIDC client ID")
	flag.StringVar(&adminGroup, "admin-group", "agenttier-admins", "Admin group name")
	flag.StringVar(&groupClaim, "group-claim", "groups", "JWT claim for groups")
	flag.StringVar(&previewDomain, "preview-domain", "", "Domain for port forwarding preview URLs")
	flag.StringVar(&ingressClassName, "ingress-class-name", "", "Ingress class to use for port-forward Ingresses (empty = cluster default)")
	// Install namespace — drives where the warm pool ConfigMap lives so the
	// Router reads/writes the same ConfigMap the controller reconciles.
	// Defaults to POD_NAMESPACE env var (set via downward API in the Helm
	// chart). Empty falls back to the warm pool's DefaultNamespace.
	flag.StringVar(&namespace, "namespace", os.Getenv("POD_NAMESPACE"),
		"Namespace where AgentTier is installed. Defaults to POD_NAMESPACE env var.")
	// Sandbox namespace — where Sandboxes (and warm pool Pods) live. Used to
	// report warm pool status from the correct namespace. Defaults to the
	// SANDBOX_NAMESPACE env var, then "default".
	flag.StringVar(&sandboxNamespace, "sandbox-namespace", os.Getenv("SANDBOX_NAMESPACE"),
		"Namespace where Sandboxes (and warm pool Pods) live. Defaults to SANDBOX_NAMESPACE env var, then \"default\".")
	// Rate limiting — zero disables. Defaults match the Helm values that
	// chart authors most likely want when they enable it (60 req/min/IP,
	// 600 req/min/user). Values are set explicitly via flags rather than
	// inferred at runtime so an operator can verify the running config
	// from `kubectl describe pod`.
	flag.Float64Var(&rateLimitPerIP, "ratelimit-per-ip-rate", 0,
		"Steady-state per-client-IP request rate (req/sec). 0 disables IP-level rate limiting.")
	flag.Float64Var(&rateLimitPerUser, "ratelimit-per-user-rate", 0,
		"Steady-state per-authenticated-user request rate (req/sec). 0 disables user-level rate limiting.")
	// Trust X-Forwarded-For/X-Real-IP for the per-IP limiter key. OFF by
	// default (secure) so a forged header can't mint a fresh bucket per
	// request; enable only when behind a trusted proxy/LB (e.g. the ALB).
	flag.BoolVar(&rateLimitTrustXFF, "ratelimit-trust-forwarded-headers",
		os.Getenv("RATE_LIMIT_TRUST_FORWARDED_HEADERS") == "true",
		"Trust X-Forwarded-For/X-Real-IP for per-IP rate limiting. Enable only behind a trusted proxy/LB.")
	// CORS allowed origins — comma-separated list of origins that may make
	// cross-origin requests. The incoming Origin header is reflected only
	// when it exactly matches one of these values. An empty list disables
	// CORS. Never use "*" — the API accepts auth headers.
	// Also honored via AGENTTIER_CORS_ALLOWED_ORIGINS (same CSV format).
	corsDefault := os.Getenv("AGENTTIER_CORS_ALLOWED_ORIGINS")
	flag.StringVar(&corsAllowedOrigins, "cors-allowed-origins", corsDefault,
		"Comma-separated list of allowed CORS origins (e.g. https://dashboard.example.com). Empty disables CORS.")
	// Dev-auth — explicit opt-in to bypass authentication and treat every
	// request as admin. OFF by default so a prod install that forgot to set
	// an OIDC issuer fails closed (401) rather than open. Also honors
	// AGENTTIER_DEV_AUTH=true for convenience in docker-compose / kind.
	flag.BoolVar(&devAuth, "dev-auth", os.Getenv("AGENTTIER_DEV_AUTH") == "true",
		"DANGER: bypass authentication and treat every request as admin. Local development only — never set in production.")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.Parse()

	// Default the sandbox namespace to where Sandboxes are created
	// ("default") when neither the flag nor SANDBOX_NAMESPACE env is set.
	if sandboxNamespace == "" {
		sandboxNamespace = warmpool.DefaultSandboxNamespace
	}

	if showVersion {
		fmt.Printf("AgentTier Router %s (%s)\n", version.Version, version.GitCommit)
		os.Exit(0)
	}

	logger := slog.New(agentotel.NewSlogContextHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
	))
	logger.Info("starting AgentTier Router", "version", version.Version)

	// Initialize OpenTelemetry. Reads OTEL_EXPORTER_OTLP_ENDPOINT from
	// the environment (Helm chart sets it when observability.otlp.endpoint
	// is non-empty). When the env var is unset, Setup installs a NeverSample
	// provider — cheap, no exports, and trace context still propagates so
	// later turns of the export knob don't require a restart-and-redeploy
	// to verify.
	otelShutdownCtx, otelShutdownCancel := context.WithCancel(context.Background())
	defer otelShutdownCancel()
	otelShutdown, err := agentotel.Setup(otelShutdownCtx,
		agentotel.LoadConfigFromEnv("agenttier-router", version.Version),
		logger)
	if err != nil {
		log.Fatalf("OpenTelemetry setup failed: %v", err)
	}
	defer func() {
		// 5s is more than enough for the BatchSpanProcessor to drain
		// — and bounded so a stuck collector can't pin shutdown.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelShutdown(ctx); err != nil {
			logger.Warn("OTel shutdown returned an error", "err", err)
		}
	}()

	// Initialize Kubernetes client
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to kubeconfig for local development
		restConfig = ctrl.GetConfigOrDie()
	}

	k8sClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		log.Fatalf("failed to create K8s client: %v", err)
	}

	// Initialize Kubernetes clientset (for exec)
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Fatalf("failed to create K8s clientset: %v", err)
	}

	// Initialize terminal bridge
	bridge := terminal.NewBridge(clientset, restConfig, logger)

	// Create and start server
	rateLimitCfg := router.RateLimitConfig{
		PerIPRate:             rateLimitPerIP,
		PerUserRate:           rateLimitPerUser,
		PerIPBurst:            30,  // burst sized for typical web-UI traffic
		PerUserBurst:          100, // generous so an interactive admin doesn't trip
		TrustForwardedHeaders: rateLimitTrustXFF,
	}
	// Parse the comma-separated CORS origins into a slice. Empty string →
	// empty slice → CORS disabled (no Access-Control-Allow-Origin emitted).
	var parsedCORSOrigins []string
	if corsAllowedOrigins != "" {
		for _, o := range strings.Split(corsAllowedOrigins, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				parsedCORSOrigins = append(parsedCORSOrigins, trimmed)
			}
		}
	}

	config := &router.Config{
		ListenAddr:         listenAddr,
		OIDCIssuerURL:      oidcIssuer,
		OIDCClientID:       oidcClientID,
		AdminGroup:         adminGroup,
		GroupClaim:         groupClaim,
		PreviewDomain:      previewDomain,
		IngressClassName:   ingressClassName,
		InstallNamespace:   namespace,
		SandboxNamespace:   sandboxNamespace,
		CORSAllowedOrigins: parsedCORSOrigins,
		DevAuth:            devAuth,
		RateLimit:          rateLimitCfg,
	}

	// Optional SQL historical-records backend. Off by default — Kubernetes
	// Events stay the source of truth. When STORAGE_SQLITE_PATH is set we
	// open the bundled pure-Go SQLite store; this is best-effort, so a
	// failure logs and the Router continues with the no-op backend (graceful
	// degradation, never blocks startup). Postgres/MySQL are bring-your-own:
	// an operator wires storage.NewSQLBackend with their driver here.
	if sqlitePath := os.Getenv("STORAGE_SQLITE_PATH"); sqlitePath != "" {
		backendCtx, backendCancel := context.WithTimeout(context.Background(), 10*time.Second)
		store, err := storage.OpenSQLite(backendCtx, sqlitePath)
		backendCancel()
		if err != nil {
			logger.Warn("failed to open SQLite storage backend; continuing with no-op backend",
				"path", sqlitePath, "error", err)
		} else {
			config.StorageBackend = store
			logger.Info("SQLite storage backend enabled", "path", sqlitePath)
		}
	}

	server := router.NewServer(config, k8sClient, bridge)
	if err := server.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
