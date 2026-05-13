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
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
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

	// Resolve concurrency cap. Today this comes from the template's agent
	// spec (which is merged into the sandbox at create time). Governance
	// will pin a cluster-wide ceiling in milestone 5.
	concurrencyLimit := resolveConcurrencyLimit(sandbox)
	current, ok := h.concurrency.try(sandbox.Name, concurrencyLimit)
	if !ok {
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
		return
	}

	invokeID := newInvokeID()
	startedAt := time.Now()
	_ = sse.WriteEvent("start", InvokeStartEvent{
		InvokeID:  invokeID,
		StartedAt: startedAt.UnixMilli(),
	})

	// invokeCtx is the context the entrypoint runs under. We derive it from
	// r.Context() so closing the HTTP connection cancels the exec. We also
	// register a CancelFunc so /invoke/cancel can terminate the process
	// out-of-band.
	invokeCtx, cancel := context.WithTimeout(r.Context(), invokeTimeout)
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

	// The bridge wants stdout / stderr writers, not stdin. The current
	// terminal.Bridge.ExecCommandStream signature doesn't accept stdin
	// because every consumer up to now (file transfer, /configure install)
	// passed it via shell metacharacters. We work around that with the
	// same shell-pipe trick: wrap argv in `sh -c` and pipe stdin through
	// a heredoc-style printf | <argv>. Avoids changing the bridge
	// signature for one milestone.
	cmd := buildInvokeCommand(argv, stdin)

	exitReason := "completed"
	exitCode, err := h.opts.Bridge.ExecCommandStream(
		invokeCtx, sandbox.Namespace, sandbox.Status.PodName, "sandbox",
		cmd, sse.withStream("stdout"), sse.withStream("stderr"),
	)
	sse.flushPending()

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
	} else if exitCode != 0 {
		exitReason = "completed" // entrypoint exited non-zero, but that's the user's program; not an AgentTier failure
	}

	_ = sse.WriteEvent("exit", InvokeExitEvent{
		InvokeID:   invokeID,
		ExitCode:   exitCode,
		DurationMs: time.Since(startedAt).Milliseconds(),
		Reason:     exitReason,
	})
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
	// Today the merged template lives in status, not on the sandbox spec.
	// We fetch the agent spec out of status.agentConfigure when it's there
	// (set by /configure), and otherwise fall back to a sensible default.
	// Governance integration in milestone 5 will overlay a cluster ceiling.
	//
	// status doesn't carry agent caps yet; default to unlimited (0). Once
	// /configure starts persisting the resolved AgentSpec we'll read it
	// from there.
	return 0
}

func resolveInvokeTimeout(sandbox *agenttierv1alpha1.Sandbox, override string) time.Duration {
	limit := defaultInvokeTimeout
	if override != "" {
		if d, err := time.ParseDuration(override); err == nil && d > 0 && d < limit {
			return d
		}
	}
	return limit
}

// buildInvokeCommand wraps argv in `sh -c` so we can pipe stdin in via
// printf without changing the bridge signature. The heredoc approach used
// by /configure file uploads doesn't work here because the user's argv
// can contain anything; we route through a temp file instead so the user
// program's stdin is whatever they sent in the request body.
func buildInvokeCommand(argv []string, stdin []byte) []string {
	// Quote argv so the user program receives the same args we got. Each
	// arg goes through a single-quote wrap with embedded single quotes
	// turned into '"'"'.
	quoted := make([]string, 0, len(argv))
	for _, a := range argv {
		quoted = append(quoted, shellQuote(a))
	}
	cmdline := strings.Join(quoted, " ")

	if len(stdin) == 0 {
		return []string{"/bin/sh", "-c", cmdline}
	}

	// stdin payload: we base64-encode it so binary or newline-rich payloads
	// don't break shell parsing. The receiving agent process sees the raw
	// bytes on its stdin via the base64 -d | <cmd> pipe.
	encoded := base64.StdEncoding.EncodeToString(stdin)
	return []string{"/bin/sh", "-c", fmt.Sprintf(
		"printf %%s %s | base64 -d | %s",
		shellQuote(encoded), cmdline,
	)}
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
