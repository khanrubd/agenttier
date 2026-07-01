# Architecture

How AgentTier fits together, what each piece does, and how data moves
between them. This is the right page to read before contributing or before
deciding whether AgentTier is the right fit for your platform.

## Components

```
┌───────────────────────────────────────────────────────────────────────┐
│                         Kubernetes Cluster                            │
│                                                                       │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                │
│  │  Controller  │  │    Router    │  │    Web UI    │                │
│  │  (operator)  │  │ (REST + WS + │  │  (React +    │                │
│  │              │  │    proxy)    │  │    nginx)    │                │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘                │
│         │ reconciles      │ exec + watch    │ /api, /ws proxied      │
│         ▼                 ▼                 │                        │
│  ┌──────────────────────────────────────┐   │                        │
│  │         Sandbox namespace(s)          │   │                        │
│  │  ┌──────────┐  ┌──────────┐  ┌─────┐ │   │                        │
│  │  │Sandbox 1 │  │Sandbox 2 │  │ ... │ │   │                        │
│  │  │Pod + PVC │  │Pod + PVC │  │     │ │   │                        │
│  │  │+ NetPol  │  │+ NetPol  │  │     │ │   │                        │
│  │  └──────────┘  └──────────┘  └─────┘ │   │                        │
│  └──────────────────────────────────────┘   │                        │
└─────────────────────────────────────────────── │ ──────────────────────┘
                                                 │
                              browser / CLI / SDK────┘
```

### Controller

A Go binary built with [kubebuilder v4](https://book.kubebuilder.io/) that
reconciles three CRDs under `agenttier.io/v1alpha1`:

- `Sandbox` — one per sandbox
- `SandboxTemplate` — namespace-scoped blueprint
- `ClusterSandboxTemplate` — cluster-scoped blueprint

It owns Pods, PVCs, NetworkPolicies, and per-sandbox ServiceAccounts. A
separate reconciler manages the warm pod pool and watches the
`agenttier-warmpool-config` ConfigMap for live reconfiguration.

State machine: Creating → Running → Stopped → Running → Deleting, with Error
as a sink. Every transition emits a Kubernetes Event on the Sandbox resource.

### Router

A Go HTTP server serving:

- REST API at `/api/v1/*` (sandboxes, templates, governance, port forwarding, audit, analytics, warm pool, identity)
- WebSocket terminal at `/ws/terminal/{id}` bridging browser WebSocket to either an in-pod HTTP-PTY (when the sandbox has `useHTTPExec: true`) or SPDY pod exec (legacy fallback)
- Authenticated in-cluster reverse proxy at `/api/v1/sandboxes/{id}/preview/{port}/...` for forwarded ports
- Prometheus metrics at `/metrics`, liveness at `/healthz`, readiness at `/readyz`

Auth is OIDC JWT + API key (SHA-256 hashed). Governance runs inline at create
time. Terminal session state (for reconnection) is kept in memory; the
`/api/v1/user/me` endpoint exposes the caller's identity for UI use.

### Web UI

React 19 + TypeScript + Vite SPA served by nginx. `/api/*` and `/ws/*` are
reverse-proxied to the Router Service. No component library — plain CSS.
TanStack Query handles server state caching.

### Sandbox Pods

Each sandbox is a Pod with one container (the sandbox), optional sidecars
and init containers from the template, a per-sandbox PVC mounted at
`/workspace`, and a NetworkPolicy scoped to that sandbox's label.

Hardened defaults: non-root, read-only root filesystem, drop all
capabilities, `seccomp=RuntimeDefault`, per-sandbox ServiceAccount with no
cluster permissions.

## Data flow: sandbox creation

1. Client (Web UI / SDK / CLI / `kubectl`) calls `POST /api/v1/sandboxes` with a `templateRef` and `name`.
2. **Router** authenticates the request, extracts the caller's identity, and runs governance enforcement (policy resolution + limits check).
3. If allowed, the Router creates a `Sandbox` CR in the target namespace. CR's `spec.createdBy` is stamped with the authenticated identity.
4. **Controller** observes the new CR. Resolves the template chain (inheritance, field-level merge, env merge), records `status.resolvedTemplate` + `status.templateResourceVersion` for auditability.
5. Controller tries to claim a warm pod for the target template. If one is available, it relabels that pod, attaches the sandbox CR as owner, and jumps the Sandbox to `Running`.
6. If no warm pod, Controller creates the PVC (via CSI, `WaitForFirstConsumer` or `Immediate` depending on the StorageClass), then the Pod, then the NetworkPolicy. Sandbox stays in `Creating`.
7. When the Pod becomes Ready, Controller sets `status.phase=Running`, records `startupDurationMs`, and emits a `Running` Event.
8. Client polls `GET /api/v1/sandboxes/{id}` or watches the CR and sees the phase transition.

## Data flow: terminal session

1. Client opens a WebSocket to `/ws/terminal/{id}`.
2. Router authenticates via JWT / API key (or grants anonymous admin in dev mode).
3. Router looks up the Sandbox, checks it is `Running`, and finds its Pod name.
4. Per-session credential injection — if the template specifies credentials, Router fetches (STS AssumeRole, Kubernetes Secret, …) and injects them into the exec environment.
5. Router picks the terminal transport based on `harness.useHTTPExec`:
    - **HTTP-PTY** (preferred when `useHTTPExec: true` and the in-pod runtime is healthy) — Router dials `ws://<pod-ip>:9000/pty?session=agenttier-<sandbox>` directly, bridging WebSocket frames TCP-to-TCP. The runtime spawns the shell with a tmux wrap (`tmux new-session -A -s <session>`) so reconnects re-attach the same shell with running processes intact (gdownload, builds, long apt installs). The kube-apiserver is out of the request path.
    - **SPDY exec** (legacy, used for non-opted-in sandboxes or when the runtime is unreachable) — Router calls `POST /api/v1/namespaces/{ns}/pods/{pod}/exec` with `tty=true`, `stdin=true`, bridging the SPDY stream to the WebSocket. Resize events flow through the SPDY `TerminalSizeQueue`. The shell is wrapped in tmux the same way (via `bridge.go`'s `buildShellCommand`) so resume-on-reconnect works there too. Shell survives the drop, but the WebSocket itself drops every 20-60 minutes because the EKS apiserver recycles long-lived streaming connections.
6. The session is tracked in Router memory with a 30-second reconnection window. If the WebSocket drops, the exec stream (or HTTP-PTY connection) stays alive for 30 seconds so the client can reconnect without losing shell state.

To verify which transport a session used, check Router logs for `terminal session via HTTP-PTY` (success) or `HTTP-PTY fallback to SPDY` with a structured `reason` field (fallback).

## Data flow: port forwarding

1. Client calls `POST /api/v1/sandboxes/{id}/ports {port: 8080}`.
2. Router creates a ClusterIP Service selecting the sandbox Pod on that port.
3. If `networking.previewDomain` is configured, Router also creates an Ingress with the configured IngressClass, routing `sandbox-{name}-{port}.{domain}` to the Service.
4. Router mirrors the forwarded port into the Sandbox's `status.forwardedPorts`.
5. Client may access the port via:
   - Public Ingress URL if configured, or
   - Router-proxied preview at `/api/v1/sandboxes/{id}/preview/{port}/...`, which authenticates and reverse-proxies into the Service (works without DNS, great for dev / kind).

## Governance enforcement

At create time, Router resolves an effective policy by merging cluster default with per-namespace override (field-by-field, non-zero override wins). Then it evaluates each rule:

- User / namespace quotas
- CPU / memory / storage caps (sandbox overrides only; template defaults are trusted)
- Timeout caps (`0` means "infinite" which exceeds any finite cap)
- Allowed templates list
- Approved image registries list

Violations are collected and returned as a 403 with a structured
`{error: "policy_violation", violations: [{code, message}, …]}` body. See
[Governance](governance.md) for the full rule set.

## State storage

- **Kubernetes etcd** — CRDs (Sandboxes, Templates) and their statuses.
- **Kubernetes ConfigMaps** — warm pool config (`agenttier-warmpool-config`), governance policies (`agenttier-governance`).
- **Kubernetes Events** — audit trail (lifecycle, terminal, credential, share, clone, port-forward).
- **In-memory** (Router) — active terminal sessions with reconnection TTL.

An opt-in SQL backend (Postgres / MySQL / SQLite) for long-term audit and
analytics retention is planned for 0.3.x. Until then, the retention you get
is the Kubernetes Event TTL (typically ~1 hour).

## Security model

- **Identity** — OIDC (multi-user, multi-group) + optional API keys. Dev mode with no OIDC grants anonymous admin for local use.
- **Authorization** — non-admin users see only sandboxes they own (or are shared with). Admins see everything. Governance-sensitive endpoints (cluster policy edit, namespace policy edit/delete) are admin-gated.
- **Pod isolation** — per-sandbox ServiceAccount with zero cluster permissions, non-root user, read-only root filesystem, drop all capabilities, `seccomp=RuntimeDefault`, optional gVisor RuntimeClass.
- **Network isolation** — NetworkPolicy deny-all egress by default; DNS always allowed; opt-in egress rules per template.
- **Credentials** — not baked into images; injected per session at exec open time.
- **Supply chain** — every released image is cosign-signed and carries SPDX + CycloneDX SBOMs.
- **Exec transport trust model** — HTTP-exec mode uses plain HTTP in-cluster, secured by NetworkPolicy. See [Security](security.md#in-cluster-exec-transport) for details and trade-offs.
- **CRD management** — the controller manages its own CRDs on startup. See [Security](security.md#crd-source-of-truth) for the GitOps opt-out path.

For the full security reference including trust boundaries, accepted risks, and hardening guidance, see [Security](security.md).

## Why these choices

A few deliberate trade-offs worth knowing:

- **Single monorepo** for controller, router, web UI, SDK, CLI, and Helm chart. Easier to keep API versions in sync; one release tag ships everything.
- **Kubernetes-native state** by default. MongoDB was removed; we use etcd + ConfigMaps + Events for everything so the platform has zero hard external deps. The optional SQL backend is purely for long-term retention, never for hot path.
- **Stable networking.k8s.io/v1 Ingress** for port forwarding rather than Gateway API. Ingress is universal in K8s 1.27+; Gateway API requires separately-installed CRDs. Operators who prefer Gateway API can switch `ingressClassName` via Helm values.
- **Flat eslint v9 config** and **golangci-lint v1** to keep the dev loop fast; major jumps are held for coordinated upgrades when the ecosystem stabilizes.
