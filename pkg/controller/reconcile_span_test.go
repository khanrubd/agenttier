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

package controller

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TestReconcile_EmitsOTelSpan verifies the controller emits a
// controller.reconcile_sandbox span per reconcile, with the sandbox
// name/namespace/phase attributes — so a sandbox can be traced
// controller→router→pod (tracing used to be router-only).
func TestReconcile_EmitsOTelSpan(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("client-go scheme: %v", err)
	}
	if err := agenttierv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("agenttier scheme: %v", err)
	}

	// Finalizer already present + a terminal phase, so dispatch runs cleanly
	// past the finalizer/creation paths and the span gets a phase attribute.
	sandbox := &agenttierv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sb-trace",
			Namespace:  "team-a",
			Finalizers: []string{FinalizerName},
		},
		Status: agenttierv1alpha1.SandboxStatus{Phase: agenttierv1alpha1.SandboxPhaseStopped},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(sandbox).
		WithStatusSubresource(&agenttierv1alpha1.Sandbox{}).
		Build()
	r := &SandboxReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "sb-trace", Namespace: "team-a"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var found bool
	for _, s := range sr.Ended() {
		if s.Name() != "controller.reconcile_sandbox" {
			continue
		}
		found = true
		attrs := map[string]string{}
		for _, kv := range s.Attributes() {
			attrs[string(kv.Key)] = kv.Value.AsString()
		}
		if attrs["sandbox.name"] != "sb-trace" || attrs["sandbox.namespace"] != "team-a" {
			t.Errorf("span missing name/namespace attrs: %+v", attrs)
		}
		if attrs["sandbox.phase"] != string(agenttierv1alpha1.SandboxPhaseStopped) {
			t.Errorf("span phase attr = %q, want %q", attrs["sandbox.phase"], agenttierv1alpha1.SandboxPhaseStopped)
		}
	}
	if !found {
		t.Fatal("no controller.reconcile_sandbox span was recorded")
	}
}
