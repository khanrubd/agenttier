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

package sandboxruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"
)

// InvokeRequest is the wire format for POST /invoke. Mirrors ExecRequest
// because the agent /invoke flow is "run a command and stream its output"
// — same shape, different transport semantics.
type InvokeRequest struct {
	Command        []string          `json:"command"`
	Stdin          string            `json:"stdin,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	WorkingDir     string            `json:"workingDir,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	// InvokeID, when set, lets the caller supply its own invoke
	// identifier so /invoke/cancel can address this run. Empty means
	// the runtime generates one and emits it on the first SSE event.
	InvokeID string `json:"invokeId,omitempty"`
}

// invokeRegistry is the in-pod cancel registry. Lives on the sandbox
// runtime — replaces the per-Router-pod registry that broke multi-replica
// cancel. Now any Router replica can dial the sandbox's /invoke/cancel
// and the in-pod runtime resolves the invoke locally regardless of which
// Router started it.
type invokeRegistry struct {
	mu      sync.Mutex
	entries map[string]context.CancelFunc
}

func newInvokeRegistry() *invokeRegistry {
	return &invokeRegistry{entries: make(map[string]context.CancelFunc)}
}

func (r *invokeRegistry) add(id string, cancel context.CancelFunc) {
	r.mu.Lock()
	r.entries[id] = cancel
	r.mu.Unlock()
}

func (r *invokeRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.entries, id)
	r.mu.Unlock()
}

// cancel fires the registered CancelFunc for id and removes the entry.
// Returns true when the id was registered, false when it wasn't (already
// completed or never started).
func (r *invokeRegistry) cancel(id string) bool {
	r.mu.Lock()
	cancel, ok := r.entries[id]
	if ok {
		delete(r.entries, id)
	}
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// handleInvoke runs the user's command and streams stdout / stderr / exit
// over Server-Sent Events. The wire format mirrors what the Router's
// agent handler emits today, so the Router can transparently proxy these
// events through to its own clients without re-shaping anything.
//
// Events:
//
//	event: start
//	data: {"invokeId":"...","startedAt":<ms>}
//
//	event: log
//	data: {"stream":"stdout"|"stderr","data":"..."}
//
//	event: exit
//	data: {"exitCode":N,"durationMs":N,"reason":"completed"|"timeout"|"canceled"|"error"}
func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "server does not support streaming")
		return
	}

	// Cap the request body. Same MaxExecOutputBytes ceiling as /exec —
	// no reason to allow a larger stdin payload here.
	r.Body = http.MaxBytesReader(w, r.Body, MaxExecOutputBytes+1024)
	var req InvokeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Command) == 0 {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}

	invokeID := req.InvokeID
	if invokeID == "" {
		invokeID = generateInvokeID()
	}

	// Headers FIRST — ResponseWriter switches to streaming mode after
	// the first WriteHeader. Keepalive comments below need this in
	// place before the first event lands.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable any nginx-style buffering
	w.WriteHeader(http.StatusOK)

	startedAt := time.Now()
	writeEvent(w, flusher, "start", map[string]any{
		"invokeId":  invokeID,
		"startedAt": startedAt.UnixMilli(),
	})

	// Wire up the cancel registry so /invoke/cancel can find this run.
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	if timeout > MaxExecTimeout {
		timeout = MaxExecTimeout
	}
	invokeCtx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	s.invokeRegistry.add(invokeID, cancel)
	defer s.invokeRegistry.remove(invokeID)

	// Run the command, streaming its output as SSE log events.
	exitCode, reason := s.runStreamed(invokeCtx, w, flusher, req)

	writeEvent(w, flusher, "exit", map[string]any{
		"invokeId":   invokeID,
		"exitCode":   exitCode,
		"durationMs": time.Since(startedAt).Milliseconds(),
		"reason":     reason,
	})
}

// runStreamed launches the command and pipes its stdout / stderr to the
// SSE writer. Returns (exitCode, reason). Reasons line up with what the
// Router's agent handler emits: "completed" (clean exit), "timeout"
// (deadline fired), "canceled" (context cancel — client disconnect or
// /invoke/cancel), "error" (start failure).
func (s *Server) runStreamed(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, req InvokeRequest) (int, string) {
	cmd := exec.CommandContext(ctx, req.Command[0], req.Command[1:]...) //nolint:gosec // authenticated caller, arbitrary command is the contract
	// Without WaitDelay, killing a shell wrapper (e.g. /bin/sh -c "...") on
	// timeout/cancel leaves orphaned grandchildren holding the inherited
	// stdout/stderr pipes open, so the pipe reads below never hit EOF and
	// wg.Wait() blocks forever — defeating the very timeout we're enforcing
	// and leaking the Router's concurrency slot. 500ms gives post-cancel
	// cleanup a small grace window before the pipes are force-closed.
	cmd.WaitDelay = 500 * time.Millisecond
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	}
	if len(req.Env) > 0 {
		base := append([]string(nil), os.Environ()...)
		for k, v := range req.Env {
			base = append(base, k+"="+v)
		}
		cmd.Env = base
	}
	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return -1, "error"
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return -1, "error"
	}

	if err := cmd.Start(); err != nil {
		return -1, "error"
	}

	// Stream stdout + stderr concurrently so a chatty stderr doesn't
	// starve stdout consumers (or vice versa). 4 KiB chunks balance
	// latency against syscall overhead. writeMu serializes the two
	// goroutines' writes — http.ResponseWriter is NOT safe for concurrent
	// Write/Flush, and unsynchronized stdout+stderr writes interleave
	// mid-event and corrupt the SSE framing the Router/SDK must parse.
	var wg sync.WaitGroup
	var writeMu sync.Mutex
	streamPipe := func(stream string, r io.Reader) {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				writeMu.Lock()
				writeEvent(w, flusher, "log", map[string]string{
					"stream": stream,
					"data":   string(buf[:n]),
				})
				writeMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}
	wg.Add(2)
	go streamPipe("stdout", stdout)
	go streamPipe("stderr", stderr)

	// Wait for both pipes to drain (EOF on close), then for the process
	// itself. Order matters: cmd.Wait() must run after the pipes finish
	// or it'll return before all output is drained.
	wg.Wait()
	waitErr := cmd.Wait()

	// Map context state -> reason. The runtime's ctx wraps the timeout +
	// the registry cancel + the request context, so any of those
	// cancelling shows up here.
	switch ctx.Err() {
	case context.DeadlineExceeded:
		return -1, "timeout"
	case context.Canceled:
		return -1, "canceled"
	}

	if waitErr == nil {
		return 0, "completed"
	}
	if exitErr, ok := waitErr.(*exec.ExitError); ok {
		return exitErr.ExitCode(), "completed"
	}
	return -1, "error"
}

// handleInvokeCancel terminates an in-flight invoke addressed by ID.
// Returns 204 on success, 404 when the invoke isn't in the registry.
//
// The path carries the ID rather than a body so a kubectl curl from a
// jumphost is one-liner-clean; the wire format on the Router-facing
// /invoke/cancel still accepts a body and the Router translates.
func (s *Server) handleInvokeCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/invoke/cancel/")
	id = path.Clean(id)
	if id == "" || id == "." || id == "/" {
		writeError(w, http.StatusBadRequest, "invokeId path component required")
		return
	}
	if s.invokeRegistry.cancel(id) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "no in-flight invoke with that id")
}

// generateInvokeID returns a 16-byte hex random string. Used when the
// caller doesn't supply an invokeID. Collision probability is
// negligible — this is per-pod, and a single sandbox running 10^9
// invokes still has < 1 in 10^20 chance of collision over its lifetime.
func generateInvokeID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fall back to time-based identifier if entropy is exhausted
		// (essentially impossible on Linux; defensive only).
		return fmt.Sprintf("inv-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// writeEvent emits one SSE event with the given type + JSON-encoded data.
// Flushes after every write so consumers see the event immediately
// rather than after the connection's TCP buffer fills.
func writeEvent(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		body = []byte(`{}`)
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, body)
	flusher.Flush()
}
