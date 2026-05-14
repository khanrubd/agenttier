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
	"log/slog"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/controller"
	"github.com/agenttier/agenttier/pkg/controller/warmpool"
	"github.com/agenttier/agenttier/pkg/version"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(agenttierv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr             string
		healthProbeAddr         string
		leaderElect             bool
		maxConcurrency          int
		defaultImage            string
		defaultStorageSize      string
		defaultMountPath        string
		agentMemorySidecarImage string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8081", "The address the metrics endpoint binds to.")
	flag.StringVar(&healthProbeAddr, "health-probe-bind-address", ":8082", "The address the health probe endpoint binds to.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for controller manager.")
	flag.IntVar(&maxConcurrency, "max-concurrency", 10, "Maximum number of concurrent reconciles.")
	flag.StringVar(&defaultImage, "default-image", "ghcr.io/agenttier/sandbox-general:latest", "Default sandbox container image.")
	flag.StringVar(&defaultStorageSize, "default-storage-size", "10Gi", "Default PVC storage size.")
	flag.StringVar(&defaultMountPath, "default-mount-path", "/workspace", "Default PVC mount path.")
	flag.StringVar(&agentMemorySidecarImage, "agent-memory-sidecar-image", "",
		"When set, every mode: agent sandbox Pod gains a mem0 sidecar at "+
			"this image and MEM0_BASE_URL=http://localhost:11434 is "+
			"injected into the sandbox container. Empty = feature off.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("starting AgentTier controller",
		"version", version.Version,
		"commit", version.GitCommit,
	)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: healthProbeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "agenttier-controller-leader",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Register the Sandbox reconciler
	if err := (&controller.SandboxReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Recorder:                mgr.GetEventRecorderFor("agenttier-controller"),
		MaxConcurrency:          maxConcurrency,
		DefaultImage:            defaultImage,
		DefaultStorageSize:      defaultStorageSize,
		DefaultMountPath:        defaultMountPath,
		AgentMemorySidecarImage: agentMemorySidecarImage,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Sandbox")
		os.Exit(1)
	}

	// Health and readiness probes
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	// Start warm pool reconciler (runs only on the leader, respects leader election)
	wpLogger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	wpReconciler := warmpool.NewReconciler(mgr.GetClient(), wpLogger)
	if err := mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
		wpReconciler.RunLoop(ctx)
		return nil
	})); err != nil {
		setupLog.Error(err, "unable to add warm pool reconciler")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

	fmt.Println("controller stopped")
}
