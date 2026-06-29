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
//
// The Setup function is the only entrypoint mains care about: call it once
// at startup, defer the returned shutdown func, and any tracer.Start(...)
// call elsewhere in the process exports to the configured OTLP collector.
//
// When OTEL_EXPORTER_OTLP_ENDPOINT is empty (the default), Setup installs
// a noop tracer provider — calls into the global tracer succeed cheaply
// and produce no exporter traffic. That's the path most operators run on,
// so we keep it allocation-free.
package otel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// Config carries OTel runtime knobs. Callers can build it manually or via
// LoadConfigFromEnv when they want the standard env-var-driven shape.
type Config struct {
	// ServiceName is required (e.g. "agenttier-router", "agenttier-controller").
	ServiceName string
	// ServiceVersion is the build-time version (pkg/version.Version).
	ServiceVersion string
	// OTLPEndpoint is the gRPC endpoint of the OTel collector. Empty
	// means "no exporter; install a noop tracer." Format: "host:port"
	// (no scheme), matching the OTLP spec.
	OTLPEndpoint string
	// Insecure controls whether to dial the collector with WithInsecure().
	// Almost always true in-cluster (collector sidecar/sibling on a
	// ClusterIP service); operators with a TLS-fronted collector can
	// flip this off.
	Insecure bool
}

// LoadConfigFromEnv builds a Config using the OpenTelemetry-spec env vars
// the Helm chart already plumbs into the router and controller pods.
//
// Recognized vars:
//
//   - OTEL_EXPORTER_OTLP_ENDPOINT — gRPC endpoint, format "host:port" or
//     "http://host:port" (the http:// scheme is normalized away because
//     the gRPC dialer doesn't accept it).
//   - OTEL_EXPORTER_OTLP_INSECURE — "true" enables WithInsecure(). Default
//     true; flip to "false" for a TLS-fronted collector.
func LoadConfigFromEnv(serviceName, serviceVersion string) Config {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	// The OTLP spec lets users write "http://collector:4317" but the
	// gRPC dialer treats the scheme as part of the host, so strip it.
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	insecure := true
	if v := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_INSECURE")); v != "" {
		insecure = strings.EqualFold(v, "true") || v == "1"
	}

	return Config{
		ServiceName:    serviceName,
		ServiceVersion: serviceVersion,
		OTLPEndpoint:   endpoint,
		Insecure:       insecure,
	}
}

// Setup initializes the global tracer provider and propagator. The returned
// shutdown function flushes any in-flight spans and closes the exporter;
// callers should defer it before main exits.
//
// When cfg.OTLPEndpoint is empty, Setup installs the SDK with no exporter
// (i.e. spans are recorded but dropped). That's intentional: we still want
// trace IDs flowing into log output (see SlogContextHandler) so an operator
// who later turns the exporter on can correlate historical logs with new
// spans. The cost is ~one allocation per span, which is fine — the
// AlwaysSample sampler is not in play; we use NeverSample when no exporter
// is configured.
func Setup(ctx context.Context, cfg Config, logger *slog.Logger) (func(context.Context) error, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build OTel resource: %w", err)
	}

	// Propagator is unconditional: even when we're not exporting, we want
	// to honor incoming W3C Trace Context headers so a parent span ID
	// from an upstream caller (curl with traceparent, another microservice)
	// shows up in our log lines. Same for Baggage — operators sometimes
	// stamp tenant IDs on baggage at the gateway.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	if cfg.OTLPEndpoint == "" {
		// No exporter configured. Install an SDK provider with NeverSample
		// so tracer.Start is cheap, but keep the resource attached so the
		// IDs that DO surface (e.g. via OTel-aware HTTP middleware) are
		// well-formed and pickable up by log correlation.
		tp := sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.NeverSample()),
		)
		otel.SetTracerProvider(tp)
		logger.Info("OpenTelemetry tracer initialized without exporter (OTEL_EXPORTER_OTLP_ENDPOINT unset)",
			"service", cfg.ServiceName)
		return tp.Shutdown, nil
	}

	exporterOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.Insecure {
		exporterOpts = append(exporterOpts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("create OTLP exporter (%s): %w", cfg.OTLPEndpoint, err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)

	logger.Info("OpenTelemetry initialized",
		"service", cfg.ServiceName,
		"endpoint", cfg.OTLPEndpoint,
		"insecure", cfg.Insecure,
	)

	return tp.Shutdown, nil
}

// Tracer returns a named tracer for the given component. Callers should
// pass a stable name like "agenttier-router/agent" so spans bucket cleanly
// in the collector.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// SpanFromContext extracts the current span from context.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// HashActor returns a stable, non-reversible 8-hex-char digest of an
// identifier suitable for OTel span attributes. The output is short
// enough to keep cardinality bounded (sub-billion possible buckets) and
// long enough to correlate within a single investigation window without
// being PII.
//
// Empty input returns the empty string so callers can omit the attribute
// entirely when no actor is known.
func HashActor(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:4])
}
