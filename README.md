<p align="center">
  <h1 align="center">AgentTier</h1>
  <p align="center">
    <strong>Enterprise-grade Kubernetes-native sandboxes — for humans and AI agents.</strong>
  </p>
  <p align="center">
    <a href="https://github.com/agenttier/agenttier/actions"><img src="https://github.com/agenttier/agenttier/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
    <a href="https://github.com/agenttier/agenttier/releases"><img src="https://img.shields.io/github/v/release/agenttier/agenttier" alt="Release"></a>
    <a href="https://pypi.org/project/agenttier/"><img src="https://img.shields.io/pypi/v/agenttier.svg" alt="PyPI"></a>
    <a href="https://goreportcard.com/report/github.com/agenttier/agenttier"><img src="https://goreportcard.com/badge/github.com/agenttier/agenttier" alt="Go Report Card"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License"></a>
  </p>
  <p align="center">
    <a href="https://agenttier.github.io/agenttier/"><strong>Documentation</strong></a> ·
    <a href="https://agenttier.github.io/agenttier/quickstart/">Quickstart</a> ·
    <a href="https://agenttier.github.io/agenttier/tutorials/">Tutorials</a> ·
    <a href="https://agenttier.github.io/agenttier/sdk/">SDK</a> ·
    <a href="https://github.com/agenttier/agenttier/releases/latest">Releases</a>
  </p>
</p>

---

## What is AgentTier?

AgentTier is a Kubernetes-native platform that provides isolated, persistent sandbox environments for running AI agents. Each sandbox is a pod with its own persistent storage, network isolation, and interactive terminal access — managed declaratively through Custom Resource Definitions.

**Key use cases:**
- **Kubernetes operator for isolated, persistent sandboxes** — declarative CRDs manage the full pod + PVC + NetworkPolicy lifecycle so stopped sandboxes keep their files and resumed sandboxes re-attach the same volume.
- Run AI coding agents (Claude Code, Cursor, Aider) in secure, isolated environments
- Provide on-demand development environments for engineering teams
- Execute untrusted AI-generated code with kernel-level isolation (gVisor)
- Orchestrate multi-agent workflows with inter-sandbox communication

---

## Screenshots

<p align="center">
  <img src="docs/assets/dashboard.png" alt="AgentTier dashboard showing six sandboxes (a mix of human developers and AI agents) with per-sandbox template, creator, and one-click lifecycle actions" width="100%" />
  <em>Dashboard with a mix of human developer sandboxes and Claude Code agent sandboxes.</em>
</p>

<p align="center">
  <img src="docs/assets/terminal-claude-code.png" alt="Browser-based terminal attached to a running sandbox, with a Claude Code session waiting for input" width="100%" />
  <em>Full PTY in the browser. This sandbox is running Claude Code against AWS Bedrock.</em>
</p>

## Features

### Sandbox lifecycle

- **Create, stop, resume, delete** — sandboxes spin up from a template; stopping preserves the workspace, packages, and git state on a persistent volume; resume reattaches the same volume in seconds; idle and max-runtime caps auto-stop with grace. Opt into `snapshotOnStop` to capture a CSI VolumeSnapshot of the workspace each time the sandbox stops, so a stopped state can be restored or cloned later.
- **Clone any sandbox via VolumeSnapshot** — `POST /api/v1/sandboxes/{id}/clone` takes a CSI VolumeSnapshot of the source PVC and provisions a new sandbox whose workspace is byte-identical to the source. Clones inherit the source's spec (template, env, ports, agent harness) so a fork is one HTTP call away. SDK + CLI surfaces; works on any CSI driver with snapshot support (EBS, GCE PD, Azure Disk, etc).
- **Scheduled PVC backups** — opt-in controller scheduler takes periodic CSI VolumeSnapshots of every managed workspace PVC on a configurable interval and prunes them past a retention window (disaster-recovery Layer 1). Restore is a normal create with `spec.cloneFromSnapshot`. Off-cluster Layer 2 (S3 / Velero) is documented.
- **Sub-second cold starts** — per-template warm pools, optional immediate PVC binding, and an opt-in image pre-pull DaemonSet take creation from ~10 s down to ~800 ms.
- **Self-healing** — bounded retries on infrastructure failures with structured Kubernetes events for every transition; clean Error state once the retry budget is exhausted.

### Templates and agent harnesses

- **Compose templates from other templates** with field-level merge and per-sandbox overrides; the harness block defines the shell, tools, system prompt, hooks, and init scripts.
- **Reference images out of the box** — general coding, Claude Code on AWS Bedrock (with cloud-native credential injection), OpenClaw on AWS Bedrock (turnkey IRSA-driven config), Strands Agents on AWS Bedrock (Python SDK with IRSA), a LangGraph agent-mode image, an `rl-rollout` image (PyTorch + Ray RLlib + Gymnasium + Stable-Baselines3 with self-contained PPO and `/invoke`-shaped rollout examples), and minimal shell.

### Agent mode

- **Run an agent on demand** — configure a sandbox once with your code and install command, then call `/invoke` to run it; output streams back as Server-Sent Events and closing the connection cancels the in-pod process.
- **Bring your own framework or harness** — the LangGraph reference template ships in the box; the same shape works for Strands Agents, AutoGen, OpenHands, OpenClaw, or any pip-installable agent library. The framework owns the loop; AgentTier owns lifecycle, auth, transport, audit, and governance.
- **Throttle, time out, and audit every invoke** — per-sandbox concurrency caps return a clean 429 with `Retry-After`; default 30-minute per-invoke timeout with a cluster ceiling; OpenTelemetry spans, Prometheus metrics, and Kubernetes events emitted on every configure and invoke.
- **Install logs persisted out-of-band** — the trailing bytes of every `/configure` install command land in a per-sandbox ConfigMap rather than inline on the Sandbox CR, so etcd object size stays small at scale and `kubectl describe sandbox` stays clean. A lazy GET endpoint serves the log on demand.
- **Optional local memory** — Helm flag adds a `mem0` sidecar next to the agent. Bring-your-own memory (PVC-local, Pinecone, Postgres + pgvector, AgentCore Memory) is fully supported and documented.

### Security and isolation

- **Locked-down sandboxes by default** — non-root user, read-only root filesystem with a writable in-memory `/tmp`, all capabilities dropped, `seccomp=RuntimeDefault`, and per-sandbox service accounts with no cluster permissions.
- **Network isolation** — default deny-all egress with a configurable allow-list and always-on DNS; optional inter-sandbox connectivity via `spec.network.allowPeerSandboxes` (with an optional peer selector) for multi-agent workflows; optional gVisor RuntimeClass for kernel-level isolation of untrusted workloads, with an opt-in `runsc` node-installer DaemonSet so operators can enable it without hand-provisioning every node.
- **Cloud-native credentials** — wire AWS Bedrock and other cloud APIs in via IRSA on EKS or workload identity on GKE; mount Kubernetes Secrets as env vars or files; no long-lived secrets baked into images. Assign a distinct cloud identity per sandbox or per template with the `serviceAccount` field (an EKS IRSA-annotated or GKE Workload Identity ServiceAccount) so different tenants get scoped, separate credentials instead of sharing one namespace-default identity.
- **Hashed share-link tokens** — share links store SHA-256 hashes at rest, never the raw secret, with constant-time comparison on validation.

### Interactive access

- **Browser terminal that survives drops** — full PTY over WebSocket with reconnect, plus an in-pod terminal endpoint (`/pty` on each sandbox) that bypasses the Kubernetes API server so long sessions survive load-balancer and apiserver-side timeouts; a tmux wrap keeps the same shell across reconnects, and tmux's alt-screen capability is stripped so fullscreen TUIs (Claude Code, vim, less) write into the browser scrollback instead of swallowing history.
- **Bottom-pinned during fast TUI redraws** — when an agent (Claude Code, vim, htop) is producing dense output the viewport stays pinned to the bottom so the input prompt stays visible. If you scroll up to read history, your scroll position is preserved — output keeps landing below without yanking you back to the prompt.
- **Run commands programmatically** — fire-and-forget or request-response exec, file upload/download/list, and port forwarding with authenticated previews through the Router; ports also surface as Ingress URLs when a preview domain is configured.

### Multi-tenancy and governance

- **Plug into any OIDC provider** — Cognito, Okta, Azure AD, or anything OIDC-compliant. JWTs are verified against the provider's JWKS (RS256 signature + issuer + audience + expiry), plus API keys minted on demand and stored as SHA-256 hashes with LRU caching. Auth fails closed: with no OIDC issuer configured the Router rejects every request with 401 unless an operator explicitly sets `auth.devAuth: true` for local development.
- **Hierarchical governance policies** — cluster and per-namespace caps on sandbox counts, CPU / memory / storage, idle and max-runtime timeouts, agent-mode concurrency, allowed templates, and approved image registries; violations return a structured response so UIs can pinpoint the failing field. Same policy is re-checked at agent `/configure` time so a policy that tightens after sandbox creation still gates code uploads.
- **Multi-namespace by design** — sandboxes can live in any namespace, not just `default`. The API resolves a sandbox cluster-wide (or by an explicit `?namespace=` when a name exists in more than one), so get / terminal / exec / stop / resume / clone / files / ports all work against tenants deployed into their own namespaces, and per-namespace governance caps apply to the namespace each sandbox actually runs in.
- **Admission webhook closes the kubectl-bypass** — an opt-in mutating admission webhook (requires cert-manager) enforces governance and stamps `spec.createdBy` from the authenticated user at admission time, so direct `kubectl apply` / GitOps writes can't forge ownership or skip the policy checks that otherwise run only in the Router. Fail-closed by default.
- **Per-IP and per-user rate limiting** — opt-in token-bucket throttling on Router endpoints, with health checks and WebSocket terminals exempt; 429 responses carry `Retry-After`.
- **Built-in audit trail** — every lifecycle, terminal, credential, share, clone, and port-forward event is recorded as a Kubernetes event (and optionally a row in a SQL backend for long-term retention).
- **Share sandboxes with users and groups** — grant, list, and revoke per-user / per-group access on a sandbox, or mint an expiring share link whose token is shown once and stored only as a SHA-256 hash. Shared users get read + terminal access; management stays owner/admin-only.
- **Lifecycle notifications** — opt-in delivery of sandbox Error and Stopped events to the owner over webhook, Slack, or SMTP email; each channel's secrets come from Kubernetes Secrets, never inline config.
- **Optional SQL backend for history** — persist audit events, sandbox lifecycle events, and cost snapshots beyond the ~1h Kubernetes-Event GC window. Bundled pure-Go SQLite (PVC-backed) or bring-your-own Postgres/MySQL. Off by default and fully best-effort — a backend hiccup never blocks an operation; Kubernetes stays the source of truth.

### Web UI

- **One-click sandbox management** — dashboard cards show name, status, mode (Code or Agent), and key metadata; primary actions (Open Terminal / Stop / Resume / Delete) sit on the card; a gear icon opens a per-sandbox settings page at `/sandbox/<id>/settings` for ports, files, agent invoke, and (in time) governance overrides, network rules, and other deeper controls.
- **Cluster glance** — the left nav shows live node + pod counts plus headroom spare and warm-pool size, with a green dot when Cluster Autoscaler is running. Refreshes every ten seconds without polling the dashboard.
- **Cluster capacity card** — the Metrics page shows per-node allocatable vs. requested CPU and memory, a requests-based saturation percentage, and the managed node group (EKS / Karpenter / GKE / AKS) when the provider labels it, with a per-node table behind an expander. Admin-only, polled every ten seconds.
- **Per-template warm pools, edited in place** — the Settings page lets operators add and remove warm-pool entries one template at a time, see ready / pending / target counts per pool, and tune the optional headroom Deployment's replica count and per-replica CPU and memory without `helm upgrade`.
- **Hierarchical workspace browser** — the Files panel on the per-sandbox settings page lets users click into folders, breadcrumb back, download a single file, download a single folder as `.zip`, or download the entire workspace as `.zip`. The archive endpoint streams `tar` from the pod and re-encodes to zip on the fly server-side, so it works on every sandbox image without extra binaries.
- **Browser-based admin** — YAML template editor, time-ordered activity log with filters, live metrics + monthly cost estimator, and a Settings page for governance policies, warm pools, and cluster autoscaling.

### Client tooling

- **`pip install agenttier`** — installs both the Python SDK (sync + async, typed models, auto-detected auth, streaming file transfers including workspace archive download, opt-in retry layer with backoff and `Retry-After`) and the same `agenttier` shell command on PATH.
- **Cross-platform CLI** — Go binary for sandbox and template management distributed for linux / darwin / windows on amd64 + arm64; the `pip` install gives you the same command tree without the Go dependency.
- **REST API** — documented endpoints for sandboxes, templates, governance, audit, sharing, port forwarding, files, archive (workspace as zip), configure, and invoke.
- **API versioning + deprecation signals** — a documented versioning policy; the Router stamps `Deprecation`/`Sunset` headers on any endpoint slated for removal, and the SDK and CLI surface a one-time deprecation warning on first hit so callers get a migration runway before anything breaks.

### Performance and observability

- **Tracking and pre-warming** — per-sandbox startup duration in logs and events for regression tracking; optional warm pool, immediate PVC binding, and image pre-pull eliminate cold-start latency.
- **OpenTelemetry traces and Prometheus metrics** — distributed traces across controller and router with **trace IDs auto-injected into structured JSON log lines** (one trace ID pivots between an OTel UI and `kubectl logs` in either direction). Spans cover every HTTP request (`router.<method>`), agent `/configure`, `/invoke`, and every controller reconcile (`controller.reconcile_sandbox`, tagged with the sandbox's namespace and phase) so a sandbox traces end-to-end across the controller, router, and pod. Bucketed `actor_hash` instead of raw OIDC subjects so traces shipped to third-party stores don't carry PII. `/metrics` exposes sandbox counts, startup histograms, queue depth, error counters, terminal stats, and the agent-mode invoke / configure / throttle metrics.
- **OTel Collector bundled with the chart** — opt-in `observability.otelCollector.enabled=true` flag renders a Deployment + ConfigMap + Service running `otel/opentelemetry-collector-contrib` in your install namespace; deployments auto-point at it. Default exporter is `debug` so you can tail the collector's container logs to verify spans without provisioning external infra; replace the exporter to ship to Honeycomb / Datadog / Tempo / Jaeger / your existing collector.
- **Continuous CVE scanning** — every sandbox base image is scanned with Trivy on every push; findings land in the GitHub Security tab as SARIF.
- **Performance regression guard** — CI fails the build if the Web UI bundle exceeds its 750 KB budget; `hack/perf-smoke.sh` and `hack/load-test.sh` capture p50/p99 cold + warm start timings and API throughput, with measured numbers published in the docs.

### Deployment and operations

- **Single Helm chart** — one `helm install` deploys controller, router, web UI, CRDs, RBAC, and optional add-ons (gVisor, ServiceMonitor, PDB, image pre-pull, OTel Collector, mem0 sidecar, rate limiting, cluster autoscaler, headroom).
- **CRDs upgrade automatically** — the controller applies its bundled CRDs on startup, so `helm upgrade` to a release with new CRD fields makes those fields usable immediately (Helm itself never upgrades CRDs). Fresh installs no longer need a manual `kubectl apply` either. Opt out with `controller.manageCRDs=false` when CRDs are managed out-of-band via GitOps.
- **Production load-balancer support** — opt-in Ingress template with AWS Load Balancer Controller defaults (4000 s idle timeout, sticky sessions, IP allow-list); compatible with ingress-nginx and Traefik via a single override.
- **Multi-cluster ready** — runs on EKS, GKE, AKS, and self-managed Kubernetes 1.27+ on any CNI with NetworkPolicy support.
- **Terraform EKS reference module** — `terraform/aws-eks/` stands up a complete cluster (VPC, EKS, managed node groups including a dedicated gVisor group, EBS CSI, AWS Load Balancer Controller, IRSA roles) and installs the AgentTier Helm release, so a from-scratch EKS deployment is `terraform apply`.
- **Highly available** — multi-replica controllers with leader election; multi-replica router with HTTP-routed exec, files, and invoke so any replica can serve any request.
- **Cluster autoscaling out of the box** — opt-in upstream Cluster Autoscaler installs cloud-neutral via Helm (works on EKS, GKE, AKS, OpenStack, Cluster API). Pair it with the `headroom` Deployment to keep N+1 spare-node capacity warm: pause Pods at negative priority squat on a spare node, real sandboxes preempt them instantly, the evicted Pods trigger CAS to add the next spare in the background. Sandboxes never wait on a cold ASG round-trip.
- **Container images you can verify** — every image is multi-arch, cosign-signed via GitHub Actions OIDC, and ships SPDX + CycloneDX SBOMs as OCI attestations.
- **Automated post-release retention** — every release run prunes container manifests, GitHub Releases, and **git tags** older than the latest 10 (plus `github-pages` deployments trimmed to 10 and stale `dependabot/*` branches removed); PyPI versions, active cosign signatures, and the underlying git commits are never pruned.

### On the roadmap

These are tracked but not yet shipped: runtime enforcement of the spec-only fields (lifecycle hook execution, command/path `Constraints`, and Cilium-backed `allowedDomains` egress are currently accepted-but-advisory), and horizontal pod autoscaling for the Router.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      Kubernetes Cluster                           │
│                                                                   │
│  ┌────────────┐  ┌────────────┐  ┌──────────┐  ┌───────────┐  │
│  │ Controller │  │   Router   │  │  Web UI  │  │    etcd    │  │
│  │ (operator) │  │ (API + WS) │  │ (nginx)  │  │ (built-in) │  │
│  └─────┬──────┘  └─────┬──────┘  └──────────┘  └───────────┘  │
│        │                │                                        │
│  ┌─────┴────────────────┴────────────────────────────────────┐  │
│  │                  Sandbox Namespace(s)                       │  │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐                │  │
│  │  │Sandbox 1 │  │Sandbox 2 │  │Sandbox N │  ...           │  │
│  │  │Pod + PVC │  │Pod + PVC │  │Pod + PVC │                │  │
│  │  │+ NetPol  │  │+ NetPol  │  │+ NetPol  │                │  │
│  │  └──────────┘  └──────────┘  └──────────┘                │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

## Quickstart

### One-Command Deployment (AWS)

```bash
git clone https://github.com/agenttier/agenttier.git
cd agenttier
./hack/quickstart.sh
```

This provisions an EKS cluster, builds container images, and deploys AgentTier in ~15 minutes. Run `./hack/quickstart.sh destroy` to tear down.

### Manual Installation

Install from the public Helm chart and container images at `ghcr.io/agenttier/*`:

```bash
# 1. Add the AgentTier Helm repo and refresh
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update

# 2. Install the chart (CRDs are bundled)
helm install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace

# 3. Create a sandbox
kubectl apply -f - <<EOF
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata:
  name: my-sandbox
spec:
  templateRef:
    name: general-coding
    kind: ClusterSandboxTemplate
EOF

# 4. Check status
kubectl get sandboxes

# 5. Open a terminal
kubectl exec -it my-sandbox-pod -c sandbox -- /bin/bash
```

> Docs site: **https://agenttier.github.io/agenttier/**
> Pre-v0.2.0 users can still `helm repo add` at `https://agenttier.github.io/agenttier` (root) — both paths resolve to the same charts.

## Sandbox Lifecycle

```
Create → Running → Stop (pod deleted, PVC preserved) → Resume (new pod, same PVC) → Delete (all removed)
```

- **Stop**: Preserves all files, packages, git repos. No compute cost while stopped.
- **Resume**: Restores exact filesystem state. Takes ~5-10 seconds.
- **Delete**: Permanently removes sandbox and all data.

## Templates

Templates define reusable sandbox configurations:

```yaml
apiVersion: agenttier.io/v1alpha1
kind: ClusterSandboxTemplate
metadata:
  name: claude-code-bedrock
spec:
  description: "AI coding environment with Claude Code CLI on Bedrock"
  image:
    repository: ghcr.io/agenttier/sandbox-claude-code:latest
  resources:
    requests: { cpu: "1", memory: 2Gi }
    limits: { cpu: "4", memory: 8Gi }
  storage:
    size: 20Gi
  network:
    allowInternet: true
  harness:
    shell: /bin/bash
    tools:
      - name: claude
        verifyCommand: "claude --version"
    hooks:
      onStart: "echo 'Sandbox ready'"
  timeout: 24h
  idleTimeout: 2h
```

Built-in templates: `general-coding`, `claude-code-bedrock` (Claude Code on Bedrock via IRSA), `openclaw-bedrock` (OpenClaw CLI on Bedrock via IRSA), `strands-bedrock` (Strands Agents Python SDK on Bedrock via IRSA), `langgraph-agent` (`mode: agent` reference), and `minimal-shell`.

## Python SDK

```python
from agenttier import AgentTierClient

client = AgentTierClient(api_url="https://agenttier.company.com")

sandbox = client.create_sandbox(template="general-coding", name="my-sandbox")
sandbox.wait_until_running()

result = sandbox.exec("echo 'Hello from AgentTier!'")
print(result.stdout)  # "Hello from AgentTier!"

sandbox.files.write("/workspace/hello.py", "print('works!')")
sandbox.terminate()
```

Install: `pip install agenttier`

## Configuration

See [`helm/agenttier/values.yaml`](helm/agenttier/values.yaml) for all configuration options.

Key settings:
- `auth.oidc.*` — OIDC provider configuration (Cognito, Okta, Azure AD)
- `defaults.sandbox.*` — Default sandbox resources, storage, timeouts
- `security.gvisor.enabled` — Enable gVisor kernel isolation
- `networking.defaultPolicy` — Default network policy (deny-all or allow-internet)

## Troubleshooting

### Terminal disconnects after long idle periods
The Router already sends RFC 6455 WebSocket control pings and application-level heartbeat messages every 30 seconds, so any load balancer with an idle timeout ≥ 60s will see traffic in both directions and keep the connection open. If you still see drops:

- On **AWS ALB**, the chart's `optional.ingress.annotations` sets `idle_timeout.timeout_seconds=4000` by default. Verify it's applied: `kubectl get ingress agenttier-webui -n agenttier -o yaml`.
- On **AWS Classic ELB**, set the annotation
  `service.beta.kubernetes.io/aws-load-balancer-connection-idle-timeout: "3600"` on the web-ui Service, or run:
  ```bash
  aws elb modify-load-balancer-attributes \
    --load-balancer-name <elb-name> \
    --load-balancer-attributes '{"ConnectionSettings":{"IdleTimeout":3600}}'
  ```
- With multi-replica routers, enable sticky sessions on the target group so a reconnecting browser lands on the same pod. The chart's default ALB annotations already include `stickiness.enabled=true`.

### Terminal shows garbled text / line wrapping issues
Ensure the Router image includes the `Tty: true` fix in StreamOptions. Run `stty size` in the terminal — it should show your actual terminal dimensions (e.g., `40 120`), not `0 0`.

### Sandbox stuck in "Creating" with ImagePullBackOff
The sandbox image can't be pulled. Check:
1. Template image reference: `kubectl get clustersandboxtemplate <name> -o jsonpath='{.spec.image.repository}'`
2. Node can reach the registry: ECR images need the node role to have `AmazonEC2ContainerRegistryReadOnly` policy
3. For private registries, set `spec.image.pullSecret` in the sandbox spec

### DyePack / Security alerts on public endpoints
Never expose services with `0.0.0.0/0`. Use:
- `loadBalancerSourceRanges` to restrict to specific IPs
- Or use `kubectl port-forward` for local access (no public exposure)
- Or deploy an Ingress with OIDC authentication

### Docker Hub rate limits during image build
All Dockerfiles use `public.ecr.aws/docker/library/*` base images. If you see 429 errors, ensure your Dockerfiles reference ECR Public, not Docker Hub directly.

## Requirements

- Kubernetes 1.27+
- CNI with NetworkPolicy support (Calico, Cilium, or AWS VPC CNI)
- CSI storage driver (EBS CSI, PD CSI, or any CSI-compliant driver)
- Helm 3.x

## Project Structure

```
agenttier/
├── cmd/controller/     # Kubernetes operator entrypoint
├── cmd/router/         # REST API + WebSocket terminal server
├── cmd/cli/            # CLI tool
├── api/v1alpha1/       # CRD type definitions
├── pkg/controller/     # Reconciliation logic
├── pkg/router/         # HTTP handlers, auth, terminal bridge
├── web-ui/             # React frontend (TypeScript + Vite)
├── helm/agenttier/     # Helm chart
├── terraform/aws-eks/  # AWS infrastructure (EKS + Cognito + ECR)
├── images/             # Reference Dockerfiles
├── python-sdk/         # Python SDK (pip install agenttier)
├── docs/               # Documentation (MkDocs)
└── hack/               # Scripts (quickstart, codegen, load testing)
```

## Contributing

We welcome contributions! See [CONTRIBUTING.md](CONTRIBUTING.md) for:
- Development setup
- Coding standards
- Testing requirements
- Pull request process

## License

Apache License 2.0 — see [LICENSE](LICENSE) for details.

## Acknowledgments

Built with:
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) — Kubernetes operator framework
- [kubebuilder](https://github.com/kubernetes-sigs/kubebuilder) — CRD scaffolding
- [gorilla/websocket](https://github.com/gorilla/websocket) — WebSocket implementation
- [xterm.js](https://xtermjs.org/) — Terminal emulator for the browser
- [React](https://react.dev/) — Web UI framework
