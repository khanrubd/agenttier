# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
