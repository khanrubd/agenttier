<p align="center">
  <h1 align="center">AgentTier</h1>
  <p align="center">
    <strong>Enterprise-grade Kubernetes operator for isolated, persistent sandboxes — for humans and AI agents.</strong>
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
    <a href="https://agenttier.github.io/agenttier/sdk/">SDK</a> ·
    <a href="https://github.com/agenttier/agenttier/releases/latest">Releases</a>
  </p>
</p>

---

## What is AgentTier?

AgentTier is a Kubernetes-native platform that provides isolated, persistent sandbox environments for running AI agents. Each sandbox is a pod with its own persistent storage, network isolation, and interactive terminal access — managed declaratively through Custom Resource Definitions.

**Key use cases:**
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

- **Declarative sandboxes** — Kubernetes CRDs (`Sandbox`, `SandboxTemplate`, `ClusterSandboxTemplate`) manage the full lifecycle with an operator-driven state machine.
- **Stop and resume** — Stopped sandboxes preserve their PVC, workspace, packages, and git state; resuming re-attaches the same volume in seconds.
- **Idle and max-runtime timeouts** — Per-sandbox and per-namespace policies auto-stop sandboxes, with configurable grace periods to notify connected sessions.
- **Self-healing** — Automatic restart with exponential backoff on infrastructure failures, permanent Error state after retry budget exhausted.
- **Sub-second warm pool** — Optional pre-provisioned Pod + PVC pool claims a sandbox in ~800ms (measured) vs ~10s cold start.

### Templates and agent harnesses

- **Template inheritance** — Templates can extend other templates via `inheritsFrom`, with field-level merge and sandbox-level overrides.
- **Agent harness config** — Templates describe the shell, tools, system prompt, hooks, and init scripts needed to run a specific agent (Claude Code, Cursor, Aider, custom).
- **Ready-to-use images** — Bundled Dockerfiles for `general-coding`, `claude-code-developer`, `minimal-shell`, `security-scanner`, and `data-analysis` workloads.
- **Claude Code + Bedrock** — First-class support for running Claude Code against Amazon Bedrock, including IRSA-based IAM credential injection.

### Security and isolation

- **Network isolation** — Default deny-all egress with configurable allow rules, always-on DNS, and optional inter-sandbox peering via label selectors.
- **Hardened pod defaults** — Non-root user, read-only root filesystem, all capabilities dropped, `seccomp=RuntimeDefault`, and per-sandbox ServiceAccounts with no cluster permissions.
- **Kernel-level isolation** — Optional gVisor RuntimeClass for untrusted agent workloads.
- **Per-session credentials** — STS AssumeRole or secret credentials injected at terminal session start (not baked into the image).
- **IRSA / Workload Identity** — Cloud-native credential attachment on EKS and GKE without long-lived secrets.

### Interactive access

- **Browser terminal** — Full PTY over WebSocket with xterm.js, resize, ANSI colors, and reconnection after transient drops.
- **Session reconnection** — 30s default grace window lets network blips and laptop sleeps reconnect without losing shell state.
- **Non-interactive exec API** — `POST /api/v1/sandboxes/{id}/exec` for request-response and fire-and-forget commands.
- **Port forwarding** — Expose any container port via `POST /api/v1/sandboxes/{id}/ports`; the controller creates a Service (and optional Ingress when a preview domain is configured), the Router provides an authenticated in-cluster reverse proxy at `/api/v1/sandboxes/{id}/preview/{port}/...`, and exposed ports show up both in the Web UI sandbox card and in `Sandbox.status.forwardedPorts`.
- **File transfer API** — Upload, download, and list files in the sandbox workspace through the REST API.

### Multi-tenancy and governance

- **OIDC + API keys** — Cognito, Okta, Azure AD, or any OIDC-compliant provider; API keys stored as SHA-256 hashes with LRU caching.
- **Hierarchical governance policies** — Cluster → namespace policy resolution with field-level merge, enforced synchronously at sandbox creation. Limits max sandboxes per user and total, CPU/memory/storage caps, timeout caps, allowed templates, and approved image registries. Violations return a structured `policy_violation` response with machine codes so UIs can pinpoint the failing field.
- **Admin-gated policy editor** — `Settings → Governance` renders per-scope editors, protected by the `isAdmin` claim in production; dev mode (no OIDC configured) auto-grants admin so the flow is fully exercised locally.
- **Audit trail** — Lifecycle, terminal, credential, share, clone, and port-forward events recorded to Kubernetes events (and optional SQL backend for long-term retention).
- **Sharing and collaboration** — User or group sharing with viewer/collaborator roles and expiring share links (in progress).

### Web UI

- **Dashboard** — Sandbox cards with status, template, age, and one-click Stop / Resume / Delete / Open Terminal. Running cards also show an inline "Port forwards" panel for exposing container ports and opening authenticated previews.
- **Templates editor** — In-browser YAML editor for creating, editing, and deleting `ClusterSandboxTemplate`s with syntax highlighting.
- **Activity log** — Time-ordered audit events with filter-by-action, user, and time range.
- **Metrics and cost estimator** — Live sandbox counts, average startup time, and estimated monthly cost based on current running resources.
- **Settings** — Governance policies (cluster and per-namespace), warm pool sizing and template, and other operational knobs persisted to the backend.

### Client tooling

- **Python SDK (`pip install agenttier`)** — Sync + async clients with auto-detected auth (kubeconfig / OIDC / API key), typed Pydantic models, and streaming file transfers.
- **CLI (`agenttier`)** — Go binary for sandbox and template management from the terminal, distributed for linux/darwin/windows on amd64 + arm64.
- **REST API** — Fully documented REST endpoints for sandboxes, templates, governance, audit, sharing, and port forwarding.

### Performance

- **Startup duration logging** — Per-sandbox `startupDurationMs` in controller logs and Kubernetes events for regression tracking.
- **Immediate PVC binding** — Warm pool uses `gp3-immediate` so EBS volumes are provisioned ahead of pod scheduling.
- **Image pre-pull** — Optional DaemonSet pre-caches sandbox images on every node to eliminate first-pull latency.

### Observability

- **OpenTelemetry** — Distributed traces across controller and router with trace context in structured JSON logs; OTLP export.
- **Prometheus metrics** — `/metrics` endpoint exposes sandbox counts, startup duration histogram, reconciliation queue depth, error counters, and terminal session stats.
- **Kubernetes-native events** — Every lifecycle transition emits a typed Event on the Sandbox resource for native `kubectl describe` inspection.

### Deployment and operations

- **Single Helm chart** — One `helm install` deploys controller, router, web UI, CRDs, RBAC, and optional add-ons (gVisor, ServiceMonitor, PDB, image pre-pull, OTel Collector).
- **Terraform EKS module** — Opinionated VPC + EKS + managed node groups + gVisor nodes + EBS CSI + ALB controller + IRSA + Helm release for one-command provisioning.
- **Multi-cluster ready** — Works on EKS, GKE, AKS, and self-managed Kubernetes 1.27+ with any CNI that supports NetworkPolicy.
- **Leader-elected controller** — Multi-replica HA with Lease-based election; degraded mode for non-critical dependency failures.
- **Kubernetes-native state** — Defaults to using Kubernetes etcd + Events for all state; optional SQL backend for compliance-driven long-term retention.

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
  name: claude-code-developer
spec:
  description: "AI coding environment with Claude Code CLI"
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

Built-in templates: `general-coding`, `claude-code-developer`, `minimal-shell`, `security-scanner`, `data-analysis`.

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

### Terminal disconnects every 60 seconds
The AWS Classic Load Balancer has a default idle timeout of 60 seconds. Increase it:
```bash
aws elb modify-load-balancer-attributes \
  --load-balancer-name <elb-name> \
  --load-balancer-attributes '{"ConnectionSettings":{"IdleTimeout":3600}}'
```
Or use the Kubernetes annotation: `service.beta.kubernetes.io/aws-load-balancer-connection-idle-timeout: "3600"`

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
