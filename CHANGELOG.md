# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Warm pool now actually claims pods (instant start works).** Pool Pods + PVCs were provisioned in the install namespace (e.g. `agenttier`), but the Router creates Sandboxes in `default`. The claim guard required `sandbox.Namespace == InstallNamespace`, so a claim was never attempted — and even if it had been, a claimed Pod can't move namespaces to reach the Sandbox. Net effect: on any install whose namespace wasn't `default`, every sandbox cold-started while idle pool Pods piled up (and leaked PVCs) in the install namespace. The warm pool now decouples its **config namespace** (the ConfigMap, still in the install namespace) from its **pod namespace** (pool Pods + PVCs, provisioned in the sandbox namespace `default` where Sandboxes are created), so a claimed Pod is reused in place. New `SANDBOX_NAMESPACE` env / `--sandbox-namespace` flag on both the controller and router (Helm `defaults.sandboxNamespace`, default `default`). Added a regression test and verified end to end on the live cluster: a new sandbox claimed a ready pool Pod and went Running instantly, and the reconciler replenished the pool.
- **Warm pool status no longer shows "off" with multiple pools.** The Web UI left-nav glance widget read the legacy single-pool scalar (`desiredCount`), which the API only populates when exactly one pool is configured. With two or more templates configured it read as zero and the widget showed "Warm pool: off" despite ready pods. The widget now aggregates the per-template `pools[]` array (total ready / target across all pools), falling back to the legacy scalar for single-pool installs.

## [v0.6.0] — 2026-06-01

### Security

- **Router authentication is now real and fails closed (P0 fix).** The Router previously shipped a complete OIDC validator that nothing called, while the wired path was a stub: `verifyRS256Signature` returned `nil` for any input (accepting forged tokens) and `validateJWT`/`validateAPIKey` returned "not implemented". In effect, dev installs granted blanket admin to every request and prod installs 401'd everything. This change:
  - Implements real RS256 verification (`crypto/rsa` `VerifyPKCS1v15` + SHA-256) in `pkg/router/auth/oidc.go` — no new dependency, no Go-toolchain bump. Added regression tests that reject forged-signature and tampered-payload tokens.
  - Wires the real `auth.OIDCValidator` into the Router and calls it from `authMiddleware`; JWTs are verified against the issuer's JWKS (signature, issuer, audience, expiry) with admin-group → `isAdmin` mapping.
  - Implements API-key authentication end to end: `POST /user/api-keys` mints a key (returned in plaintext exactly once), stored as a SHA-256 hash in a per-install-namespace Secret; `GET` lists metadata only; `DELETE` revokes (ownership-checked). Validation goes through the existing LRU-cached `APIKeyValidator`. These three endpoints were previously `501 not implemented`.
  - **Fails closed.** Dev-mode blanket-admin is now gated behind an explicit `--dev-auth` flag (Helm `auth.devAuth: true`, default `false`). A production install that simply forgot to set an OIDC issuer now rejects every request with 401 instead of silently granting admin. The Router logs a loud warning when dev-auth is active and a warning when neither auth path is configured.
  - Added the first tests for `pkg/router/auth` (previously zero) covering signature rejection, expiry, issuer/audience mismatch, unknown-kid, admin-group mapping, and the API-key validator's hit/miss/expiry/caching behavior.
- **Validating/mutating admission webhook for Sandbox resources (closes the kubectl-bypass).** Governance previously ran only in the Router's `POST /sandboxes` handler, and `spec.createdBy` was set by the Router — so anyone with direct cluster access (`kubectl apply`, GitOps, a script) could forge ownership and skip every governance cap. New opt-in webhook (`pkg/controller/webhook`, Helm `optional.admissionWebhook.enabled`) served by the controller:
  - On CREATE, overwrites `spec.createdBy` from the authenticated `AdmissionRequest` UserInfo (a forged `createdBy` in the request body is replaced with the real caller), and runs the same `governance.Check` the Router runs — rejecting over-quota / disallowed-template / disallowed-image creates at admission, for ALL writers.
  - On UPDATE, rejects changes to immutable fields (`mode`, `templateRef`, `cloneFromSnapshot`) and to `createdBy`.
  - Registered as a `MutatingWebhookConfiguration` (mutating webhooks can both patch and deny). Serving certs are issued by cert-manager (self-signed Issuer + Certificate; CA injected into the webhook config via `cert-manager.io/inject-ca-from`). `failurePolicy: Fail` by default (fail-closed; configurable). Requires cert-manager — when disabled, Router-side governance still applies to every create through the API.
  - Six handler tests cover the forged-identity overwrite, governance allow/deny, and the immutability rules. Verified end-to-end on the live cluster: a `kubectl apply` with a forged `createdBy` was rewritten to the authenticated user, a disallowed-template create was denied at admission, and `createdBy`/`mode` changes on update were rejected.

## [v0.5.5] — 2026-05-28

### Added

- **OpenTelemetry observability is now actually wired up.** Both `cmd/router` and `cmd/controller` initialize `pkg/otel.Setup()` at boot, reading `OTEL_EXPORTER_OTLP_ENDPOINT` (and the optional `OTEL_EXPORTER_OTLP_INSECURE`) from the environment. When the endpoint is empty the SDK installs a `NeverSample` provider — cheap, no exports, and W3C Trace Context still propagates so an operator can flip the export knob on without restarting upstream callers. The router now wraps its mux with `otelhttp.NewHandler`, producing one server span per request named `router.<method>` (e.g. `router.GET`, `router.POST`); health probes, metrics scrape, and WebSocket upgrades are excluded to keep span volume sane. Existing `agenttier.invoke` and `agenttier.configure` spans now correctly inherit the request span as their parent, so a single trace UI walks the full HTTP-to-pod-to-stream chain instead of starting fresh inside the agent handler. End-to-end-tested on the live `agentloft-e2e` cluster.
- **slog handler that injects trace IDs.** New `pkg/otel.NewSlogContextHandler(...)` wraps any `slog.Handler` and stamps `trace_id` and `span_id` on every record whose context carries a valid OTel span. Both mains apply it to their root logger; the router's `loggingMiddleware` already passes `r.Context()` to slog, so request log lines auto-correlate with the matching trace once an exporter is configured. Verified live: a single trace ID copied from the OTel UI pivots straight to the matching `kubectl logs` line.
- **Opt-in OpenTelemetry Collector ships with the chart.** New Helm template `helm/agenttier/templates/otel-collector.yaml` renders a Deployment + ConfigMap + Service running `otel/opentelemetry-collector-contrib:0.104.0` when `observability.otelCollector.enabled=true`. Receives OTLP gRPC on `:4317` and OTLP HTTP on `:4318`. Default exporter is `debug` so a fresh install works without external infra (incoming spans show up in the collector's container logs immediately); operators replace the exporter via `observability.otelCollector.extraConfig`. Includes a `health_check` extension so kubelet's liveness/readiness probes succeed cleanly. When the collector is enabled and `observability.otlp.endpoint` is empty, the router and controller deployments auto-point `OTEL_EXPORTER_OTLP_ENDPOINT` at the in-cluster Service — zero manual env-var wiring. Optional `ServiceMonitor` for the collector's self-metrics endpoint, gated on `observability.prometheus.serviceMonitor`. New docs page `docs/docs/observability.md` covers traces, metrics, log correlation, and how to wire common backends (Honeycomb, Datadog, Tempo, Jaeger).
- **Sandbox cloning via VolumeSnapshot.** `POST /api/v1/sandboxes/{id}/clone` now creates a CSI VolumeSnapshot of the source sandbox's PVC, then provisions a new Sandbox CR whose PVC is hydrated from that snapshot via `dataSource: VolumeSnapshot`. Body is `{name?, snapshotClass?}` — both optional; `name` defaults to `<source>-clone-<ts>` and `snapshotClass` falls back to the cluster's default `VolumeSnapshotClass`. Returns 202 Accepted with the snapshot name in the body so the caller can poll the new sandbox's phase. The `Sandbox` CRD gains `spec.cloneFromSnapshot` (the controller reads this and wires the PVC `dataSource` accordingly) and the new `LocalObjectReference` helper type. RBAC for `volumesnapshots` was already in the chart from the Phase 1 cloning scaffolding. Three unit tests cover the happy path, the no-PVC source rejection, and the invalid-name rejection.
- **`sandbox-rl` reference image.** Reinforcement-learning reference image bundling PyTorch 2.5.1 (CPU build), Ray 2.40.0 (`ray[default,rllib,tune]`), Gymnasium 1.0.0, Stable-Baselines3 2.4.0, plus numpy/scipy/pandas/matplotlib/tensorboard pinned. Multi-arch (linux/amd64 + linux/arm64). Two ready-to-run examples ship at `/opt/agenttier/examples/`: `train.py` (self-contained PPO loop on CartPole-v1, finishes in ~2 minutes on 4 CPU cores, writes a checkpoint to `/workspace/.rl-cache/checkpoints/`) and `agent.py` (`/invoke`-shaped wrapper that loads a checkpoint, runs one episode of the named Gym env, and prints `{episode_reward, episode_length}` JSON on stdout — falls back to a random policy when no checkpoint is present so `/invoke` works on a fresh sandbox). New `rl-rollout` `ClusterSandboxTemplate` shipped in the chart sized for typical PPO workers (2-4 CPU, 4-8 GiB memory, 20 GiB storage). New `defaults.rl.image` Helm value. Wired into the image-prepull DaemonSet, the release matrix (multi-arch + cosign + SBOM), the CI Trivy CVE scan, and `buildspec.yml` for the live `agentloft-e2e` cluster.
- **Automated post-release retention.** New `hack/release-retention.sh` script + matching `release-retention` job in `release.yml` — runs after `github-release` succeeds and prunes everything older than the latest 3 GA releases per the policy in `.kiro/steering/project.md`. Older Releases are demoted to pre-release (NOT deleted, so CLI binary deep links keep working); container package versions tagged for releases that fell out of the latest-3 window are deleted from ghcr.io; untagged container manifests older than 30 days are deleted; `gh-pages` `charts/index.yaml` is trimmed to the 3 most recent versions. Git tags, PyPI versions, cosign signatures whose targets are kept, and historical CHANGELOG entries are NEVER pruned. Idempotent and dry-run-capable for safe iteration.
- **Web terminal stays pinned to the bottom during TUI sessions.** `web-ui/src/pages/Terminal.tsx` now uses xterm's `viewportY === baseY` check before each `term.write(...)` to remember whether the user was at the bottom. After the write, when (and only when) the user was already at the bottom we batch a `scrollToBottom()` into the next animation frame so new output keeps the input prompt visible during fast Claude Code redraws. When the user has scrolled up to read history, the viewport is left alone — they retain their scroll position while output continues to land below.

### Changed

- **Install log moved out of CR status into a per-sandbox ConfigMap.** Every agent-mode `/configure` run previously wrote up to 8 KiB of trailing install-log bytes inline on `Sandbox.status.agentConfigure.installLog`. That bloated etcd objects, multiplied watch churn across every controller / Router replica, and dumped unrelated noise into `kubectl describe sandbox`. The log now persists to a dedicated `<sandbox>-install-log` ConfigMap in the same namespace, owner-referenced to the Sandbox so it's garbage-collected automatically when the sandbox is deleted. The CR status carries only an `installLogConfigMapRef` pointer (new `LocalObjectReference` helper type). New `GET /api/v1/sandboxes/{id}/configure/install-log` endpoint serves the log lazily — Web UI / SDK fetch it on demand instead of pulling it down with every sandbox-detail request. Two new unit tests cover the 404-when-absent and the happy-path GET.

### Security

- **Span attributes no longer include the raw OIDC `sub` claim.** Both `agenttier.invoke` and `agenttier.configure` spans previously stamped the user's stable OIDC subject as the `actor` attribute, which becomes PII the moment traces are exported to a third-party store like Honeycomb or Datadog. Replaced with `actor_hash` — a stable 8-character SHA-256 prefix exposed via `pkg/otel.HashActor()`. Operators retain enough signal to group spans by user within an investigation window without leaking re-identifiable identity into telemetry.

## [v0.5.0] — 2026-05-28

### Added

- **Governance gate at agent `/configure`.** The agent-mode `POST /configure` endpoint now re-checks the resolved sandbox against the namespace's governance policy before writing any files or running install. Three policy fields are honored: `allowedTemplates` (re-checked against `status.resolvedTemplate`), `allowedAgentImages` (re-checked against `spec.image.repository` when the sandbox overrides the template image), and `maxConcurrentInvokesPerSandbox` (already clamped via `ClampConcurrency`). Server-side correctness limits — 32 MiB per file, 128 MiB aggregate, 200 files max per request — protect the Router from misbehaving callers regardless of policy. Denied configures return HTTP 403 before any I/O and emit a `ConfigureDenied` Kubernetes event on the sandbox CR for the audit trail. Six new unit tests cover the policy paths; existing `/configure` tests remain green.
- **Smarter self-healing for sandbox pod failures.** The controller's auto-restart path now distinguishes between **infrastructure failures** (OOMKilled, Evicted, NodeLost, pod disappearance, CrashLoopBackOff) and **application errors** (CMD exited non-zero with a Completed reason). Infra failures trigger the existing exponential-backoff restart loop (10s → 20s → 40s → 80s → 160s, capped at MaxRestartCount=5); app errors go straight to terminal Error so a misconfigured CMD doesn't pointlessly burn the restart budget. New `RestartCountResetWindow=5m`: a pod that's been stably Ready for 5 minutes has its `Status.RestartCount` reset to 0, so a sandbox that experienced transient flaps over a long uptime gets a fresh restart budget for the next infra failure. The Error → Creating transition now goes through `reconcileError` (which actually applies the backoff) instead of resetting to Creating immediately. Verified end-to-end on the live `agentloft-e2e` cluster: force-deleted pod recovered after exactly the 20s backoff window with `AutoRestarted` + `Restarting` events on the timeline.
- **Strands Agents on AWS Bedrock reference image (`sandbox-strands-bedrock`).** New turnkey image for the [AWS Strands Agents Python SDK](https://strandsagents.com/) preconfigured to talk to AWS Bedrock via IRSA-injected credentials, plus a matching `strands-bedrock` `ClusterSandboxTemplate` shipped in the Helm chart. Strands defaults to Bedrock and uses the AWS SDK default credential chain — so on an EKS sandbox with the namespace's `default` ServiceAccount annotated with `eks.amazonaws.com/role-arn=...`, agent code Just Works with zero runtime config. Bundles `strands-agents==1.41.0`, `strands-agents-tools==0.6.0`, `boto3==1.43.16`, AWS CLI v2, and a ready-to-run sample `agent.py` at `/opt/agenttier/examples/agent.py` that reads stdin and prints the agent's response (drop-in for AgentTier `/invoke`). New Helm value `defaults.strandsBedrock.image` + `defaults.strandsBedrock.irsaRoleArn`. Wired into the image-prepull DaemonSet, the release matrix (multi-arch + cosign + SBOM), the CI Trivy CVE scan, and `buildspec.yml` for the live `agentloft-e2e` cluster.
- **OpenClaw on AWS Bedrock reference image (`sandbox-openclaw`).** New turnkey image for the [OpenClaw CLI](https://github.com/openclaw/openclaw) preconfigured to talk to AWS Bedrock via IRSA-injected credentials, plus a matching `openclaw-bedrock` `ClusterSandboxTemplate` shipped in the Helm chart. The image bakes a baseline `~/.openclaw/config.json` that pre-enables `plugins.entries.amazon-bedrock.config.discovery.enabled = true` (required for IRSA — OpenClaw's auto-detection otherwise only fires on `AWS_PROFILE` / `AWS_ACCESS_KEY_ID` / `AWS_BEARER_TOKEN_BEDROCK` env markers, none of which IRSA sets) and points at the `bedrock-converse-stream` API with `auth: "aws-sdk"`. Default model is `amazon-bedrock/us.anthropic.claude-3-5-sonnet-20241022-v2:0`. The entrypoint seeds the config to the writable PVC on first launch so per-sandbox customizations (additional providers, auth profiles, model overrides) persist across stop/resume. Same shape as the existing `claude-code-bedrock` reference: pin `openclaw@2026.5.19` (auto-update disabled), Node 22 (OpenClaw requires ≥22.19), AWS CLI v2, the in-pod runtime binary, telemetry off. New Helm value `defaults.openclaw.image` + `defaults.openclaw.irsaRoleArn`. Wired into the image-prepull DaemonSet, the release matrix (multi-arch + cosign + SBOM), the CI Trivy CVE scan, and `buildspec.yml` for the live `agentloft-e2e` cluster.

## [v0.4.1] — 2026-05-17

### Added

- **Hierarchical file browser + workspace zip download.** The Web UI Settings page Files panel now lets you click into folders, breadcrumb back, download a single file, download a single folder as a `.zip`, or download the entire workspace as `.zip`. New streaming Router endpoint `GET /api/v1/sandboxes/{id}/archive?path=/workspace[/subdir]` execs `tar -cf - -C <path> .` in the pod and re-encodes to a real `.zip` on the fly using Go's `archive/zip` — no `zip` binary required in any sandbox image, no full-archive buffering in the Router (one tar entry's deflate window at a time). Locked to the `/workspace` subtree. Soft cap of 5 GiB per archive. Mirror surface in the SDK (`sandbox.files.archive(destination, path="/workspace")` on both sync and async clients) and Python CLI (`agenttier sandbox files archive <id> -o ws.zip [--path /workspace/sub]`). Closes the hierarchical-file-browser item on the project board.
- **Chromium / Playwright runtime libs baked into `sandbox-claude-code`.** The Dockerfile now installs the canonical Microsoft Playwright Chromium dep set (`libnss3`, `libnspr4`, `libatk1.0-0`, `libatk-bridge2.0-0`, `libatspi2.0-0`, `libcups2`, `libdbus-1-3`, `libdrm2`, `libegl1`, `libgbm1`, `libglib2.0-0`, `libgtk-3-0`, `libxkbcommon0`, `libxcomposite1`, `libxdamage1`, `libxfixes3`, `libxrandr2`, `libx11-6`, `libx11-xcb1`, `libxcb1`, `libxext6`, `libxshmfence1`, `libasound2`, `libpango-1.0-0`, `libcairo2`, `fonts-liberation`). Agents running inside Claude Code can now run `npx playwright install chromium` and launch a headless Chromium without needing root or write access to the read-only sandbox rootfs. The Chromium binary itself still downloads to the writable PVC at `/workspace/.cache/ms-playwright/`, where it persists across stop/resume.

### Fixed

- **Claude Code Bash tool wedged after failed self-update.** The `sandbox-claude-code` image now sets `ENV DISABLE_AUTOUPDATER=1` and `ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`. Sandbox pods run with a read-only root filesystem (security hardening), so Claude Code's `npm install -g @anthropic-ai/claude-code` self-update attempt at every launch failed with EROFS — and Claude Code v2.1.x has a known regression where a failed self-update leaves its persistent Bash tool's bash subprocess in a wedged state, returning exit 1 with no output for every command (`whoami`, `pwd`, anything). Disabling the auto-updater entirely sidesteps the doomed code path; the version pinned at image build time is the only one that ever runs, matching the release discipline of every other reference image. Reproduced on the live cluster from the user-visible `✗ Auto-update failed · Try claude doctor` banner; verified post-fix with the `dev-archive-claude` and `dev-chromium` builds.

## [v0.4.0] — 2026-05-17

### Added

- **Web UI overhaul: per-sandbox settings page, mode badge, gear icon, per-template warm pool editor, headroom editor.** The Dashboard card now shows a Code or Agent badge next to the status, the template moves out of the title row to a plain metadata line above the Created date, and a gear icon at the top-right opens `/sandbox/<id>/settings` in a new tab. The new per-sandbox settings page hosts everything that was previously crammed into an inline "Advanced" expander (port forwards, files, agent invoke), with room for future per-sandbox controls (governance overrides, env vars, network rules) without further crowding the card. The cluster Settings page replaces the legacy single-template warm-pool form with a per-template editor (add row, remove row, save sets the canonical `pools[]` shape), and adds a Headroom block that resizes the chart's spare-node pause-Pod Deployment without `helm upgrade` (replicas + per-replica CPU + per-replica memory, all admin-gated).
- **Cluster status nav widget** — left nav shows live `nodes ready / total`, `sandbox + total pods`, `headroom spare`, plus a green dot when Cluster Autoscaler is running. Backed by `GET /api/v1/cluster/status`. Refreshes every ten seconds.
- **`GET` and admin-gated `PUT /api/v1/cluster/headroom`** — read/write the chart's optional `agenttier-headroom` Deployment so operators can resize the spare-node reservation at runtime. PUT is bounded to `replicas ∈ [0, 50]` and validates CPU/memory through `resource.Quantity` before applying. RBAC adds `apps/deployments` get/list/watch/update/patch.
- **Cluster autoscaling out of the box** — opt-in upstream Cluster Autoscaler installs cloud-neutral via Helm (`optional.clusterAutoscaler.enabled: true`), works on EKS, GKE, AKS, OpenStack, Cluster API. Companion `optional.headroom.enabled: true` keeps N+1 spare-node capacity warm via pause Pods at negative `PriorityClass`: real sandboxes preempt them instantly, evicted Pods trigger CAS to add the next spare in the background. Net effect: sandboxes never wait on cold ASG round-trips. New `docs/docs/scaling.md` page covers sizing math + cost trade-offs + verification. Tested end-to-end on the live `agentloft-e2e` cluster: scale-up 2 → 4 → 6 nodes during a 12-sandbox burst, scale-down 6 → 3 nodes (= 2 control-plane + 1 spare held by headroom) when sandboxes deleted.
- **WebGL renderer for the browser terminal** — `xterm-addon-webgl` collapses each redraw into a single GPU blit so full-screen TUI updates (Claude Code parallel work, vim, htop) don't flicker through partial-row paints on the main thread. Falls back to the DOM renderer automatically when WebGL is unavailable.
- **In-pod WebSocket `/pty` endpoint and HTTP-PTY browser-terminal transport** — the browser terminal was the last call path still going through `kubectl exec` SPDY, which means the EKS apiserver was recycling long-lived streams every 20-60 minutes regardless of LB tuning. New `/pty` WebSocket endpoint on `pkg/sandboxruntime` that spawns a child shell with `creack/pty`, bidirectionally bridges WS frames to the PTY, runs the same 30s keepalive cadence as the Router-side bridge, and (when the Router passes a session name) wraps the spawn in `tmux new-session -A -s <name>` for resume-on-reconnect uniformity with the SPDY path. Router-side `pkg/router/pty_dispatch.go` mirrors `exec_dispatch.go`'s decision tree (token Secret + PodIP + healthy `/healthz` → HTTP-PTY, anything else → SPDY fallback) so opted-in sandboxes silently shift to the new transport without any configuration churn. Existing sandboxes without `useHTTPExec: true` continue on SPDY unchanged.
- **`agenttier` Web UI now resumes the same shell across drops on opted-in sandboxes** — when the template has `harness.useHTTPExec: true` and the in-pod runtime is healthy, the WebSocket terminal goes pod-to-Router-to-browser without an apiserver hop, so the every-20-minutes drop disappears. Older sandboxes still benefit from the tmux wrap that landed in v0.3.5 — they keep dropping but the shell + running processes survive.
- **Python SDK retry layer** — `RetryConfig` (configurable max retries, exponential backoff with jitter, retryable status codes, `Retry-After` honoring) opt-in via `AgentTierClient(retry=...)` and `AsyncAgentTierClient(retry=...)`. SSE-streaming endpoints (`/configure`, `/invoke`) bypass retries to avoid duplicate events. Closes the SDK retry-logic-for-transient-failures item on the project board.
- **`mode` field on the sandbox JSON response** — `GET /api/v1/sandboxes` and `GET /api/v1/sandboxes/{id}` now emit `"mode": "code" | "agent"` (defaults to `"code"` for older CRs that predate the field). Web UI uses this for the new mode badge.
- **`fetchSandbox(id)` SDK helper** in the Web UI's `api/client.ts` for the per-sandbox settings page.

### Fixed

- **Cross-replica invoke cancel** — when the Router runs more than one replica, `POST /invoke/cancel` could land on a different pod from the in-flight invoke and 404 because the invoke registry lived in a per-pod `sync.Map`. The cancel handler now also tries the in-pod sandbox runtime, whose registry is reachable from any Router replica. The fix code shipped on `main` weeks ago via the HTTP-exec opt-in path; this release verifies it end-to-end on the live `agentloft-e2e` cluster with 2 router replicas + the langgraph sandbox.
- **Browser terminal scroll-back lost in fullscreen TUIs.** Claude Code (and vim, less, htop) used the alt-screen capability, which xterm.js doesn't preserve scrollback for — wheel events were converted to up/down arrow keystrokes, which Claude Code's prompt mapped to history navigation, so scrolling cycled through previous prompts. Fixed by stripping `smcup`/`rmcup` at the tmux layer (`set -ga terminal-overrides 'xterm*:smcup@:rmcup@'`); alt-screen apps now run in the main buffer and xterm.js scrolls natively. Mouse selection still works because tmux's mouse mode stays off. The canonical fix from the tmux community since ~2011.
- **Warm-pool pods missing `/tmp` tmpfs.** The earlier "Mount writable /tmp tmpfs into sandbox containers" commit only added the mount to `pod_builder.go`'s cold-start path. `pkg/controller/warmpool/pool.go`'s `createPoolPod` had its own container constructor that was missed, so any sandbox that claimed a pool pod got a `/tmp`-less environment that broke tmux (`couldn't create directory /tmp/tmux-1000`), pip, npm, and the entrypoint's `agenttier-tmux.conf` write. Mirrored the cold-start path: 256 MiB Memory-backed `emptyDir` at `/tmp` on the warm-pool pod's container.
- **Mouse-wheel cycled through Claude Code prompt history.** Pre-tmux-fix workaround that suppressed wheel events in alt-screen mode is no longer needed (and incorrectly used xterm.js v5.4's `attachCustomWheelEventHandler` API which our v5.3 pinning doesn't expose); reverted in `Terminal.tsx` since the alt-screen disable above is the proper root-cause fix.

### Changed

- `docs/docs/architecture.md` data-flow section for terminal sessions documents both transports and how to verify which one a session used (Router log lines `terminal session via HTTP-PTY` vs `HTTP-PTY fallback to SPDY`).
- `docs/docs/troubleshooting.md` adds a top-of-page entry on the 20-60 minute terminal drop symptom and the `useHTTPExec: true` opt-in to eliminate it.
- `docs/docs/sdk.md` documents the new `RetryConfig` knob.
- **Removed `pkg/credentials/`** — placeholder code that returned static role ARNs and never wired to anything. Real credential injection happens via `Sandbox.spec.credentials` (Secret-as-env or Secret-as-volume) plus IRSA / Workload Identity / Azure Workload Identity at the ServiceAccount level — all already implemented in `pkg/controller/pod_builder.go`. Closes the credentials-providers-return-placeholders item on the project board.
- `buildspec.yml` aligned with the live cluster: pushes to `agentloft/*` ECR (the namespace the cluster pulls from), adds `sandbox-langgraph`, and uses repo root as Docker build context for the reference-image Dockerfiles so the in-pod runtime binary gets baked in.

## [v0.3.5] — 2026-05-16

### Added

- **`agenttier` CLI shipped via `pip install agenttier`** — Python entry point on top of the SDK. `pip install` now gives users both the SDK and the `agenttier` shell command on `PATH`. Mirrors the Go CLI's surface and adds full lifecycle management: `sandbox list/get/create/stop/resume/delete/exec/wait`, `sandbox files {ls,cat,upload,download,write}`, `sandbox ports {list,forward,remove}`, `template {list,get}`, plus the existing `configure` and `invoke`. Adds `login` for saving endpoint and credentials to `~/.config/agenttier/config.json` and `whoami` for verifying auth. Every command supports `--output text|json` for scriptable use. (Closes [todo 5.7](#).)
- **Per-template warm pools** — `Config.Pools` is the new canonical shape; each entry warms one template with its own desired count, scaled independently. `Reconcile` walks every entry and converges them in parallel; orphaned pods (templates dropped from config) are cleaned up automatically. Old single-template config shape (`Config.Template` + `Config.DesiredCount`) is auto-migrated on read via `Config.Normalize()`, so existing ConfigMaps keep working through one rolling controller upgrade.
- **Per-IP and per-user rate limiting on the Router** — opt-in via `router.rateLimit.perIPPerSecond` and `router.rateLimit.perUserPerSecond` Helm values (both default to `0` = off). Token-bucket implementation backed by `golang.org/x/time/rate`. Per-IP runs before `authMiddleware` so anonymous abuse gets throttled; per-user (Sub claim) runs after auth on the `/api/v1` subrouter. Health endpoints (`/healthz`, `/readyz`, `/metrics`) and WebSocket terminals (`/ws/*`) are always exempt. 429 responses include `Retry-After` and a structured JSON body matching the existing `concurrency_exceeded` shape.
- **Trivy CVE scanning for all four sandbox base images in CI** — `sandbox-general`, `sandbox-claude-code`, `sandbox-minimal`, and `sandbox-langgraph` are scanned on every push to main, each with its own SARIF category in the GitHub Security tab. Closes the supply-chain gap where a CVE in a base layer could land in user workloads via `helm upgrade` with no early-warning channel.
- **`Sandbox.status.resolvedAgentSpec` field** — additive optional field, persists the merged `AgentSpec` so `/configure` doesn't have to re-walk the template inheritance chain on every request. CRD manifests regenerated.
- **`ShareLink.TokenHash` and `ShareLink.ID` fields** — preventive secure-by-default schema for the share-link feature (sharing not yet GA). New `pkg/router/sharelinks` package with `Generate()` returning `(id, raw, hash)` and `Validate(link, raw)` using `crypto/subtle.ConstantTimeCompare`. SHA-256 hashing (raw tokens are 256-bit cryptographic random, fast hash is appropriate). Legacy `Token` field deprecated for one minor release of backward compatibility.
- New **CLI command reference** docs page at `/cli-reference/` with the full command tree, flags, and examples.
- **Tutorials section in the docs** — hub page plus four hands-on walkthroughs (Web UI, Python SDK, code mode, agent mode). Each tutorial assumes AgentTier is already installed and walks end-to-end through real workflows with copy-pasteable commands.

### Fixed

- **Heredoc injection truncated files containing the marker string** (P0) — the file deployer init container used a fixed-string heredoc terminator (`AGENTLOFT_EOF`); user files containing that string on their own line were silently truncated. Now uses `printf '%s' '<base64>' | base64 -d`, the same pattern `/configure` already uses. End-to-end verified by executing a generated init script through `/bin/sh` against content containing the literal marker plus quotes, apostrophes, and `$vars`.
- **Warm pool hardcoded to `default` namespace** (P0) — `GetStatus`, `SetConfig`, `Claim`, `listPoolPods`, and `createPoolPod` all assumed the pool lived in the `default` namespace; on every real install (which runs in `agenttier`) the pool was mis-targeted and Sandboxes never claimed from it. Namespace is now threaded through every operation, plumbed from `POD_NAMESPACE` injected via the Kubernetes downward API in both controller and router Deployments.
- **Restart count off-by-one between `handleInfrastructureFailure` (`>`) and `reconcileError` (`>=`)** — infrastructure failures got one extra restart attempt past the documented `MaxRestartCount`. Standardized on `>=` in both call sites.
- **Router `ReadTimeout: 0` exposed every endpoint to slowloris** — required for WebSocket but inherited by REST, SSE, file PUT/GET, and port-preview. Added `ReadHeaderTimeout: 5s` which bounds only the request-line + headers phase and coexists cleanly with WebSocket upgrades.
- **`/readyz` always returned 200 even when the K8s API was unreachable** — the Service kept routing traffic to broken Router pods. Probe now does a cheap `client.List(&SandboxList{}, Limit=1)` with a 3-second context timeout, returning 503 with a diagnostic body on failure.
- **Warm pool `Claim` had a list-then-update race** — two concurrent claimers could pick the same pod and silently `continue` on the loser's `Update`. Now uses explicit `errors.IsConflict` / `errors.IsNotFound` detection with a 3-attempt retry loop that re-Lists each iteration. Added `agenttier_warmpool_claim_conflicts_total` counter for contention visibility.
- **Template inheritance not walked when resolving agent caps** — child templates inheriting `MaxConcurrentInvokes` from parents had the cap silently dropped to zero. Controller now persists the merged `AgentSpec` onto `Sandbox.status.resolvedAgentSpec` at create time; `/configure` reads from there with zero extra K8s round-trips. Falls back to the old direct-template lookup for legacy sandboxes that predate the status field.

### Changed

- `docs/docs/cli.md` and `docs/docs/sdk.md` now point at `cli-reference.md` and call out that `pip install agenttier` ships the CLI alongside the SDK.
- README header links the new Tutorials section.
- Left-nav: bold uppercase section labels in primary purple with indented children, top borders separating sections, dark-mode friendly.

### Security

- **Share-link tokens hashed at rest** — when the share-link feature lands end-to-end (todo 7.2), tokens will only ever exist in plaintext in the create-link API response. Past v0.3.5, raw tokens never persist on the CR or in etcd. The legacy `Token` field is honored as a fallback for one deprecation window, then removed.

## [v0.3.0] — 2026-05-13

### Added

- **Agent mode for sandboxes** — `Sandbox.spec.mode` and `SandboxTemplate.spec.mode` accept `code` (default, today's behavior) or `agent`. Agent-mode sandboxes are driven via `POST /api/v1/sandboxes/{id}/configure` (one-shot install + entrypoint registration) and `POST /api/v1/sandboxes/{id}/invoke` (Server-Sent Events streaming runner) instead of the terminal. Same Sandbox CRD, same Pod, same PVC, same NetworkPolicy, same governance, same warm pool — only the calling pattern differs. CRD additions are additive: existing sandboxes and templates default to code mode and run unchanged. (Phase 10)
- New `HarnessSpec.Agent` block carries the agent runtime contract: `entrypoint`, `installCommand`, `workingDir`, `env`, `maxConcurrentInvokes`, `defaultInvokeTimeout`. Template inheritance correctly merges the agent block (deep merge with additive env). The Router copies template-set `mode` onto each new Sandbox at create time so the agent endpoints recognize it without waiting for the controller's resolve cycle.
- `POST /configure` — uploads files into the sandbox PVC, runs an install command, records the entrypoint into `Sandbox.status.agentConfigure`. Streams stdout/stderr live as SSE so SDK / CLI / Web UI callers watch progress in real time. Idempotency keyed off SHA256(sorted files | installCommand) so re-runs are no-ops. Returns 400 on a code-mode sandbox with a clear message. 15-minute soft install timeout, 32 MiB per-file cap.
- `POST /invoke` — runs the configured entrypoint inside the sandbox and streams stdout / stderr / exit as SSE. Body bytes (or `?prompt=...`) are forwarded to the entrypoint on stdin via SPDY's native stdin channel — so 1 MB JSON bodies and other non-trivial payloads work cleanly. Closing the SSE connection cancels the in-pod process. Per-sandbox concurrency cap returns HTTP 429 with `Retry-After: 5`. Default per-invoke timeout 30 minutes (callers can lower via `?timeout=`). 15-second SSE keepalive comments survive ALB / nginx idle timeouts.
- `POST /invoke/cancel` — terminates an in-flight invoke by `invokeId`. Best-effort: returns 404 if the invoke already completed. Ownership-checked: a non-admin can only cancel their own invokes.
- Observability — both `/configure` and `/invoke` emit OTel spans (`agenttier.configure`, `agenttier.invoke`) with bounded attributes (template, actor, exit code, duration). New Prometheus metrics: `agenttier_invoke_requests_total`, `agenttier_invoke_duration_seconds`, `agenttier_invoke_throttled_total`, plus configure equivalents. Labels are `{template, outcome}`. Audit lines persist as Kubernetes events on the sandbox CR — visible via `kubectl describe sandbox` and the existing `/api/v1/audit/events` endpoint.
- Governance fields for agent mode — `Policy` gains `maxAgentSandboxes` (per-namespace cap on `mode: agent` sandboxes), `allowedAgentImages` (tighter registry allowlist than `approvedRegistries`, applied only to agent-mode image overrides), and `maxConcurrentInvokesPerSandbox` (cluster ceiling that clamps the per-template `agent.maxConcurrentInvokes`). All three default unset for zero behavior change.
- Reference image: `ghcr.io/agenttier/sandbox-langgraph` — Python 3.11 + LangGraph 0.6.4 + LangChain 0.3.27 + httpx 0.28.1 + mem0ai 0.1.115. Pinned versions, multi-arch (linux/amd64 + linux/arm64), cosign-signed, SBOM-attached. Ships a model-free sample `agent.py` at `/opt/agenttier/examples/`. New `langgraph-agent` default `ClusterSandboxTemplate`.
- Optional mem0 sidecar (`optional.agentMemorySidecar.enabled`) — when enabled and the sandbox is `mode: agent`, the controller injects a pinned `mem0/mem0-api-server` sidecar into the Pod and sets `MEM0_BASE_URL=http://localhost:8000` in the sandbox container's env. Storage lives at `/workspace/.agenttier/memory` on the workspace PVC. **Disabled by default**: the upstream mem0 image only ships arm64 today, which doesn't run on amd64 EKS / GKE node groups. Documented as opt-in for arm64 clusters.
- Python SDK agent surface — `sandbox.agent.configure(...)`, `invoke(...)`, `invoke_stream(...)`, `invoke_cancel(...)` on both `Sandbox` and `AsyncSandbox`. Files accept dicts, `(path, local_path)`, or `(path, bytes)` tuples; binary auto-base64s. `invoke()` returns a typed `InvokeResult`; `invoke_stream()` yields `InvokeEvent` objects for live rendering.
- CLI agent commands — `agenttier configure <sandbox>` uploads files, runs the install command, and registers the entrypoint with live install logs. `agenttier invoke <sandbox>` runs the configured entrypoint, streams output to stdout, and exits with the same exit code so it composes in shell pipelines. `--prompt`, `--input` (inline / `@file` / `-` for stdin), `--timeout`, `--cancel` flags supported.
- Web UI agent panel — running sandboxes show an "Agent" section inside the Advanced expander with Configure (agent code editor + install command + entrypoint), Invoke (prompt textarea + streaming log viewer + Cancel button), and per-session Recent invokes. Native fetch + ReadableStream consumer, no third-party SSE library. Templates with `mode: agent` show a 🤖 prefix in the Create Sandbox dialog.
- Documentation — new `docs/docs/agent-mode.md` (concept page with curl + SDK + CLI quickstarts), `docs/docs/agent-memory.md` (three memory patterns), and `examples/langgraph-agent/` (runnable LangGraph example with README). `templates.md` gained Agent mode + Credentials sections; `governance.md` documents the three new policy fields; `sdk.md` and `cli.md` got full agent-mode sections.

### Fixed

- Controller now reacts to the `agenttier.io/action: stop` annotation that the Router writes when `POST /sandboxes/{id}/stop` is called. Pre-existing bug from v0.2.0+ where `sandbox.stop()` was a quiet no-op; the annotation has always been written but the reconciler didn't watch for it. Now `stop` deletes the pod and transitions to `Stopped` within one reconcile loop.
- `/api/v1/audit/events` was returning empty for everyone because the Router ServiceAccount could `create` events but couldn't `list` them. RBAC fixed in `helm/agenttier/templates/rbac.yaml`. Pre-existing bug; surfaced when Phase 10's agent endpoints started writing audit events.
- SDK `raise_for_status` reads the body of streaming HTTP responses before decoding. Without the fix, `/configure` and `/invoke` errors raised an opaque `ResponseNotRead` instead of the structured `APIError`.
- `Bridge.ExecCommandStreamWithStdin` pipes a real `io.Reader` into the SPDY exec channel for `/invoke`. The previous shell-pipe trick (`printf | base64 -d | <cmd>`) hit `ARG_MAX` (~128 KB) on bigger payloads. 1 MB JSON bodies now flow cleanly.
- Web UI Cancel button populates "Recent invokes" on AbortError. The local `AbortController` fires before the server's exit event arrives, so the canceled run was previously invisible to the recent-invokes UI.
- `Dockerfile.controller` and `Dockerfile.router` accept a `GOPROXY` build-arg so builds work on networks where `proxy.golang.org` is blocked (corporate VPNs, captive networks).

## [0.2.2] — 2026-05-13

### Added

- Python SDK file transfer wrappers (`Sandbox.files` / `AsyncSandbox.files`) exposing `list`, `read`, `write`, `upload`, `download` with a 32 MiB `MAX_BYTES` cap that mirrors the Router. Typed `FileEntry` model lives in `agenttier.models`. 48 unit tests, mypy strict clean (QL.3).
- Web UI `FilesPanel` on every running sandbox card: directory listing with click-to-download links and an **Upload file** button. Paired with the existing port-forwards panel inside a single collapsed "Advanced — ports & files" expander so cards stay compact by default.
- Optional `gp3-immediate` StorageClass template (`optional.storageClass.enabled`). `WaitForFirstConsumer` saves cross-AZ attach cost but adds 5–10s to cold starts; an Immediate-binding class provisions PVCs up front and shaves most of that off for the warm pool and any template that targets it (#9.3).

### Changed

- Controller requeues the Creating state every 1s instead of 2s (after Pod create) / 3s (waiting for Pod Ready). The controller-runtime Pod watch is still the primary trigger; the shorter requeue is a backstop that trims up to 2s off a cold start (#9.4).
- README opening tagline: "Enterprise-grade Kubernetes-native sandboxes — for humans and AI agents." The older "operator for isolated, persistent sandboxes" framing moves into the What is AgentTier? bullets.
- Dashboard card grid now uses `align-items: start` so an expanded card no longer stretches its row-mate's border.
- GitHub Actions bumped to current majors: `docker/login-action@4`, `docker/setup-buildx-action@4`, `actions/setup-go@6`, `actions/setup-node@6`.

### Fixed

- Helm chart image helper now pulls `<repo>:v<appVersion>` by default instead of `<repo>:<appVersion>`. Previously, `helm install agenttier agenttier/agenttier` with no overrides would `ImagePullBackOff` because the release workflow tags images with a `v` prefix but the chart rendered the bare semver. Users who set `<component>.image.tag` explicitly are unaffected.

### Security

- Terminal size-queue resize message (`msg.Cols`, `msg.Rows`) is now clamped to uint16 bounds in `pkg/router/terminal/session.go` before conversion, fixing gosec G115 warnings and protecting against pathological values from a hostile client.
- File-download `Content-Disposition` filename is restricted to `[A-Za-z0-9._-]` so user-supplied path segments cannot inject response-header control characters, fixing gosec G705 taint analysis.
- Closed the gosec G104 "unhandled error" warnings in the router WebSocket cleanup path by explicitly discarding with a comment (`_ = conn.Close()`), confirming the close is best-effort.
- Annotated the false-positive gosec G101 on `GOOGLE_APPLICATION_CREDENTIALS` in `pkg/credentials/provider.go`; the literal is a GKE Workload Identity token-file path, not a credential value.
- Enabled GitHub Dependabot automated security fixes on the repo so future patch-available CVEs open PRs automatically.

## [0.2.0] — 2026-05-12

### Added

- Server-side WebSocket keepalive: the Router now sends RFC 6455 control pings and application-level heartbeat messages every 30 seconds on every terminal session. Browser WebSocket connections survive the default 60-second AWS load-balancer idle timeout without disconnects (#9.8).
- Client-side heartbeat watchdog: the Web UI tracks the server's 30-second heartbeat, surfaces a "stale" connection banner, and force-reconnects after 90 seconds of silence so a wedged Router pod is noticed immediately (#9.10).
- Optional Ingress template for the Web UI (`helm/agenttier/templates/webui-ingress.yaml`) with AWS Load Balancer Controller defaults — `idle_timeout.timeout_seconds=4000`, `lb_cookie` stickiness, and `inbound-cidrs` support for IP allowlisting. Compatible with `ingress-nginx` and Traefik by overriding `optional.ingress.className` (#9.9).
- File transfer REST API (`GET /api/v1/sandboxes/{id}/files/` to list, `GET/PUT .../files/{path}` to read and write). Drives sandbox-side `ls`/`stat`/`base64` through the existing SPDY exec bridge, enforces a 32 MiB per-request cap, and rejects shell-metachar path traversal (#7.4).
- Optional image pre-pull DaemonSet (`helm/agenttier/templates/image-prepull-daemonset.yaml`). Gated on `optional.imagePrepull.enabled`; pre-pulls the configured sandbox image, the Claude Code image, and anything in `optional.imagePrepull.extraImages` on every node, cutting the cold-start image-pull leg from 15–30s to near zero (#9.2, #7.11).
- Web UI: Settings page now polls warm pool status every 5s so ready/pending counts update live without a page refresh (QL.1).
- Web UI: Create Sandbox dialog pre-selects `claude-code-bedrock` as the default template when available, with fallback to the first installed template (QL.2).

### Changed

- Router Deployment now runs as the `agenttier-controller` ServiceAccount. Previously the router ran as the `default` SA and could not create sandbox CRs in a clean install.
- Helm chart `fullname` no longer stutters when release name equals chart name; resources render as `agenttier-controller`, `agenttier-router`, `agenttier-webui` instead of `agenttier-agenttier-…`.
- `docs/docs/installation.md` — ALB Ingress section now pins the AWS Load Balancer Controller IAM policy to the upstream `main` snapshot (adds `elasticloadbalancing:DescribeListenerAttributes`, which older frozen policies lacked) and documents the zombie-CRD cleanup for pre-rename installs.
- Warm pool ConfigMap moved to the install namespace as `agenttier-warmpool-config` (previously hardcoded to the legacy `agentloft-warmpool-config`).

### Fixed

- `helm/agenttier/templates/NOTES.txt` no longer crashes when ingress is enabled without TLS (`index ... 0` on an empty slice).

## [0.1.1] — 2026-05-12

### SDK (0.1.1)

- Rewrote the Python SDK to match the Router's camelCase JSON schema. The 0.1.0 SDK (not published) called `list_sandboxes()` and crashed with a Pydantic `ValidationError`.
- Removed the `FilesAPI`, `CommandsAPI`, and `clone()` surfaces because the corresponding server endpoints return 501. The surface now covers only endpoints the Router actually implements: create/list/get/stop/resume/terminate/exec/status/wait_until_running plus port forwarding, template listing, and `current_user()`.
- Typed exception hierarchy: every error inherits from `AgentTierError`; 401 → `AuthenticationError`, 403 → `AuthorizationError` (or `PolicyViolationError` when the Router returns the structured `policy_violation` body with `.violations`), 404 → `NotFoundError`, 409 → `ConflictError`, everything else → `APIError(status_code, body)`.
- Added `py.typed` marker and strict mypy support so downstream consumers get type checking.
- Dropped unused `websockets` dependency; added `httpx` and `pydantic` upper bounds.
- Added `User-Agent: agenttier-python-sdk/<version>` header, argument validation on all public methods, and 41 unit + integration tests against a mocked Router.

### Platform

- Cosign keyless signatures + SPDX & CycloneDX SBOMs attached to every released image (see `docs/docs/verifying-images.md` for `cosign verify` + `verify-attestation` flows).
- Docs site deploys to GitHub Pages on every release with Helm charts served from `/charts/` subpath; legacy root URL still works for existing users.
- Release notes auto-grouped into Breaking / Features / Fixes / Security / Docs / Dependencies via `.github/release.yml`; Release body prepends install snippets for Helm, images, CLI, and PyPI.
- Security scans run gosec + govulncheck + gitleaks + Trivy fs + Trivy image scans with SARIF upload to the repo Security tab (currently advisory pending the coordinated Go toolchain upgrade).
- Added `RELEASING.md` with the canonical pre-release checklist.

## [0.1.0] — 2026-05-11

First public release.

### Added

**Core platform**
- Kubernetes-native `Sandbox`, `SandboxTemplate`, and `ClusterSandboxTemplate` CRDs under `agenttier.io/v1alpha1`.
- Controller with state machine (Creating/Running/Stopped/Error/Deleting), finalizer-based cleanup, idle-timeout and max-runtime enforcement, leader election, and Prometheus metrics.
- Template resolution with inheritance (max depth 10), field-level merge, additive env-var merge, and `resourceVersion`-stamped audit trail in sandbox status.
- Warm pod pool with leader-elected reconciler, `gp3-immediate` StorageClass, per-template claim + auto-replenish, configurable from the Settings page. Measured 791 ms sandbox startup vs ~10 s cold start.
- Structured JSON logging with per-sandbox `startupDurationMs`.

**Router, terminal, and API**
- REST API at `/api/v1/*` for sandboxes, templates, governance, port forwarding, warm pool, audit events, analytics, cost estimation, and user identity (`/user/me`).
- WebSocket terminal at `/ws/terminal/{sandboxId}` bridging JSON messages to SPDY exec with full PTY semantics (resize, raw-mode input, ANSI passthrough) and 30 s reconnection window.
- Per-session credential injection (STS, secrets) plumbed through at session start.
- Non-interactive command execution via `POST /api/v1/sandboxes/{id}/exec`.
- OIDC JWT and API-key authentication middleware with a dev-mode bypass (auto-admin when `--oidc-issuer` is empty).

**Governance (phase 7.1)**
- `pkg/governance` engine with ConfigMap-backed policy store, cluster + per-namespace resolution with field-level merge, and enforcement at sandbox creation returning structured `policy_violation` errors.
- Admin-gated `GET/PUT/DELETE` REST endpoints for cluster and namespace policies, plus `/governance/effective` for previewing the resolved policy.
- Settings-page `GovernanceEditor` React component. Read-only for non-admin, editable for admin. Dev mode is auto-admin.

**Port forwarding (phase 7.3)**
- `pkg/router/portforward` creates Kubernetes Services per forwarded port, plus Ingresses when `previewDomain` is configured (no Gateway API CRD required).
- Authenticated in-Router reverse proxy at `/api/v1/sandboxes/{id}/preview/{port}/...` lets users hit a forwarded port from the browser even without public DNS.
- Sandbox status mirrors forwarded ports so `kubectl get sandbox -o yaml` and the Web UI stay in sync.
- Web UI: port-forwarding panel on every running sandbox card with inline add/remove and preview links.

**Web UI**
- React 19 + TypeScript + Vite SPA with pages for Dashboard, Templates (inline YAML editor), Terminal (xterm.js), Activity Log, Metrics, Cost Estimator, and Settings.
- OIDC PKCE login flow via `oidc-client-ts`, protected route wrapper, silent refresh, in-memory token storage.
- Multi-stage Dockerfile (node build → nginx serve) with reverse-proxy config for REST and WebSocket.

**Python SDK (phase 5)**
- `agenttier` package on PyPI with sync (`AgentTierClient`) and async (`AsyncAgentTierClient`) clients.
- Authentication auto-detection from `AGENTTIER_API_KEY` / `AGENTTIER_TOKEN` / kubeconfig, plus explicit `APIKeyAuth`, `BearerTokenAuth`, and kubeconfig providers.
- Typed Pydantic models, streaming file transfers, and `CommandsAPI` / `FilesAPI` exposed off the `Sandbox` handle.

**Helm chart and templates**
- Single `helm install agenttier agenttier/agenttier` deploys the controller, router, web UI, CRDs, and RBAC.
- Reference `ClusterSandboxTemplate`s for `general-coding` and `claude-code-bedrock`.
- Optional components: gVisor RuntimeClass, Prometheus ServiceMonitor, PodDisruptionBudget, image pre-pull DaemonSet, OTel Collector sidecar, Ingress for Web UI.

**Sandbox images (published to ghcr.io/agenttier)**
- `controller`, `router`, `web-ui` — platform services.
- `sandbox-general` — Ubuntu 22.04 + Node.js 20 + Python 3.11 + Go 1.22 + developer tooling.
- `sandbox-claude-code` — Node.js 20 + Claude Code CLI 2.1.81 + AWS CLI v2, wired for Bedrock via IRSA.
- `sandbox-minimal` — Alpine 3.20 + bash + git + curl.
- All images published for `linux/amd64` and `linux/arm64`.

**Operations and CI**
- Multi-arch Docker Buildx builds (amd64 + arm64) for every image on every `v*` tag.
- Helm chart published to `gh-pages` at `https://agenttier.github.io/agenttier` on every release.
- CLI binaries built for linux/darwin/windows on amd64 + arm64 with SHA-256 checksums attached to the GitHub Release.
- Python SDK auto-published to PyPI when `PYPI_TOKEN` is configured; wheel + sdist otherwise attached as artifacts.
- Security CI job running gosec, govulncheck, Trivy filesystem + container image scans, and gitleaks secret scanning with SARIF upload to the repo Security tab.
- License-header gate script in `hack/check-license-headers.sh` keeps every first-party Go file carrying the Apache 2.0 boilerplate.
- Dependabot groups for `k8s.io/*` and `go.opentelemetry.io/*`, with major-version ignores for web-ui tooling and the Go toolchain pending coordinated upgrades.

### Known limitations

- MongoDB-backed audit and governance persistence has been retired in favor of Kubernetes Events + ConfigMaps. Long-term retention requires the optional SQL backend (phase 7.13, not yet implemented).
- Sharing and collaboration (phase 7.2), file transfer API (7.4), notifications (7.5), and sandbox cloning (7.6) are stubbed but not yet functional.
- Image signing + SBOM (phase 8.2), release-notes template (8.7), and docs-site auto-deploy (8.8) still pending.
- WebSocket ping frames (9.8), ALB migration (9.9), and application-level heartbeat (9.10) pending — sessions through AWS Classic ELBs may still need manual reconnection every 60 minutes.
