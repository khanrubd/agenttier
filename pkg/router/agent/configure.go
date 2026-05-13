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
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// installSoftTimeout is how long the install command can run before the
// Router gives up. 15 minutes covers any reasonable pip / npm / apt install;
// anything longer is almost certainly a bug or runaway dependency tree.
const installSoftTimeout = 15 * time.Minute

// configureFileLimitBytes caps a single uploaded file. Mirrors the existing
// file-transfer cap so /configure doesn't open a larger attack surface.
const configureFileLimitBytes = 32 * 1024 * 1024 // 32 MiB

// installLogTailBytes is how much install log we persist into status. Enough
// for users to debug failures without bloating the CR.
const installLogTailBytes = 8 * 1024

// ConfigureRequest is the body of POST /configure.
type ConfigureRequest struct {
	// Files to write into the sandbox PVC before running InstallCommand.
	// Paths must be absolute and live under the sandbox container — we
	// reject anything that traverses upward.
	Files []ConfigureFile `json:"files,omitempty"`

	// InstallCommand runs once after files are written. Idempotent across
	// re-configures with the same file set + command (we hash both).
	InstallCommand []string `json:"installCommand,omitempty"`

	// Entrypoint is the command POST /invoke runs on every call. Updated
	// unconditionally on every configure, even when InstallCommand is a no-op.
	Entrypoint []string `json:"entrypoint,omitempty"`
}

// ConfigureFile is a single file to upload. Mirrors PutFile semantics but
// embedded in the configure request body so callers can ship code + run
// install in one round trip.
type ConfigureFile struct {
	// Path inside the container, e.g. "/workspace/agent.py".
	Path string `json:"path"`

	// Content is either a raw UTF-8 string (most common case for source
	// code) or, when ContentBase64 is non-empty, base64-encoded bytes for
	// binary files like wheels or model checkpoints. Exactly one of the
	// two is required.
	Content       string `json:"content,omitempty"`
	ContentBase64 string `json:"contentBase64,omitempty"`
}

// ConfigureResponse is what the SDK + CLI parse out of the SSE stream's
// final "result" event. We also write it into Sandbox.status.agentConfigure
// so kubectl users see the same data.
type ConfigureResponse struct {
	LastConfiguredAt   metav1.Time `json:"lastConfiguredAt"`
	InstallCommandHash string      `json:"installCommandHash"`
	Entrypoint         []string    `json:"entrypoint,omitempty"`
	InstallExitCode    int         `json:"installExitCode"`
	Skipped            bool        `json:"skipped"` // true when the install was a no-op (idempotent re-configure)
}

func (h *Handler) handleConfigure(w http.ResponseWriter, r *http.Request) {
	sandbox, _, ok := h.loadSandbox(w, r)
	if !ok {
		return
	}

	// /configure is only valid for agent-mode sandboxes. Code-mode users
	// already have file-transfer + exec endpoints; they don't need this.
	if sandbox.Spec.Mode != agenttierv1alpha1.SandboxModeAgent {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"sandbox %s is in mode %q — /configure is only valid for mode: agent. "+
				"Use the file-transfer and exec APIs for interactive sandboxes.",
			sandbox.Name, modeOrDefault(sandbox.Spec.Mode)))
		return
	}
	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseRunning {
		writeError(w, http.StatusConflict, fmt.Sprintf(
			"sandbox is in phase %q — wait for Running before configuring", sandbox.Status.Phase))
		return
	}

	var req ConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Hash inputs for idempotency. Same files + same install command means
	// re-running install is wasteful; we short-circuit and just refresh
	// the entrypoint + lastConfiguredAt.
	hash := req.installHash()
	skipped := false
	if existing := sandbox.Status.AgentConfigure; existing != nil &&
		existing.InstallCommandHash == hash &&
		hash != "" {
		skipped = true
	}

	sse, ok := newSSEWriter(w)
	if !ok {
		return
	}

	// 1. Write all files first. We do this before the install so the
	//    install command can reference uploaded code (e.g., requirements.txt).
	if !skipped {
		if err := h.writeFiles(r.Context(), sse, sandbox, req.Files); err != nil {
			_ = sse.WriteEvent("error", map[string]string{
				"phase":   "files",
				"message": err.Error(),
			})
			return
		}
	}

	// 2. Run the install command. We pipe stdout + stderr live to the SSE
	//    stream so users watch their pip / npm install progress in real time.
	exitCode := 0
	var installLogTail string
	if !skipped && len(req.InstallCommand) > 0 {
		ec, tail, err := h.runInstall(r.Context(), sse, sandbox, req.InstallCommand)
		if err != nil {
			_ = sse.WriteEvent("error", map[string]string{
				"phase":   "install",
				"message": err.Error(),
			})
			return
		}
		exitCode = ec
		installLogTail = tail
	}

	// 3. Persist the result into Sandbox.status.agentConfigure. Even when
	//    the install failed (exitCode != 0) we record the attempt so the
	//    UI can surface it; the next /configure call will re-run because
	//    we only short-circuit on a successful prior configure.
	now := metav1.Now()
	resp := ConfigureResponse{
		LastConfiguredAt:   now,
		InstallCommandHash: hash,
		Entrypoint:         req.Entrypoint,
		InstallExitCode:    exitCode,
		Skipped:            skipped,
	}
	if exitCode == 0 || skipped {
		if err := h.persistStatus(r.Context(), sandbox, &resp, installLogTail); err != nil {
			h.opts.Logger.Warn("failed to persist agentConfigure status",
				"sandbox", sandbox.Name, "error", err)
		}
	}

	_ = sse.WriteEvent("result", resp)
}

func (req ConfigureRequest) validate() error {
	if len(req.Files) == 0 && len(req.InstallCommand) == 0 && len(req.Entrypoint) == 0 {
		return fmt.Errorf("at least one of files, installCommand, or entrypoint is required")
	}
	for i, f := range req.Files {
		if f.Path == "" {
			return fmt.Errorf("files[%d].path is required", i)
		}
		if !strings.HasPrefix(f.Path, "/") {
			return fmt.Errorf("files[%d].path must be absolute (got %q)", i, f.Path)
		}
		if strings.Contains(f.Path, "..") {
			return fmt.Errorf("files[%d].path must not contain '..'", i)
		}
		if f.Content == "" && f.ContentBase64 == "" {
			return fmt.Errorf("files[%d] must set content or contentBase64", i)
		}
		if f.Content != "" && f.ContentBase64 != "" {
			return fmt.Errorf("files[%d]: set content OR contentBase64, not both", i)
		}
	}
	return nil
}

// installHash returns a stable SHA256 over the uploaded files + install
// command. Used as the idempotency key for re-configures.
func (req ConfigureRequest) installHash() string {
	h := sha256.New()
	for _, c := range req.InstallCommand {
		h.Write([]byte(c))
		h.Write([]byte{0})
	}
	// Files are hashed in path-sorted order so uploading the same set in
	// a different request order produces the same hash.
	files := append([]ConfigureFile(nil), req.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		if f.ContentBase64 != "" {
			h.Write([]byte(f.ContentBase64))
		} else {
			h.Write([]byte(f.Content))
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeFiles copies each ConfigureFile into the sandbox PVC by piping the
// contents through `base64 -d` over the SPDY exec bridge. Mirrors the
// approach in pkg/router/handlers.go's handlePutFile — same pattern, just
// invoked in a loop.
func (h *Handler) writeFiles(ctx context.Context, sse *sseWriter, sandbox *agenttierv1alpha1.Sandbox, files []ConfigureFile) error {
	for _, f := range files {
		cleaned := path.Clean(f.Path)
		_ = sse.WriteEvent("log", map[string]string{
			"stream": "stdout",
			"data":   fmt.Sprintf("[configure] uploading %s", cleaned),
		})

		var raw []byte
		if f.ContentBase64 != "" {
			b, err := base64.StdEncoding.DecodeString(f.ContentBase64)
			if err != nil {
				return fmt.Errorf("decode %s: %w", cleaned, err)
			}
			raw = b
		} else {
			raw = []byte(f.Content)
		}
		if int64(len(raw)) > configureFileLimitBytes {
			return fmt.Errorf("%s: %d bytes exceeds limit of %d", cleaned, len(raw), configureFileLimitBytes)
		}

		encoded := base64.StdEncoding.EncodeToString(raw)
		dir := path.Dir(cleaned)
		// Same here-doc trick as handlePutFile: %q-quote the encoded payload
		// so shell metachars are neutralized, then pipe through `base64 -d`.
		cmd := []string{"/bin/sh", "-c", fmt.Sprintf(
			"mkdir -p '%s' && printf '%%s' %q | base64 -d > '%s'",
			dir, encoded, cleaned,
		)}
		var stderr bytes.Buffer
		exitCode, err := h.opts.Bridge.ExecCommandStream(ctx, sandbox.Namespace, sandbox.Status.PodName, "sandbox", cmd, &nullWriter{}, &stderr)
		if err != nil {
			return fmt.Errorf("write %s: %w", cleaned, err)
		}
		if exitCode != 0 {
			return fmt.Errorf("write %s exited %d: %s", cleaned, exitCode, strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}

// runInstall runs the install command inside the sandbox, streams output to
// SSE, and returns the exit code + a tail of the log for status persistence.
func (h *Handler) runInstall(ctx context.Context, sse *sseWriter, sandbox *agenttierv1alpha1.Sandbox, command []string) (int, string, error) {
	_ = sse.WriteEvent("log", map[string]string{
		"stream": "stdout",
		"data":   fmt.Sprintf("[configure] running install: %s", strings.Join(command, " ")),
	})

	installCtx, cancel := context.WithTimeout(ctx, installSoftTimeout)
	defer cancel()

	// Tee stdout/stderr into a tail buffer so we can persist the trailing
	// install_log_tail bytes onto the sandbox status. Writing through both
	// the SSE writer and the tail keeps logic small.
	tail := &tailBuffer{max: installLogTailBytes}
	stdoutW := &multiWriter{writers: []writerlike{sse.withStream("stdout"), tail}}
	stderrW := &multiWriter{writers: []writerlike{sse.withStream("stderr"), tail}}

	exitCode, err := h.opts.Bridge.ExecCommandStream(installCtx, sandbox.Namespace, sandbox.Status.PodName, "sandbox", command, stdoutW, stderrW)
	sse.flushPending()
	if err != nil {
		// Context deadline / cancel surfaces here. Distinguish so users
		// see a clear "install timed out" rather than a generic exec error.
		if installCtx.Err() == context.DeadlineExceeded {
			return -1, tail.String(), fmt.Errorf("install timed out after %s", installSoftTimeout)
		}
		return -1, tail.String(), err
	}
	return exitCode, tail.String(), nil
}

// persistStatus writes the configure result into Sandbox.status.agentConfigure.
// We use a 3x retry loop to absorb the optimistic-concurrency conflicts that
// happen when the controller updates status concurrently.
func (h *Handler) persistStatus(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, resp *ConfigureResponse, installLog string) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		// Re-fetch so we always patch the latest resourceVersion.
		fresh := &agenttierv1alpha1.Sandbox{}
		if err := h.opts.K8sClient.Get(ctx, ctrlClientKey(sandbox), fresh); err != nil {
			return err
		}
		now := resp.LastConfiguredAt
		fresh.Status.AgentConfigure = &agenttierv1alpha1.AgentConfigureStatus{
			LastConfiguredAt:   &now,
			InstallCommandHash: resp.InstallCommandHash,
			Entrypoint:         resp.Entrypoint,
			InstallExitCode:    resp.InstallExitCode,
			InstallLog:         installLog,
		}
		if err := h.opts.K8sClient.Status().Update(ctx, fresh); err != nil {
			if errors.IsConflict(err) {
				lastErr = err
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func modeOrDefault(m agenttierv1alpha1.SandboxMode) string {
	if m == "" {
		return string(agenttierv1alpha1.SandboxModeCode)
	}
	return string(m)
}

// --- small writer utilities -----------------------------------------------

type writerlike interface {
	Write(p []byte) (int, error)
}

// nullWriter discards everything (used when we don't care about file-write
// stdout but still need to satisfy the bridge's writer signature).
type nullWriter struct{}

func (n *nullWriter) Write(p []byte) (int, error) { return len(p), nil }

// multiWriter fans a single Write to several backends. Used so install
// output simultaneously streams to SSE and accumulates into the tail buffer.
type multiWriter struct {
	writers []writerlike
}

func (m *multiWriter) Write(p []byte) (int, error) {
	for _, w := range m.writers {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

// tailBuffer keeps only the last `max` bytes written to it. Used for
// install_log_tail in agentConfigure status.
type tailBuffer struct {
	max  int
	data []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.data = append(t.data, p...)
	if len(t.data) > t.max {
		t.data = t.data[len(t.data)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.data) }

// envValuesByName mirrors mergeEnvVars semantics for the agent.env map. Unused
// today but kept as a hook for the next milestone (/invoke needs to merge
// caller-supplied env onto template env).
//
//nolint:unused // wired into /invoke in milestone 3
func envValuesByName(env []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		out[e.Name] = e.Value
	}
	return out
}
