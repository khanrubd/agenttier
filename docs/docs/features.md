# Features

What AgentTier ships today, grouped by what you probably need first.

## Declarative sandboxes

- **Kubernetes CRDs** — `Sandbox` (namespace-scoped), `SandboxTemplate` (namespace-scoped), `ClusterSandboxTemplate` (cluster-scoped). Manage sandboxes with `kubectl`, GitOps (Argo CD / Flux), or through the REST API, SDK, or Web UI.
- **State machine** — Creating → Running → Stopped → Running → Deleting, with an Error sink and Kubernetes Events at every transition so `kubectl describe sandbox` tells the full story.
- **Stop and resume** — Stop deletes the Pod while preserving the PVC. Resume re-attaches the same volume in about two seconds. Workspace contents, installed packages, and git state are exactly as left.
- **Idle and max-runtime timeouts** — per-sandbox via `spec.idleTimeout` / `spec.timeout`, or per-namespace via governance caps. A configurable grace window notifies connected terminal sessions before auto-stop.
- **Self-healing** — restart on transient pod failures (OOM, preemption) with 10s / 20s / 40s / 80s / 160s exponential backoff. Permanent failure modes (image pull forever, config error) are surfaced on the sandbox `status.conditions`.

## Warm pod pool

- **Sub-second startup** — a leader-elected controller keeps N pre-provisioned Pods hot. When a user creates a sandbox, AgentTier claims one from the pool (measured 791 ms vs ~10 s cold).
- **Immediate PVC binding** — the warm pool uses a `gp3-immediate` StorageClass so the EBS volume is provisioned up-front; pod scheduling no longer waits on `WaitForFirstConsumer`.
- **Runtime reconfiguration** — change pool size or template through the Settings page; the controller picks it up from the `agenttier-warmpool-config` ConfigMap without a redeploy.

## Templates and agent harnesses

- **Field-level merge with inheritance** — `spec.inheritsFrom` chains templates up to depth 10. Sandbox spec overrides template spec overrides parent template overrides cluster defaults, one field at a time.
- **Harness config** — tell AgentTier which shell, tools, system prompt, and hooks to run. Hooks fire on start / idle / stop / resume.
- **Init scripts** — run cluster-approved setup commands before the container becomes Running (install extra tooling, clone a repo, wait for a service).
- **Embedded files** — templates can seed files into the workspace (e.g. a default `.tmux.conf`, a README, a code-of-conduct).
- **Reference images** — `general-coding` (Ubuntu + Node + Python + Go), `claude-code-bedrock` (Claude Code CLI wired to AWS Bedrock via IRSA), `minimal-shell` (Alpine + bash + git + curl). All published on `ghcr.io/agenttier/sandbox-*`.

## Security and isolation

- **NetworkPolicy by default** — deny-all egress, allow DNS. Opt-in egress rules per template (e.g. "allow github.com and pypi.org"). Inter-sandbox peering is opt-in via label selectors.
- **Hardened pod defaults** — non-root, read-only root filesystem, drop all capabilities, `seccomp=RuntimeDefault`, per-sandbox ServiceAccounts with zero cluster permissions.
- **Kernel isolation** — optional gVisor RuntimeClass for untrusted workloads.
- **Per-session credentials** — STS AssumeRole or Kubernetes Secrets projected into the exec session at terminal open time (not baked into the image).
- **IRSA / Workload Identity** — zero long-lived cloud keys. IAM roles attach to the sandbox's ServiceAccount on EKS, Workload Identity does the same on GKE.
- **Signed container images** — every released image is cosign-signed with keyless OIDC (GitHub Actions identity). SPDX + CycloneDX SBOMs attached as OCI attestations. See [Verifying images](verifying-images.md).

## Interactive access

- **Browser terminal** — full PTY over WebSocket with xterm.js. Resize, ANSI colors, paste, copy, and a 30-second reconnection window for network blips.
- **Non-interactive exec** — `POST /api/v1/sandboxes/{id}/exec` returns `stdout` / `stderr` / `exitCode`. Matches how the SDK's `sandbox.exec()` is wired.
- **Port forwarding** — expose any container port with one click (Web UI) or one API call. AgentTier creates a ClusterIP Service, adds an Ingress when a preview domain is configured, and also offers an authenticated in-Router reverse proxy so users can reach ports even without DNS. See [Port forwarding](port-forwarding.md).

## Multi-tenancy and governance

- **OIDC + API keys** — Cognito, Okta, Azure AD, Auth0, Google — anything with a JWKS endpoint works. API keys are stored as SHA-256 hashes with an LRU cache. Dev mode (no OIDC configured) grants anonymous admin for local development.
- **Governance policies** — cluster-wide default + per-namespace overrides with field-level merge. Enforced synchronously at sandbox creation; violations return a structured `policy_violation` body with stable machine codes so UIs pinpoint the failing field. See [Governance](governance.md) for the full rule list.
- **Admin-gated editor** — `Settings → Governance` in the Web UI renders the active policies; only users with the admin claim can edit.
- **Audit trail** — lifecycle, terminal, credential, share, clone, and port-forward events recorded as Kubernetes Events. The Activity Log page filters on action, user, and time range. An optional SQL backend (phase 7.13) is planned for long-term retention.

## Web UI

- **Dashboard** — sandbox cards with status, template, age, one-click Stop / Resume / Delete / Open Terminal. Running cards also show an inline Port Forwards panel.
- **Templates editor** — in-browser YAML editor with syntax highlighting, create / save / delete, field validation.
- **Activity Log** — time-ordered events with filters.
- **Metrics** — live sandbox counts, average startup time, reconciliation queue depth.
- **Cost Estimator** — current monthly cost based on running resources.
- **Settings** — governance policies, warm pool sizing and template, operational defaults. Admin-gated.

## Client tooling

- **Python SDK** — `pip install agenttier`. Sync + async clients, typed Pydantic models, auto-detected auth, structured exception hierarchy. See [SDK](sdk.md).
- **CLI** — `agenttier` Go binary for linux / macOS / Windows on amd64 + arm64. See [CLI](cli.md).
- **REST API** — sandboxes, templates, governance, port forwarding, audit, analytics, warm pool, identity. Documented inline in [`pkg/router/server.go`](https://github.com/agenttier/agenttier/blob/main/pkg/router/server.go) and exercised by the SDK.

## Observability

- **OpenTelemetry** — distributed traces across controller + router with trace context in structured JSON logs. OTLP exporter wires to any collector; the Helm chart can optionally deploy one as a sidecar.
- **Prometheus** — `/metrics` exposes sandbox counts by status/template, startup-duration histograms, reconciliation queue depth, error counters, terminal session stats. Optional `ServiceMonitor` for Prometheus Operator.
- **Kubernetes Events** — every lifecycle transition emits a typed Event on the Sandbox resource so `kubectl describe sandbox` is a first-class debugging surface.
- **Startup logging** — `startupDurationMs` is logged per creation and recorded on an Event for regression tracking.

## Deployment and operations

- **Single Helm chart** — one `helm install agenttier agenttier/agenttier` deploys controller, router, web UI, CRDs, RBAC, and all opt-ins.
- **Multi-cluster** — works on EKS, GKE, AKS, kind, and any self-managed Kubernetes 1.27+ with NetworkPolicy-capable CNI.
- **Leader-elected HA** — multi-replica controller with Lease-based election. Graceful degradation for non-critical dependency failures (e.g. can't reach OTel collector).
- **Cluster autoscaling out of the box** — opt-in upstream Cluster Autoscaler installs cloud-neutral via Helm (works on EKS, GKE, AKS, OpenStack, Cluster API). Pair with the `headroom` Deployment for N+1 spare-node capacity: pause Pods at negative priority squat on a spare node, sandboxes preempt them instantly, evicted Pods trigger a fresh node in the background. See [Scaling](scaling.md) for sizing math + cost trade-offs.
- **Kubernetes-native state** — defaults to Kubernetes etcd + Events + ConfigMaps for all state. An optional SQL backend (Postgres / MySQL / SQLite) is on the roadmap for compliance-driven long-term retention.
- **Terraform** — EKS / GKE / AKS modules under [`terraform/`](https://github.com/agenttier/agenttier/tree/main/terraform) for fully-provisioned reference deployments.

## What is not here yet

Roadmap items that are *not* shipped in v0.3.5 and will return real errors or missing features if you rely on them:

- Sharing and collaboration (viewer/collaborator roles, expiring share links) — planned for 0.2.x.
- File transfer API — planned for 0.2.x.
- Sandbox cloning via `VolumeSnapshot` — planned for 0.2.x.
- Notifications (webhook / email / Slack) — planned for 0.2.x.
- WebSocket ping frames + ALB migration — planned for 0.2.x; sessions through AWS Classic ELBs may still need manual reconnection every 60 minutes without the `connection-idle-timeout` annotation tweak.
- Optional SQL backend for audit + analytics long-term retention — planned for 0.3.x.

Track progress in the [GitHub issues](https://github.com/agenttier/agenttier/issues) or the `todo.md` file in the repo if you are contributing.
