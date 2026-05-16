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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// MaxExecOutputBytes caps stdout + stderr per /exec response. Mirrors the
// Router's existing exec ceiling so the new path doesn't loosen any
// limits the user-facing contract already enforces. Output exceeding this
// cap is truncated with a marker; the exit code is preserved.
const MaxExecOutputBytes = 8 * 1024 * 1024 // 8 MiB

// MaxExecTimeout caps how long any single /exec request can run before the
// runtime gives up and returns timedOut=true. Synced with Router's
// `defaultInvokeTimeout` ceiling so the in-pod path doesn't extend what
// the API contract already defines.
const MaxExecTimeout = 30 * time.Minute

// DefaultExecTimeout is what /exec uses when the request omits
// timeoutSeconds. Same as today's SPDY-exec path: 60 seconds covers any
// reasonable interactive command, longer ones must be explicit.
const DefaultExecTimeout = 60 * time.Second

// ExecRequest is the wire format for POST /exec.
type ExecRequest struct {
	// Command is the argv to execute. First element is the binary, rest are
	// arguments. Empty slice is a 400.
	Command []string `json:"command"`

	// Stdin is bytes to write to the process's stdin. Empty = no stdin.
	// Currently fully buffered in memory (capped at MaxExecOutputBytes).
	// Streaming variants ship in Phase 4.
	Stdin string `json:"stdin,omitempty"`

	// TimeoutSeconds bounds wall-clock execution. Zero defaults to
	// DefaultExecTimeout. Values above MaxExecTimeout are clamped down
	// rather than rejected so a misbehaving caller doesn't see a 400 it
	// can't easily reason about.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// WorkingDir is the directory the command runs in. Empty means
	// "process default" (typically the container's `WORKDIR` or `/`).
	WorkingDir string `json:"workingDir,omitempty"`

	// Env adds or overrides environment variables for this command only.
	// The runtime's own env is preserved so PATH, LANG, etc. still work.
	Env map[string]string `json:"env,omitempty"`
}

// ExecResponse is the wire format /exec returns.
type ExecResponse struct {
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	DurationMs int64  `json:"durationMs"`
	TimedOut   bool   `json:"timedOut"`
	Truncated  bool   `json:"truncated,omitempty"`
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	// Cap request body so a malicious client can't exhaust memory by
	// sending an unbounded stdin payload. The body parser respects this
	// limit via http.MaxBytesReader.
	r.Body = http.MaxBytesReader(w, r.Body, MaxExecOutputBytes+1024)
	var req ExecRequest
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

	resp := s.executor.Execute(r.Context(), req)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// defaultExecutor runs commands via os/exec.Command. Production
// implementation; tests inject a stub via Server.SetExecutor.
type defaultExecutor struct{}

func (defaultExecutor) Execute(ctx context.Context, req ExecRequest) ExecResponse {
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = DefaultExecTimeout
	}
	if timeout > MaxExecTimeout {
		timeout = MaxExecTimeout
	}

	// Derive a child context that fires at the timeout and propagates
	// the parent's cancellation (the HTTP request being closed).
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, req.Command[0], req.Command[1:]...) //nolint:gosec // command is provided by an authenticated caller and we explicitly run arbitrary user input — that's the contract
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	}
	if len(req.Env) > 0 {
		// Inherit the current environment so PATH etc. still work, then
		// overlay the caller's overrides.
		base := append([]string(nil), os.Environ()...)
		for k, v := range req.Env {
			base = append(base, k+"="+v)
		}
		cmd.Env = base
	}
	if req.Stdin != "" {
		cmd.Stdin = strings.NewReader(req.Stdin)
	}

	// Cap stdout + stderr independently so a noisy command doesn't blow
	// memory. We use a custom writer that stops appending past the cap
	// but keeps reading so the child process doesn't block on a closed
	// pipe (which would skew the exit code).
	stdoutBuf := &cappedBuffer{max: MaxExecOutputBytes}
	stderrBuf := &cappedBuffer{max: MaxExecOutputBytes}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	resp := ExecResponse{
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		DurationMs: duration.Milliseconds(),
		Truncated:  stdoutBuf.truncated || stderrBuf.truncated,
	}

	if cmdCtx.Err() == context.DeadlineExceeded {
		resp.TimedOut = true
		resp.ExitCode = -1
		return resp
	}

	if runErr == nil {
		resp.ExitCode = 0
		return resp
	}

	// Extract the exit code from os/exec's error wrapping. Non-zero exits
	// produce *ExitError; signal kills produce -1 in the conventional
	// shell sense.
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		resp.ExitCode = exitErr.ExitCode()
		return resp
	}

	// Unknown error — exec failed to start, binary not found, etc.
	resp.ExitCode = -1
	resp.Stderr = strings.TrimRight(resp.Stderr, "\n") + "\n[runtime] " + runErr.Error()
	return resp
}

// cappedBuffer is an io.Writer that caps total bytes written. Past the cap
// it discards but keeps consuming so the writer-side never blocks. Mirrors
// the truncation semantics of today's exec endpoint.
type cappedBuffer struct {
	max       int
	buf       bytes.Buffer
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	remaining := c.max - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) <= remaining {
		return c.buf.Write(p)
	}
	if _, err := c.buf.Write(p[:remaining]); err != nil {
		return 0, err
	}
	c.truncated = true
	return len(p), nil
}

func (c *cappedBuffer) String() string {
	if c.truncated {
		return c.buf.String() + "\n[truncated]"
	}
	return c.buf.String()
}
