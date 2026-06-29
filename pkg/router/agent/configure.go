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

	"go.opentelemetry.io/otel/attribute"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
	"github.com/agenttier/agenttier/pkg/governance"
	agentotel "github.com/agenttier/agenttier/pkg/otel"
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
	sandbox, claims, ok := h.loadSandbox(w, r)
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

	// One OTel span per /configure. Span name follows the steering rule:
	// "service.operation" → "agenttier.configure". Attributes are bounded
	// (template, actor hash) so cardinality stays sane and no raw OIDC
	// sub claim leaks into third-party trace stores. See
	// agentotel.HashActor for the digest contract.
	tracer := agentotel.Tracer("agenttier-router/agent")
	ctx, span := tracer.Start(r.Context(), "agenttier.configure") // nb: WithAttributes accepts a variadic slice; we add some now and
	// fill in install_command_hash and outcome below.

	span.SetAttributes(
		attribute.String("sandbox", sandbox.Name),
		attribute.String("template", sandbox.Status.ResolvedTemplate),
		attribute.String("actor_hash", agentotel.HashActor(claims.Sub)),
	)
	defer span.End()
	startedAt := time.Now()
	tmplLabel := templateLabel(sandbox.Status.ResolvedTemplate)
	outcome := "ok"
	defer func() {
		configureRequestsTotal.WithLabelValues(tmplLabel, outcome).Inc()
		configureDurationSeconds.WithLabelValues(tmplLabel, outcome).Observe(time.Since(startedAt).Seconds())
		span.SetAttributes(attribute.String("outcome", outcome))
	}()

	var req ConfigureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		outcome = "bad_request"
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := req.validate(); err != nil {
		outcome = "bad_request"
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Governance gate. Runs before any I/O so a denied configure costs
	// nothing — no PVC writes, no exec into the pod, no audit clutter
	// from half-applied state. Three checks:
	//
	//  1. AllowedTemplates  — namespace operator restricts which
	//     ClusterSandboxTemplate names can be configured. Even though
	//     the sandbox already exists, /configure is the first time
	//     user-supplied code lands inside it, so re-checking here is a
	//     useful belt-and-braces against templates added after a
	//     sandbox was created.
	//  2. AllowedAgentImages — the template's image must match an
	//     approved registry prefix. This stops a configure from
	//     attaching agent code to a sandbox using a non-approved
	//     image. Same allowlist semantics as the create-time check
	//     in pkg/governance/engine.go's Check().
	//  3. ConfigureFile size + count — capped server-side so a
	//     misbehaving caller can't shovel a multi-GiB tarball through
	//     a 16 MiB body limit by chunking. The per-file cap stays at
	//     configureFileLimitBytes (32 MiB); the new aggregate cap
	//     prevents 50 × 32 MiB requests.
	if denied, reason := h.checkConfigureGovernance(ctx, sandbox, &req); denied {
		outcome = "policy_denied"
		h.recordAuditEvent(ctx, sandbox, corev1.EventTypeWarning, "ConfigureDenied", reason)
		writeError(w, http.StatusForbidden, reason)
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

	// Resolve the template's agent caps (max concurrent + default invoke
	// timeout) and persist them on status so /invoke can enforce without
	// re-resolving the template on every request.
	maxConcurrent, defaultTimeout := h.resolveAgentCaps(ctx, sandbox)

	sse, ok := newSSEWriter(w)
	if !ok {
		outcome = "stream_unsupported"
		return
	}

	// 1. Write all files first. We do this before the install so the
	//    install command can reference uploaded code (e.g., requirements.txt).
	if !skipped {
		if err := h.writeFiles(ctx, sse, sandbox, req.Files); err != nil {
			_ = sse.WriteEvent("error", map[string]string{
				"phase":   "files",
				"message": err.Error(),
			})
			outcome = "files_failed"
			return
		}
	}

	// 2. Run the install command. We pipe stdout + stderr live to the SSE
	//    stream so users watch their pip / npm install progress in real time.
	exitCode := 0
	var installLogTail string
	if !skipped && len(req.InstallCommand) > 0 {
		ec, tail, err := h.runInstall(ctx, sse, sandbox, req.InstallCommand)
		if err != nil {
			_ = sse.WriteEvent("error", map[string]string{
				"phase":   "install",
				"message": err.Error(),
			})
			outcome = "install_failed"
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
		if err := h.persistStatus(ctx, sandbox, &resp, installLogTail, maxConcurrent, defaultTimeout); err != nil {
			h.opts.Logger.Warn("failed to persist agentConfigure status",
				"sandbox", sandbox.Name, "error", err)
		}
	} else {
		outcome = "install_nonzero"
	}

	span.SetAttributes(
		attribute.String("install_command_hash", hash),
		attribute.Int("install_exit_code", exitCode),
		attribute.Bool("skipped", skipped),
	)

	// Audit event onto the sandbox CR. Visible via `kubectl describe sandbox`
	// and through the existing /api/v1/audit/events endpoint.
	auditMsg := fmt.Sprintf("install_exit_code=%d skipped=%t", exitCode, skipped)
	auditType := corev1.EventTypeNormal
	if exitCode != 0 {
		auditType = corev1.EventTypeWarning
	}
	h.recordAuditEvent(ctx, sandbox, auditType, "AgentConfigured", auditMsg)

	_ = sse.WriteEvent("result", resp)
}

// configureFileTotalLimitBytes caps the cumulative size of all files in
// one ConfigureRequest. The per-file cap (configureFileLimitBytes,
// 32 MiB) protects against a single huge upload; this aggregate cap
// stops 50 × 32 MiB requests from sneaking past the per-file ceiling.
// The chosen value is 4× the per-file cap — generous enough for a
// real codebase, tight enough that a malicious caller can't shovel
// gigabytes through.
const configureFileTotalLimitBytes = 4 * configureFileLimitBytes // 128 MiB

// configureFileMaxCount caps the number of files in one request.
// Picked to be lenient (a real codebase rarely has more than a few
// dozen top-level files in /workspace) but bounded.
const configureFileMaxCount = 200

// checkConfigureGovernance applies governance gates that are specific
// to /configure but reuse the same Policy resolved by Check() at
// sandbox-create time. Returns (denied, reason). When denied=false the
// caller proceeds; when denied=true the caller returns 403.
//
// The function is intentionally a no-op when no PolicyResolver is
// wired in — same shape as resolveAgentCaps' clamp path, so a
// minimally-configured Router (no governance ConfigMap) keeps working.
func (h *Handler) checkConfigureGovernance(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, req *ConfigureRequest) (bool, string) {
	// Aggregate file-size + count caps run regardless of policy. They
	// are server-side correctness limits, not configurable policy.
	if len(req.Files) > configureFileMaxCount {
		return true, fmt.Sprintf("too many files in request: %d (max %d)", len(req.Files), configureFileMaxCount)
	}
	var total int64
	for i, f := range req.Files {
		size := int64(len(f.Content))
		// Base64-encoded content decodes to ~3/4 of its encoded size,
		// but we use the upper bound so we reject before allocating
		// the decode buffer in writeFiles.
		size += int64(len(f.ContentBase64))
		if size > int64(configureFileLimitBytes) {
			return true, fmt.Sprintf("files[%d] (%s): %d bytes exceeds per-file cap %d", i, f.Path, size, configureFileLimitBytes)
		}
		total += size
		if total > int64(configureFileTotalLimitBytes) {
			return true, fmt.Sprintf("aggregate file size %d bytes exceeds total cap %d", total, configureFileTotalLimitBytes)
		}
	}

	// Policy-driven checks. Skip when no resolver is wired in.
	if h.opts.PolicyOf == nil {
		return false, ""
	}
	policy, err := h.opts.PolicyOf(ctx, sandbox.Namespace)
	if err != nil {
		// Fail open on resolver errors — a transient ConfigMap read
		// hiccup shouldn't break /configure for legitimate users. We
		// still log via the audit path so operators can spot drift.
		h.opts.Logger.Warn("policy resolver failed; allowing configure", "sandbox", sandbox.Name, "error", err)
		return false, ""
	}
	if policy.IsEmpty() {
		return false, ""
	}

	// AllowedTemplates: the sandbox is allowed to exist (the create-time
	// check passed), but a re-check here catches policies that tightened
	// after creation. The template name lives on Status.ResolvedTemplate.
	if len(policy.AllowedTemplates) > 0 && sandbox.Status.ResolvedTemplate != "" {
		if !policyContains(policy.AllowedTemplates, sandbox.Status.ResolvedTemplate) {
			return true, fmt.Sprintf("template %q is not in the AllowedTemplates list (%s)", sandbox.Status.ResolvedTemplate, strings.Join(policy.AllowedTemplates, ", "))
		}
	}

	// AllowedAgentImages: the running pod's container image must match.
	// We read the image from the explicit Spec.Image override; the
	// template's baseline image isn't surfaced into Status, so when
	// neither is set we skip this check (the create-time governance
	// gate already validated the template at Sandbox-create time).
	if len(policy.AllowedAgentImages) > 0 {
		var image string
		if sandbox.Spec.Image != nil {
			image = sandbox.Spec.Image.Repository
		}
		if image != "" && !registryPrefixAllowed(image, policy.AllowedAgentImages) {
			return true, fmt.Sprintf("agent image %q is not in the AllowedAgentImages list (%s)", image, strings.Join(policy.AllowedAgentImages, ", "))
		}
	}

	return false, ""
}

// policyContains is a small utility — pkg/governance has the same
// helper but unexported. Duplicated here so the agent package doesn't
// pull in package-internal helpers.
func policyContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// registryPrefixAllowed mirrors pkg/governance's hasRegistryPrefix:
// any prefix match counts (e.g. "ghcr.io/agenttier" allows
// "ghcr.io/agenttier/sandbox-langgraph:v1").
func registryPrefixAllowed(image string, allowed []string) bool {
	for _, p := range allowed {
		if p == "" {
			continue
		}
		if strings.HasPrefix(image, p) {
			return true
		}
	}
	return false
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
		// Reject shell metacharacters: writeFiles interpolates the path into
		// a `sh -c` command via single-quoting, so an embedded quote /
		// backtick / backslash / newline would break out and inject. Mirrors
		// router.sandboxFilePath's allowlist (kept in sync, not imported,
		// since the agent package must not depend on the router package).
		if strings.ContainsAny(f.Path, "'`\\\n\r") {
			return fmt.Errorf("files[%d].path contains disallowed characters", i)
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
//
// The install log is stored out-of-band in a per-sandbox ConfigMap (see
// writeInstallLog) so the Sandbox CR stays small. Status holds only a
// reference to that ConfigMap; the Web UI / SDK fetch the log on demand.
func (h *Handler) persistStatus(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, resp *ConfigureResponse, installLog string, maxConcurrent int32, defaultTimeout time.Duration) error {
	// Write the install log ConfigMap first. If this fails we still want
	// the status update to land — the configure itself succeeded; the
	// log is debug data. So we log + continue, leaving InstallLogConfigMapRef
	// nil so a subsequent GET returns 404.
	var installLogRef *agenttierv1alpha1.LocalObjectReference
	cmName, err := h.writeInstallLog(ctx, sandbox, installLog)
	if err != nil {
		h.opts.Logger.Warn("failed to write install log ConfigMap; status will omit reference",
			"sandbox", sandbox.Name, "error", err)
	} else if cmName != "" {
		installLogRef = &agenttierv1alpha1.LocalObjectReference{Name: cmName}
	}

	var lastErr error
	for i := 0; i < 3; i++ {
		// Re-fetch so we always patch the latest resourceVersion.
		fresh := &agenttierv1alpha1.Sandbox{}
		if err := h.opts.K8sClient.Get(ctx, ctrlClientKey(sandbox), fresh); err != nil {
			return err
		}
		now := resp.LastConfiguredAt
		fresh.Status.AgentConfigure = &agenttierv1alpha1.AgentConfigureStatus{
			LastConfiguredAt:            &now,
			InstallCommandHash:          resp.InstallCommandHash,
			Entrypoint:                  resp.Entrypoint,
			InstallExitCode:             resp.InstallExitCode,
			InstallLogConfigMapRef:      installLogRef,
			MaxConcurrentInvokes:        maxConcurrent,
			DefaultInvokeTimeoutSeconds: int32(defaultTimeout.Seconds()),
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

// installLogConfigMapName returns the canonical ConfigMap name for a
// sandbox's install log. We use a stable name (rather than a random
// suffix) so re-configures overwrite cleanly without leaving orphan
// ConfigMaps to garbage-collect.
func installLogConfigMapName(sandboxName string) string {
	return sandboxName + "-install-log"
}

// writeInstallLog upserts a per-sandbox ConfigMap holding the trailing
// installLogTailBytes of stdout/stderr from the most recent install run.
// The ConfigMap is owner-referenced to the Sandbox so it gets garbage-
// collected when the Sandbox is deleted — no stale logs left behind.
//
// Empty input means "no install command ran"; in that case we delete any
// stale ConfigMap from a prior run (so the UI doesn't surface a log that
// no longer corresponds to the resolved configuration) and return
// ("", nil).
func (h *Handler) writeInstallLog(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox, log string) (string, error) {
	cmName := installLogConfigMapName(sandbox.Name)
	cm := &corev1.ConfigMap{}
	getErr := h.opts.K8sClient.Get(ctx, client.ObjectKey{Namespace: sandbox.Namespace, Name: cmName}, cm)

	if log == "" {
		// No log to persist — clean up any stale CM so the UI doesn't
		// keep showing an old install run's output.
		if errors.IsNotFound(getErr) {
			return "", nil
		}
		if getErr != nil {
			return "", getErr
		}
		if err := h.opts.K8sClient.Delete(ctx, cm); err != nil && !errors.IsNotFound(err) {
			return "", err
		}
		return "", nil
	}

	// Owner reference back to the Sandbox so this CM is GC'd when the
	// sandbox is deleted. Controller is true so the CM is treated as a
	// dependent the sandbox owns.
	t := true
	owner := metav1.OwnerReference{
		APIVersion:         agenttierv1alpha1.GroupVersion.String(),
		Kind:               "Sandbox",
		Name:               sandbox.Name,
		UID:                sandbox.UID,
		Controller:         &t,
		BlockOwnerDeletion: &t,
	}

	if errors.IsNotFound(getErr) {
		newCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:            cmName,
				Namespace:       sandbox.Namespace,
				OwnerReferences: []metav1.OwnerReference{owner},
				Labels: map[string]string{
					"agenttier.io/sandbox": sandbox.Name,
					"agenttier.io/kind":    "install-log",
				},
			},
			Data: map[string]string{"log": log},
		}
		if err := h.opts.K8sClient.Create(ctx, newCM); err != nil {
			return "", err
		}
		return cmName, nil
	}
	if getErr != nil {
		return "", getErr
	}

	// Update existing — overwrite the log payload, refresh the owner
	// reference (in case the sandbox UID rotated through a delete + recreate
	// with the same name).
	cm.Data = map[string]string{"log": log}
	cm.OwnerReferences = []metav1.OwnerReference{owner}
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels["agenttier.io/sandbox"] = sandbox.Name
	cm.Labels["agenttier.io/kind"] = "install-log"
	if err := h.opts.K8sClient.Update(ctx, cm); err != nil {
		return "", err
	}
	return cmName, nil
}

// resolveAgentCaps returns (maxConcurrentInvokes, defaultInvokeTimeout) for
// the sandbox, preferring the merged AgentSpec the controller persists on
// status at create time. The controller already walks the full template
// inheritance chain via TemplateResolver, so reading from status correctly
// surfaces caps inherited from parent templates — a child template that
// only sets MaxConcurrentInvokes via inheritance still gets the cap
// enforced.
//
// Falls back to direct template lookup for sandboxes created before the
// status field was added (no migration churn — the next reconcile populates
// it). Final fallback is zeros, meaning "use Router defaults."
func (h *Handler) resolveAgentCaps(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) (int32, time.Duration) {
	agentSpec := sandbox.Status.ResolvedAgentSpec

	// Fallback path: status field empty (legacy sandbox or no template
	// ref). Look at the directly referenced template — same path the
	// previous implementation took, retained so we don't regress
	// pre-existing sandboxes during a rolling controller upgrade.
	if agentSpec == nil && sandbox.Spec.TemplateRef != nil {
		agentSpec = h.directTemplateAgentSpec(ctx, sandbox)
	}

	if agentSpec == nil {
		return 0, 0
	}

	var maxConcurrent int32
	if agentSpec.MaxConcurrentInvokes != nil {
		maxConcurrent = *agentSpec.MaxConcurrentInvokes
	}
	var defaultTimeout time.Duration
	if agentSpec.DefaultInvokeTimeout != nil {
		defaultTimeout = agentSpec.DefaultInvokeTimeout.Duration
	}

	// Clamp the resolved value against the cluster ceiling if a
	// PolicyResolver is configured. Empty policy or unset ceiling means
	// the resolved value wins unchanged.
	if h.opts.PolicyOf != nil {
		if policy, err := h.opts.PolicyOf(ctx, sandbox.Namespace); err == nil {
			maxConcurrent = governance.ClampConcurrency(policy, maxConcurrent)
		}
	}

	return maxConcurrent, defaultTimeout
}

// directTemplateAgentSpec is the legacy fallback path — fetches only the
// directly referenced template, ignoring inheritance. Kept narrow so the
// happy path (status.resolvedAgentSpec) is the dominant code path going
// forward.
func (h *Handler) directTemplateAgentSpec(ctx context.Context, sandbox *agenttierv1alpha1.Sandbox) *agenttierv1alpha1.AgentSpec {
	kind := sandbox.Spec.TemplateRef.Kind
	if kind == "" {
		kind = "ClusterSandboxTemplate" // sandbox-template namespacing handled by lookup below
	}

	switch kind {
	case "ClusterSandboxTemplate":
		t := &agenttierv1alpha1.ClusterSandboxTemplate{}
		if err := h.opts.K8sClient.Get(ctx, ctrlClientKeyClusterTemplate(sandbox.Spec.TemplateRef.Name), t); err == nil {
			if t.Spec.Harness != nil {
				return t.Spec.Harness.Agent
			}
		}
	case "SandboxTemplate":
		ns := sandbox.Spec.TemplateRef.Namespace
		if ns == "" {
			ns = sandbox.Namespace
		}
		t := &agenttierv1alpha1.SandboxTemplate{}
		if err := h.opts.K8sClient.Get(ctx, ctrlClientKeyNamespacedTemplate(ns, sandbox.Spec.TemplateRef.Name), t); err == nil {
			if t.Spec.Harness != nil {
				return t.Spec.Harness.Agent
			}
		}
	}

	return nil
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

// handleGetInstallLog serves the install-log ConfigMap that
// persistStatus wrote during the most recent /configure run. Lazy-loaded
// from a separate ConfigMap (rather than inline on Sandbox.status) so
// the CR stays small at scale — see the InstallLogConfigMapRef field
// docs on AgentConfigureStatus.
//
// Response shape mirrors the body persisted: `{"log": "<bytes>"}`. When
// no log exists (sandbox never configured, or the most recent configure
// had nothing to install), returns 404 — consistent with "the install
// log doesn't exist" rather than "the sandbox doesn't exist."
func (h *Handler) handleGetInstallLog(w http.ResponseWriter, r *http.Request) {
	sandbox, _, ok := h.loadSandbox(w, r)
	if !ok {
		return
	}

	cmName := installLogConfigMapName(sandbox.Name)
	cm := &corev1.ConfigMap{}
	if err := h.opts.K8sClient.Get(r.Context(), client.ObjectKey{
		Namespace: sandbox.Namespace,
		Name:      cmName,
	}, cm); err != nil {
		if errors.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "no install log available for this sandbox")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read install log: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `{"log": %q}`, cm.Data["log"])
}
