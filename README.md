<p align="center">
  <h1 align="center">AgentTier</h1>
  <p align="center">
    <strong>Enterprise-grade Kubernetes operator for managing isolated, persistent sandboxes for AI agents.</strong>
  </p>
  <p align="center">
    <a href="https://github.com/agenttier/agenttier/actions"><img src="https://github.com/agenttier/agenttier/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
    <a href="https://github.com/agenttier/agenttier/releases"><img src="https://img.shields.io/github/v/release/agenttier/agenttier" alt="Release"></a>
    <a href="https://goreportcard.com/report/github.com/agenttier/agenttier"><img src="https://goreportcard.com/badge/github.com/agenttier/agenttier" alt="Go Report Card"></a>
    <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License"></a>
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

## Features

| Feature | Description |
|---------|-------------|
| **Declarative Sandboxes** | Kubernetes CRDs for sandbox lifecycle management |
| **Persistent Storage** | PVC-backed workspaces that survive stop/resume cycles |
| **Network Isolation** | Default deny-all egress with configurable rules |
| **Template System** | Reusable blueprints with inheritance and agent harness config |
| **Interactive Terminal** | Full PTY access via WebSocket (xterm.js in browser) |
| **Security-First** | Non-root pods, read-only rootfs, gVisor support, IRSA/Workload Identity |
| **Self-Healing** | Auto-restart on infrastructure failures with exponential backoff |
| **Multi-Tenant** | Namespace isolation, RBAC, governance policies, per-user limits |
| **Observable** | OpenTelemetry logs/metrics/traces + Prometheus endpoint |
| **Web UI** | React dashboard with sandbox management and terminal |
| **Python SDK** | Programmatic sandbox management for agent orchestrators |
| **CLI** | Full-featured command-line tool |

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                      Kubernetes Cluster                           │
│                                                                   │
│  ┌────────────┐  ┌────────────┐  ┌──────────┐  ┌───────────┐  │
│  │ Controller │  │   Router   │  │  Web UI  │  │  MongoDB  │  │
│  │ (operator) │  │ (API + WS) │  │ (nginx)  │  │(optional) │  │
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

```bash
# 1. Install CRDs
kubectl apply -f config/crd/

# 2. Deploy via Helm
helm install agenttier ./helm/agenttier/ \
  --namespace agenttier --create-namespace \
  --set controller.image.repository=ghcr.io/agenttier/controller \
  --set router.image.repository=ghcr.io/agenttier/router

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
- `mongodb.enabled` — Enable persistent datastore for audit/governance

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
├── pkg/mongodb/        # Persistent datastore
├── pkg/governance/     # Policy enforcement
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
