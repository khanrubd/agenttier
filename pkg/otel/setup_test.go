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

package otel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestLoadConfigFromEnv_StripsScheme(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"otel-collector:4317", "otel-collector:4317"},
		{"http://otel-collector:4317", "otel-collector:4317"},
		{"https://collector.example.com:4317", "collector.example.com:4317"},
		{"  otel-collector:4317  ", "otel-collector:4317"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.raw)
		got := LoadConfigFromEnv("svc", "v0").OTLPEndpoint
		if got != tc.want {
			t.Errorf("LoadConfigFromEnv(%q) = %q; want %q", tc.raw, got, tc.want)
		}
	}
}

func TestLoadConfigFromEnv_InsecureFlag(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "host:4317")

	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")
	if got := LoadConfigFromEnv("svc", "v0").Insecure; got != true {
		t.Errorf("default insecure = %v; want true", got)
	}

	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")
	if got := LoadConfigFromEnv("svc", "v0").Insecure; got != false {
		t.Errorf("Insecure with 'false' = %v; want false", got)
	}

	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "TRUE")
	if got := LoadConfigFromEnv("svc", "v0").Insecure; got != true {
		t.Errorf("Insecure with 'TRUE' = %v; want true", got)
	}
}

func TestSetup_NoEndpoint_InstallsNoExportProvider(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	shutdown, err := Setup(context.Background(), Config{
		ServiceName:    "test",
		ServiceVersion: "0",
		OTLPEndpoint:   "",
	}, logger)
	if err != nil {
		t.Fatalf("Setup returned error: %v", err)
	}
	defer shutdown(context.Background())

	// Tracer should be functional but record nothing exporter-side.
	tr := otel.Tracer("test-tracer")
	_, span := tr.Start(context.Background(), "test-span")
	span.End()
	// We can't directly assert "no export happened" without an exporter,
	// but we CAN assert the call doesn't panic. NeverSample produces
	// unsampled spans whose context is not flagged as IsSampled — that's
	// fine for our use case; we only care that the SDK didn't refuse to
	// install or panic on Start.
	_ = span
}

func TestHashActor(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"alice", "2bd806c9"},
		{"alice", "2bd806c9"}, // stable
	}
	for _, tc := range cases {
		got := HashActor(tc.in)
		if got != tc.want {
			t.Errorf("HashActor(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlogContextHandler_AddsTraceIDs(t *testing.T) {
	// Use an in-memory exporter so spans get a valid SpanContext.
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(otel.GetTracerProvider())

	var buf bytes.Buffer
	handler := NewSlogContextHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger := slog.New(handler)

	tr := otel.Tracer("test")
	ctx, span := tr.Start(context.Background(), "test-span")
	wantTraceID := span.SpanContext().TraceID().String()
	wantSpanID := span.SpanContext().SpanID().String()

	logger.LogAttrs(ctx, slog.LevelInfo, "hello")
	span.End()

	out := buf.String()
	if !strings.Contains(out, wantTraceID) {
		t.Errorf("log output missing trace_id %q: %s", wantTraceID, out)
	}
	if !strings.Contains(out, wantSpanID) {
		t.Errorf("log output missing span_id %q: %s", wantSpanID, out)
	}

	// Verify it's actually a JSON field, not just text the JSON handler
	// happens to have flattened.
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &record); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}
	if record["trace_id"] != wantTraceID {
		t.Errorf("record.trace_id = %v; want %q", record["trace_id"], wantTraceID)
	}
}

func TestSlogContextHandler_NoSpanInContext(t *testing.T) {
	var buf bytes.Buffer
	handler := NewSlogContextHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger := slog.New(handler)

	logger.LogAttrs(context.Background(), slog.LevelInfo, "no span here")

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := record["trace_id"]; ok {
		t.Errorf("trace_id should be absent when no span is in context, got %v", record)
	}
	if _, ok := record["span_id"]; ok {
		t.Errorf("span_id should be absent when no span is in context, got %v", record)
	}
}
