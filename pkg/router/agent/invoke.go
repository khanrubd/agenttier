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

package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	corev1 "k8s.io/api/core/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	agentotel "github.com/agenttier/agenttier/pkg/otel"
)

// defaultInvokeTimeout caps a single /invoke when the template doesn't set
// one. 30 minutes lines up with the steering file's call-out and matches
// what most agent frameworks consider a "long" task.
const defaultInvokeTimeout = 30 * time.Minute

// invokeKeepaliveInterval is how often we emit a `: keepalive` SSE comment
// while the entrypoint is silent. 15s is short enough that ALB / nginx
// idle-timeout middleboxes never close the connection, long enough that
// the comment doesn't dominate the wire.
const invokeKeepaliveInterval = 15 * time.Second

// InvokeStartEvent is the first SSE event every /invoke emits so callers
// can extract the invokeId for cancel.
type InvokeStartEvent struct {
	InvokeID  string `json:"invokeId"`
	StartedAt int64  `json:"startedAt"` // unix ms
}

// InvokeExitEvent is the final SSE event. Carries the exit code and the
// invoke's wall-clock duration so SDK users can compose them in shell
// pipelines (CLI exits with the same code).
type InvokeExitEvent struct {
	InvokeID   string `json:"invokeId"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
	Reason     string `json:"reason,omitempty"` // "completed" | "canceled" | "timeout" | "client_disconnect"
}

// invokeRegistryEntry tracks one in-flight invoke. Stored in Handler's
// invokes map keyed by invokeId so cancel + concurrency accounting work.
type invokeRegistryEntry struct {
	invokeID  string
	sandboxID string
	cancel    context.CancelFunc
	startedAt time.Time
	actor     string // claims.Sub — used by audit + cancel ownership check
}

// concurrencyTracker enforces agent.maxConcurrentInvokes per sandbox. A
// per-sandbox counter is created lazily; we never delete entries because
// counters are tiny and a sandbox's concurrency cap rarely changes.
type concurrencyTracker struct {
	mu     sync.Mutex
	counts map[string]int // sandboxID -> in-flight count
}

func newConcurrencyTracker() *concurrencyTracker {
	return &concurrencyTracker{counts: make(map[string]int)}
}

func (c *concurrencyTracker) try(sandboxID string, limit int) (current int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.counts[sandboxID]
	if limit > 0 && cur >= limit {
		return cur, false
	}
	c.counts[sandboxID] = cur + 1
	return cur + 1, true
}

func (c *concurrencyTracker) release(sandboxID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cur := c.counts[sandboxID]; cur > 0 {
		c.counts[sandboxID] = cur - 1
	}
}

// snapshot returns the current in-flight count for a sandbox. Used by the
// metrics gauge wired up alongside audit + OTel in the next milestone.
//
//nolint:unused // wired into metrics in milestone 4
func (c *concurrencyTracker) snapshot(sandboxID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[sandboxID]
}

// handleInvoke runs the configured agent entrypoint and streams its output
// as SSE. Closing the HTTP request cancels the in-pod process via SPDY exec
// teardown.
func (h *Handler) handleInvoke(w http.ResponseWriter, r *http.Request) {
	sandbox, claims, ok := h.loadSandbox(w, r)
	if !ok {
		return
	}
	if sandbox.Spec.Mode != agenttierv1alpha1.SandboxModeAgent {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"sandbox %s is in mode %q — /invoke is only valid for mode: agent",
			sandbox.Name, modeOrDefault(sandbox.Spec.Mode)))
		return
	}
	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseRunning {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"sandbox is in phase %q — must be Running to invoke", sandbox.Status.Phase))
		return
	}

	// Resolve the entrypoint. /configure populates status.agentConfigure
	// with the most recent one; we use that. Templates without a configure
	// step but with a static template-level entrypoint can also work, but
	// that path lands later — for now /configure must run first.
	if sandbox.Status.AgentConfigure == nil || len(sandbox.Status.AgentConfigure.Entrypoint) == 0 {
		writeError(w, http.StatusFailedDependency, fmt.Sprintf(
			"sandbox %s has no entrypoint — call POST /configure first to set one",
			sandbox.Name))
		return
	}
	entrypoint := append([]string(nil), sandbox.Status.AgentConfigure.Entrypoint...)

	// One OTel span per invoke. Attributes follow the steering rule (no
	// per-user IDs in label values; bucket by template instead).
	tracer := agentotel.Tracer("agenttier-router/agent")
	ctx, span := tracer.Start(r.Context(), "agenttier.invoke")
	span.SetAttributes(
		attribute.String("sandbox", sandbox.Name),
		attribute.String("template", sandbox.Status.ResolvedTemplate),
		attribute.String("actor", claims.Sub),
	)
	defer span.End()
	tmplLabel := templateLabel(sandbox.Status.ResolvedTemplate)
	startedAt := time.Now()
	outcome := "completed"
	var bytesStdout, bytesStderr int64
	defer func() {
		span.SetAttributes(
			attribute.String("outcome", outcome),
			attribute.Int64("bytes_stdout", bytesStdout),
			attribute.Int64("bytes_stderr", bytesStderr),
		)
		invokeRequestsTotal.WithLabelValues(tmplLabel, outcome).Inc()
		invokeDurationSeconds.WithLabelValues(tmplLabel, outcome).Observe(time.Since(startedAt).Seconds())
	}()

	// Resolve concurrency cap. /configure persists the resolved template
	// value onto status.agentConfigure.maxConcurrentInvokes. Governance
	// will overlay a cluster ceiling in milestone 5.
	concurrencyLimit := resolveConcurrencyLimit(sandbox)
	current, ok := h.concurrency.try(sandbox.Name, concurrencyLimit)
	if !ok {
		invokeThrottledTotal.Inc()
		outcome = "throttled"
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":    "concurrency_exceeded",
			"limit":    concurrencyLimit,
			"inflight": current,
			"message":  fmt.Sprintf("sandbox %s already has %d concurrent invokes (max %d)", sandbox.Name, current, concurrencyLimit),
		})
		return
	}
	defer h.concurrency.release(sandbox.Name)

	// Resolve per-invoke timeout. Caller can lower (via ?timeout=Xs query
	// param) but not raise the template default.
	invokeTimeout := resolveInvokeTimeout(sandbox, r.URL.Query().Get("timeout"))

	// Build the command argv. The configured entrypoint runs unmodified.
	// If ?prompt=... is set, it's appended as --prompt=<value> for
	// frameworks that take a flag, and also fed on stdin so frameworks
	// that read stdin still work. Body bytes (when present) are fed to
	// stdin instead of / alongside the prompt.
	argv := append([]string(nil), entrypoint...)
	prompt := r.URL.Query().Get("prompt")
	if prompt != "" {
		argv = append(argv, "--prompt="+prompt)
	}

	// Read the request body — capped to keep one bad caller from OOM-ing
	// the Router. 16 MiB is generous for a JSON payload; agents that need
	// larger inputs can write a file via /configure first.
	const maxBodyBytes = 16 * 1024 * 1024
	bodyReader := io.LimitReader(r.Body, maxBodyBytes+1)
	body, _ := io.ReadAll(bodyReader)
	if int64(len(body)) > maxBodyBytes {
		outcome = "body_too_large"
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"request body exceeds %d bytes", maxBodyBytes))
		return
	}
	// Fall back to prompt as stdin when the body is empty.
	stdin := body
	if len(stdin) == 0 && prompt != "" {
		stdin = []byte(prompt)
	}

	// Now we've passed all the cheap rejects — set up SSE and stream.
	sse, ok := newSSEWriter(w)
	if !ok {
		outcome = "stream_unsupported"
		return
	}

	invokeID := newInvokeID()
	span.SetAttributes(attribute.String("invoke_id", invokeID))
	_ = sse.WriteEvent("start", InvokeStartEvent{
		InvokeID:  invokeID,
		StartedAt: startedAt.UnixMilli(),
	})

	// invokeCtx is the context the entrypoint runs under. We derive it from
	// the spanned ctx so closing the HTTP connection cancels the exec, and
	// also so the OTel span covers the entire exec lifetime. We also
	// register a CancelFunc so /invoke/cancel can terminate the process
	// out-of-band.
	invokeCtx, cancel := context.WithTimeout(ctx, invokeTimeout)
	defer cancel()

	h.invokes.Store(invokeID, &invokeRegistryEntry{
		invokeID:  invokeID,
		sandboxID: sandbox.Name,
		cancel:    cancel,
		startedAt: startedAt,
		actor:     claims.Sub,
	})
	defer h.invokes.Delete(invokeID)

	// Keepalive comment loop. Cancels when invokeCtx ends (cleanup happens
	// in the goroutine itself).
	go func() {
		t := time.NewTicker(invokeKeepaliveInterval)
		defer t.Stop()
		for {
			select {
			case <-invokeCtx.Done():
				return
			case <-t.C:
				sse.WriteRaw(": keepalive\n\n")
			}
		}
	}()

	// Wrap stdout / stderr writers so we count bytes for the OTel span
	// without buffering the whole stream.
	stdoutCounter := &countingWriter{inner: sse.withStream("stdout")}
	stderrCounter := &countingWriter{inner: sse.withStream("stderr")}

	cmd := buildInvokeCommand(argv)

	exitReason := "completed"
	var stdinReader io.Reader
	if len(stdin) > 0 {
		stdinReader = bytes.NewReader(stdin)
	}
	exitCode, err := h.opts.Bridge.ExecCommandStreamWithStdin(
		invokeCtx, sandbox.Namespace, sandbox.Status.PodName, "sandbox",
		cmd, stdinReader, stdoutCounter, stderrCounter,
	)
	sse.flushPending()
	bytesStdout = stdoutCounter.n
	bytesStderr = stderrCounter.n

	// Map context errors into the exit reason so callers can distinguish.
	if err != nil {
		switch invokeCtx.Err() {
		case context.DeadlineExceeded:
			exitReason = "timeout"
			exitCode = -1
		case context.Canceled:
			// We can't tell here whether the cancel came from the client
			// disconnecting or from /invoke/cancel. Both bubble up the
			// same way. Choose "canceled" — clients that disconnect
			// implicitly cancel; that's accurate.
			exitReason = "canceled"
			exitCode = -1
		default:
			exitReason = "error"
			exitCode = -1
		}
	}
	outcome = exitReason

	_ = sse.WriteEvent("exit", InvokeExitEvent{
		InvokeID:   invokeID,
		ExitCode:   exitCode,
		DurationMs: time.Since(startedAt).Milliseconds(),
		Reason:     exitReason,
	})

	// Audit. Reason indicates how the invoke ended; message stays small
	// (no argv / stdin so we never accidentally record secrets in the
	// audit trail). The audit toggle from the steering file
	// (`audit.includeInvokePayloads`) lands when payload recording is
	// requested by a real consumer.
	auditType := corev1.EventTypeNormal
	if exitReason == "timeout" || exitReason == "error" || (exitReason == "completed" && exitCode != 0) {
		auditType = corev1.EventTypeWarning
	}
	h.recordAuditEvent(ctx, sandbox, auditType, "AgentInvoked", fmt.Sprintf(
		"invokeId=%s exit=%d reason=%s duration_ms=%d",
		invokeID, exitCode, exitReason, time.Since(startedAt).Milliseconds(),
	))
}

// countingWriter wraps an io.Writer and tracks total bytes written. Used so
// the invoke OTel span carries bytes_stdout / bytes_stderr attributes
// without buffering the whole stream in memory.
type countingWriter struct {
	inner io.Writer
	n     int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.inner.Write(p)
	c.n += int64(n)
	return n, err
}

// CancelRequest is the body of POST /invoke/cancel.
type CancelRequest struct {
	InvokeID string `json:"invokeId"`
}

// handleInvokeCancel terminates an in-flight invoke. Best-effort: if the
// invoke completed between the client deciding to cancel and the request
// landing, we return 404.
func (h *Handler) handleInvokeCancel(w http.ResponseWriter, r *http.Request) {
	sandbox, claims, ok := h.loadSandbox(w, r)
	if !ok {
		return
	}
	if sandbox.Spec.Mode != agenttierv1alpha1.SandboxModeAgent {
		writeError(w, http.StatusBadRequest, "sandbox is not in mode: agent")
		return
	}

	var req CancelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.InvokeID == "" {
		writeError(w, http.StatusBadRequest, "invokeId is required in request body")
		return
	}

	raw, ok := h.invokes.Load(req.InvokeID)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("invoke %s not in flight", req.InvokeID))
		return
	}
	entry, ok := raw.(*invokeRegistryEntry)
	if !ok || entry == nil {
		writeError(w, http.StatusNotFound, "invoke not found")
		return
	}

	// Belt-and-braces: cancel only matches when the invoke is on this
	// sandbox AND (caller is admin OR caller started it). Prevents one
	// user from canceling another user's job that happens to share an
	// invokeId guess.
	if entry.sandboxID != sandbox.Name {
		writeError(w, http.StatusNotFound, "invoke does not belong to this sandbox")
		return
	}
	if !claims.IsAdmin && entry.actor != claims.Sub {
		writeError(w, http.StatusForbidden, "you do not own this invoke")
		return
	}

	entry.cancel()
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers --------------------------------------------------------------

func newInvokeID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Should never happen; fallback to a timestamp so we still produce
		// something usable for the registry key.
		return fmt.Sprintf("inv-%d", time.Now().UnixNano())
	}
	return "inv-" + hex.EncodeToString(b)
}

func resolveConcurrencyLimit(sandbox *agenttierv1alpha1.Sandbox) int {
	// /configure persists the resolved AgentSpec.MaxConcurrentInvokes onto
	// status. Governance integration in milestone 5 overlays a cluster
	// ceiling that clamps the value down further at admission time, so we
	// only need to honor what's already on status here.
	if sandbox.Status.AgentConfigure != nil {
		return int(sandbox.Status.AgentConfigure.MaxConcurrentInvokes)
	}
	return 0
}

func resolveInvokeTimeout(sandbox *agenttierv1alpha1.Sandbox, override string) time.Duration {
	limit := defaultInvokeTimeout
	if sandbox.Status.AgentConfigure != nil && sandbox.Status.AgentConfigure.DefaultInvokeTimeoutSeconds > 0 {
		if d := time.Duration(sandbox.Status.AgentConfigure.DefaultInvokeTimeoutSeconds) * time.Second; d > 0 {
			limit = d
		}
	}
	if override != "" {
		if d, err := time.ParseDuration(override); err == nil && d > 0 && d < limit {
			return d
		}
	}
	return limit
}

// buildInvokeCommand wraps argv in `sh -c` so we can pass a single string
// to the SPDY exec layer. Stdin is delivered separately via the bridge's
// stdin channel — no shell encoding required, no ARG_MAX limit.
func buildInvokeCommand(argv []string) []string {
	// Quote argv so the user program receives the same args we got. Each
	// arg goes through a single-quote wrap with embedded single quotes
	// turned into '"'"'.
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, shellQuote(a))
	}
	cmdline := strings.Join(quoted, " ")
	return []string{"/bin/sh", "-c", cmdline}
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes.
// Standard POSIX-safe pattern.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, "'\\\"$`!*?[ \t\n#&|;<>(){}~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// itoa is a small helper kept here in case future error responses need it
// without pulling in strconv at the call site. Currently unused.
//
//nolint:unused // reserved for future error-response shaping
func itoa(n int) string { return strconv.Itoa(n) }
