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

- **Create, stop, resume, delete** — sandboxes spin up from a template; stopping preserves the workspace, packages, and git state on a persistent volume; resume reattaches the same volume in seconds; idle and max-runtime caps auto-stop with grace.
- **Sub-second cold starts** — per-template warm pools, optional immediate PVC binding, and an opt-in image pre-pull DaemonSet take creation from ~10 s down to ~800 ms.
- **Self-healing** — bounded retries on infrastructure failures with structured Kubernetes events for every transition; clean Error state once the retry budget is exhausted.

### Templates and agent harnesses

- **Compose templates from other templates** with field-level merge and per-sandbox overrides; the harness block defines the shell, tools, system prompt, hooks, and init scripts.
- **Reference images out of the box** — general coding, Claude Code on AWS Bedrock (with cloud-native credential injection), minimal shell, and a LangGraph agent-mode image.

### Agent mode

- **Run an agent on demand** — configure a sandbox once with your code and install command, then call `/invoke` to run it; output streams back as Server-Sent Events and closing the connection cancels the in-pod process.
- **Bring your own framework or harness** — the LangGraph reference template ships in the box; the same shape works for Strands Agents, AutoGen, OpenHands, OpenClaw, or any pip-installable agent library. The framework owns the loop; AgentTier owns lifecycle, auth, transport, audit, and governance.
- **Throttle, time out, and audit every invoke** — per-sandbox concurrency caps return a clean 429 with `Retry-After`; default 30-minute per-invoke timeout with a cluster ceiling; OpenTelemetry spans, Prometheus metrics, and Kubernetes events emitted on every configure and invoke.
- **Optional local memory** — Helm flag adds a `mem0` sidecar next to the agent. Bring-your-own memory (PVC-local, Pinecone, Postgres + pgvector, AgentCore Memory) is fully supported and documented.

### Security and isolation

- **Locked-down sandboxes by default** — non-root user, read-only root filesystem with a writable in-memory `/tmp`, all capabilities dropped, `seccomp=RuntimeDefault`, and per-sandbox service accounts with no cluster permissions.
- **Network isolation** — default deny-all egress with a configurable allow-list and always-on DNS; optional gVisor RuntimeClass for kernel-level isolation of untrusted workloads.
- **Cloud-native credentials** — wire AWS Bedrock and other cloud APIs in via IRSA on EKS or workload identity on GKE; mount Kubernetes Secrets as env vars or files; no long-lived secrets baked into images.
- **Hashed share-link tokens** — share links store SHA-256 hashes at rest, never the raw secret, with constant-time comparison on validation.

### Interactive access

- **Browser terminal that survives drops** — full PTY over WebSocket with reconnect, plus an in-pod terminal endpoint that bypasses the Kubernetes API server so long sessions survive load-balancer and apiserver-side timeouts; a tmux wrap keeps the same shell across reconnects.
- **Run commands programmatically** — fire-and-forget or request-response exec, file upload/download/list, and port forwarding with authenticated previews through the Router; ports also surface as Ingress URLs when a preview domain is configured.

### Multi-tenancy and governance

- **Plug into any OIDC provider** — Cognito, Okta, Azure AD, or anything OIDC-compliant, plus API keys stored as SHA-256 hashes with LRU caching.
- **Hierarchical governance policies** — cluster and per-namespace caps on sandbox counts, CPU / memory / storage, idle and max-runtime timeouts, agent-mode concurrency, allowed templates, and approved image registries; violations return a structured response so UIs can pinpoint the failing field.
- **Per-IP and per-user rate limiting** — opt-in token-bucket throttling on Router endpoints, with health checks and WebSocket terminals exempt; 429 responses carry `Retry-After`.
- **Built-in audit trail** — every lifecycle, terminal, credential, share, clone, and port-forward event is recorded as a Kubernetes event (and optionally a row in a SQL backend for long-term retention).

### Web UI

- **One-click sandbox management** — dashboard cards show status, template, age and run Stop / Resume / Delete / Open Terminal; running cards expose inline Files and Port-forward panels.
- **Browser-based admin** — YAML template editor, time-ordered activity log with filters, live metrics + monthly cost estimator, and a Settings page for governance policies and warm-pool sizing.

### Client tooling

- **`pip install agenttier`** — installs both the Python SDK (sync + async, typed models, auto-detected auth, streaming file transfers, opt-in retry layer with backoff and `Retry-After`) and the same `agenttier` shell command on PATH.
- **Cross-platform CLI** — Go binary for sandbox and template management distributed for linux / darwin / windows on amd64 + arm64; the `pip` install gives you the same command tree without the Go dependency.
- **REST API** — documented endpoints for sandboxes, templates, governance, audit, sharing, port forwarding, files, configure, and invoke.

### Performance and observability

- **Tracking and pre-warming** — per-sandbox startup duration in logs and events for regression tracking; optional warm pool, immediate PVC binding, and image pre-pull eliminate cold-start latency.
- **OpenTelemetry traces and Prometheus metrics** — distributed traces across controller and router with trace context in structured JSON logs; `/metrics` exposes sandbox counts, startup histograms, queue depth, error counters, terminal stats, and the agent-mode invoke / configure / throttle metrics.
- **Continuous CVE scanning** — every sandbox base image is scanned with Trivy on every push; findings land in the GitHub Security tab as SARIF.

### Deployment and operations

- **Single Helm chart** — one `helm install` deploys controller, router, web UI, CRDs, RBAC, and optional add-ons (gVisor, ServiceMonitor, PDB, image pre-pull, OTel Collector, mem0 sidecar, rate limiting).
- **Production load-balancer support** — opt-in Ingress template with AWS Load Balancer Controller defaults (4000 s idle timeout, sticky sessions, IP allow-list); compatible with ingress-nginx and Traefik via a single override.
- **Multi-cluster ready** — runs on EKS, GKE, AKS, and self-managed Kubernetes 1.27+ on any CNI with NetworkPolicy support.
- **Highly available** — multi-replica controllers with leader election; multi-replica router with HTTP-routed exec, files, and invoke so any replica can serve any request.
- **Container images you can verify** — every image is multi-arch, cosign-signed via GitHub Actions OIDC, and ships SPDX + CycloneDX SBOMs as OCI attestations.

### On the roadmap

These are tracked but not yet shipped: Terraform module for EKS, sharing and collaboration UX, webhook / email / Slack notifications, sandbox cloning via VolumeSnapshot, inter-sandbox networking, optional SQL backend for state, validating admission webhook, and additional reference images (Strands Agents on Bedrock, OpenHands, OpenClaw, RL training).

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
