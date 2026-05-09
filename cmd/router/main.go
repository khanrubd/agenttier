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
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/router"
	"github.com/agenttier/agenttier/pkg/router/terminal"
	"github.com/agenttier/agenttier/pkg/version"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(agenttierv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		listenAddr    string
		oidcIssuer    string
		oidcClientID  string
		adminGroup    string
		groupClaim    string
		previewDomain string
		showVersion   bool
	)

	flag.StringVar(&listenAddr, "listen-addr", ":8080", "HTTP listen address")
	flag.StringVar(&oidcIssuer, "oidc-issuer", "", "OIDC issuer URL")
	flag.StringVar(&oidcClientID, "oidc-client-id", "", "OIDC client ID")
	flag.StringVar(&adminGroup, "admin-group", "agenttier-admins", "Admin group name")
	flag.StringVar(&groupClaim, "group-claim", "groups", "JWT claim for groups")
	flag.StringVar(&previewDomain, "preview-domain", "", "Domain for port forwarding preview URLs")
	flag.BoolVar(&showVersion, "version", false, "Print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("AgentTier Router %s (%s)\n", version.Version, version.GitCommit)
		os.Exit(0)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("starting AgentTier Router", "version", version.Version)

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
	config := &router.Config{
		ListenAddr:    listenAddr,
		OIDCIssuerURL: oidcIssuer,
		OIDCClientID:  oidcClientID,
		AdminGroup:    adminGroup,
		GroupClaim:    groupClaim,
		PreviewDomain: previewDomain,
	}

	server := router.NewServer(config, k8sClient, bridge)
	if err := server.Start(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
