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

// Package otel provides OpenTelemetry instrumentation helpers for AgentTier.
package otel

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Config holds OpenTelemetry configuration.
type Config struct {
	ServiceName    string
	ServiceVersion string
	OTLPEndpoint   string // gRPC endpoint (e.g., "otel-collector:4317")
	Enabled        bool
}

// Setup initializes OpenTelemetry tracing and returns a shutdown function.
// If OTLPEndpoint is empty, a no-op tracer is used.
func Setup(ctx context.Context, cfg *Config, logger *slog.Logger) (func(context.Context) error, error) {
	if !cfg.Enabled || cfg.OTLPEndpoint == "" {
		logger.Info("OpenTelemetry disabled (no OTLP endpoint configured)")
		return func(ctx context.Context) error { return nil }, nil
	}

	// Create resource with service metadata
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel resource: %w", err)
	}

	// Create OTLP trace exporter
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlptracegrpc.WithInsecure(), // TODO: Make TLS configurable
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set global trace provider and propagator
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("OpenTelemetry initialized",
		"service", cfg.ServiceName,
		"endpoint", cfg.OTLPEndpoint,
	)

	// Return shutdown function
	return tp.Shutdown, nil
}

// Tracer returns a named tracer for the given component.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// SpanFromContext extracts the current span from context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}
