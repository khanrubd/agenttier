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
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// SlogContextHandler wraps a base slog.Handler and stamps the current
// trace_id and span_id (extracted from the slog.Record's context) onto
// every emitted record. When no span is active in the context, the fields
// are simply omitted — no overhead beyond a single trace.SpanContextFromContext
// call that returns an invalid span context.
//
// Wrapping at the handler level (rather than asking every call site to
// pass a logger derived from a per-request context) means existing log
// statements that already pass r.Context() down to slog automatically
// gain trace correlation the moment Setup wires an OTel provider in.
type SlogContextHandler struct {
	inner slog.Handler
}

// NewSlogContextHandler wraps an existing handler. Pass the result to
// slog.New(...) when constructing your application logger.
func NewSlogContextHandler(inner slog.Handler) *SlogContextHandler {
	return &SlogContextHandler{inner: inner}
}

// Enabled delegates to the inner handler.
func (h *SlogContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle injects trace context fields into the record before delegating.
// We add to a copy of the record's attrs rather than mutating in place so
// the caller's record stays clean for any other handlers in a fan-out.
func (h *SlogContextHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		// Both IDs are stringified to their canonical hex form. trace_id
		// is 32 hex chars, span_id is 16. Empty IDs (which IsValid
		// rejects) would render as zeros — handled by the IsValid gate.
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

// WithAttrs returns a new handler whose inner handler has the given
// attrs pre-applied. The trace-context injection still runs at Handle
// time on the outer wrapper.
func (h *SlogContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &SlogContextHandler{inner: h.inner.WithAttrs(attrs)}
}

// WithGroup returns a new handler whose inner handler is grouped.
func (h *SlogContextHandler) WithGroup(name string) slog.Handler {
	return &SlogContextHandler{inner: h.inner.WithGroup(name)}
}
