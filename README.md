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

AgentTier is a Kubernetes-native platform that provides isolated, persistent sandbox environments for running AI agents and human developers. Each sandbox is a pod with its own persistent storage, network isolation, and interactive terminal access — managed declaratively through Custom Resource Definitions.

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

---

## Deploy from source

All paths build images from source. No published artifacts are required.

### Configuration (optional)

Copy the example config and edit before running `deploy.sh`. All variables have defaults — skip this step to use the defaults.

```bash
cp config/config.env.example config/config.env
# Edit config/config.env to override registry, tag, region, etc.
```

Key variables (see `config/config.env.example` for the full list):

| Variable | Default | Purpose |
|---|---|---|
| `AGENTTIER_REGISTRY` | `ghcr.io/agenttier` | Registry prefix for built images |
| `AGENTTIER_IMAGE_TAG` | _(derived)_ | Derived from `VERSION` file (clean tree) or `sha-<hash>[-dirty]` |
| `AGENTTIER_AWS_REGION` | `us-east-1` | AWS region for Terraform + ECR |
| `AGENTTIER_EKS_PLATFORM` | `linux/amd64` | Target build platform for EKS nodes |
| `AGENTTIER_NAMESPACE` | `agenttier` | Kubernetes namespace for the Helm release |

### Path 1: Local cluster (kind or minikube)

**Prerequisites:** `docker`, `kubectl`, `helm`, `go` 1.25+, and `kind` or `minikube`.

```bash
git clone https://github.com/agenttier/agenttier.git
cd agenttier
./deploy.sh --target=local
```

What this does:

1. Creates a `kind` (or `minikube`) cluster named `agenttier-local` if one does not exist.
2. Builds all four container images (controller, router, web-ui, sandbox-general) from source for your local architecture.
3. Side-loads images into the cluster — no registry or push required.
4. Installs the Helm chart from the local `helm/agenttier/` tree with `auth.devAuth=true` (local development only; never set in production).
5. Runs `hack/smoke-test.sh` — creates a test sandbox, waits for `Phase=Running`, runs an exec, then cleans up.

**Expected output** (abbreviated):

```
==> Checking local prerequisites
[agenttier] Local prerequisites OK.
==> Ensuring local cluster exists
[agenttier] Creating kind cluster 'agenttier-local'...
==> Building container images (local arch)
[agenttier] Building controller image: ghcr.io/agenttier/controller:sha-<hash>
[agenttier] Building sandbox-general image: ghcr.io/agenttier/sandbox-general:sha-<hash>
==> Loading images into local cluster
==> Installing / upgrading Helm chart
[agenttier] Helm release: agenttier → namespace: agenttier
==> Running smoke test
[smoke] controller Available
[smoke] sandbox phase=Running
[smoke] exec OK
[smoke] PASSED

[agenttier] Local deploy complete!
[agenttier] Access the web UI:   kubectl port-forward -n agenttier svc/agenttier-webui 8080:80
[agenttier] Access the router:   kubectl port-forward -n agenttier svc/agenttier-router 8081:8080
[agenttier] Tear down:           ./deploy.sh --target=local --teardown
```

**Teardown:**

```bash
./deploy.sh --target=local --teardown
```

Uninstalls the Helm release and deletes the kind/minikube cluster.

---

### Path 2: AWS EKS

**Prerequisites:** `aws` CLI (configured with credentials), `terraform` >= 1.5, `docker` with `buildx`, `kubectl`, `helm`, `jq`, `zip`.

**Cost note:** an EKS cluster with the default Terraform configuration costs approximately $8-10/day. Run `./deploy.sh --target=eks --teardown` when done to avoid ongoing charges.

```bash
git clone https://github.com/agenttier/agenttier.git
cd agenttier

# Optional: override AWS region (default: us-east-1) and other settings
cp config/config.env.example config/config.env
# Edit AGENTTIER_AWS_REGION in config/config.env

./deploy.sh --target=eks
```

What this does:

1. Verifies AWS credentials via `aws sts get-caller-identity`.
2. Runs `terraform apply` in `terraform/aws-eks/` — provisions VPC, EKS cluster, managed node groups (including an optional gVisor group), EBS CSI, AWS Load Balancer Controller, IRSA roles, ECR repositories, and a Cognito User Pool for OIDC auth.
3. Reads ECR registry URLs and Cognito OIDC settings from Terraform outputs. Any empty mandatory output fails immediately with a clear message.
4. Authenticates Docker to ECR via `aws ecr get-login-password`.
5. Builds and pushes all four images to ECR using `docker buildx` at `$AGENTTIER_EKS_PLATFORM` (default: `linux/amd64`).
6. Configures `kubectl` for the new cluster.
7. Installs the Helm chart from the local `helm/agenttier/` tree, wiring Cognito OIDC auth and deploying a default gp3 EBS StorageClass so sandbox PVCs bind immediately. Dev-auth is never set on the EKS path.
8. Runs `hack/smoke-test.sh`.

**Expected output** (abbreviated):

```
==> Checking EKS prerequisites
[agenttier] EKS prerequisites OK.
==> Provisioning infrastructure via Terraform
...
==> Reading Terraform outputs
[agenttier] ECR registry     : 123456789012.dkr.ecr.us-east-1.amazonaws.com/agenttier
[agenttier] EKS cluster      : agenttier-eks
==> Building images with docker buildx (platform: linux/amd64)
...
==> Installing / upgrading Helm chart
==> Running smoke test
[smoke] PASSED

[agenttier] EKS deploy complete!
[agenttier] Cluster         : agenttier-eks
[agenttier] Region          : us-east-1
[agenttier] Cognito issuer  : https://cognito-idp.us-east-1.amazonaws.com/...
[agenttier] Estimated cost: ~$8-10/day while cluster is running.
```

**Teardown** (removes all billable AWS resources):

```bash
./deploy.sh --target=eks --teardown
```

This uninstalls the Helm release, deletes sandbox PVCs and LoadBalancer services (waiting for AWS LB deprovisioning), then runs `terraform destroy -auto-approve`. No orphaned resources are left behind.

---

### CodeBuild opt-in (EKS only)

For air-gapped or slow-network environments, image builds can be offloaded to AWS CodeBuild. This is disabled by default (`enable_codebuild=false`). Enable it in `terraform/aws-eks/variables.tf` or by passing `-var="enable_codebuild=true"` to `terraform apply`. `deploy.sh` detects the CodeBuild project via Terraform outputs and switches automatically.

---

### Forking / custom registry

To deploy from a fork using your own registry, set the following in `config/config.env`:

```bash
AGENTTIER_REGISTRY=your-registry.example.com/your-prefix
AGENTTIER_AWS_REGION=us-west-2        # if using EKS
```

No source edits are required. The EKS path reads the actual ECR registry URL from `terraform output ecr_registry` and overrides `AGENTTIER_REGISTRY` automatically.

---

## Usage

After a successful deploy, interact with AgentTier via the Python SDK, the CLI, or the REST API.

### Port-forward (local path)

```bash
# Web UI at http://localhost:8080
kubectl port-forward -n agenttier svc/agenttier-webui 8080:80 &

# Router API at http://localhost:8081
kubectl port-forward -n agenttier svc/agenttier-router 8081:8080 &
```

### Python SDK

Install from PyPI or from the source tree:

```bash
pip install agenttier           # from PyPI
# OR
pip install -e python-sdk/      # from source (no PyPI dependency)
```

```python
from agenttier import AgentTierClient

client = AgentTierClient(api_url="http://localhost:8081")  # or your EKS ALB URL

# Create a sandbox and wait for it to be Running
sandbox = client.create_sandbox(template="general-coding", name="my-sandbox")
sandbox.wait_until_running()

# Run a command
result = sandbox.exec("echo 'Hello from AgentTier!'")
print(result.stdout)  # Hello from AgentTier!

# Upload and download files
sandbox.files.write("/workspace/hello.py", "print('works!')")
content = sandbox.files.read("/workspace/hello.py")

# Open a terminal (returns a WebSocket URL)
terminal_url = sandbox.terminal_url()

# Stop and resume (all files are preserved)
sandbox.stop()
sandbox.resume()

# Clone (byte-identical workspace fork via CSI VolumeSnapshot)
clone = sandbox.clone(name="my-sandbox-clone")

# Delete
sandbox.terminate()
```

### CLI

Install from PyPI or build from source:

```bash
pip install agenttier           # from PyPI
# OR
make build && export PATH="$PATH:$(pwd)/bin"   # from source
```

```bash
export AGENTTIER_URL=http://localhost:8081

# List and create sandboxes
agenttier sandbox list
agenttier sandbox create --template general-coding --name my-sandbox

# Exec, stop, resume, delete
agenttier sandbox exec my-sandbox -- echo "Hello"
agenttier sandbox stop my-sandbox
agenttier sandbox resume my-sandbox
agenttier sandbox delete my-sandbox

# Templates
agenttier template list
```

### Agent mode

```bash
# Configure a sandbox with your agent code
agenttier agent configure my-sandbox \
  --code ./my_agent.py \
  --install "pip install -r requirements.txt"

# Invoke (streams output as Server-Sent Events; closing the connection cancels the run)
agenttier agent invoke my-sandbox --prompt "Summarize the README"
```

---

## Sandbox lifecycle

```
Create -> Running -> Stop (pod deleted, PVC preserved) -> Resume (new pod, same PVC) -> Delete (all removed)
```

- **Stop** - preserves all files, packages, and git state. No compute cost while stopped.
- **Resume** - restores the exact filesystem state in ~5-10 seconds (warm pool: ~800 ms).
- **Delete** - permanently removes the sandbox and all data.
- **Clone** - takes a CSI VolumeSnapshot of the source PVC and provisions a new sandbox with a byte-identical workspace.

---

## Templates

Templates define reusable sandbox configurations. Built-in templates installed by the chart:

| Template | Description |
|---|---|
| `general-coding` | General-purpose coding sandbox |
| `claude-code-bedrock` | Claude Code CLI on AWS Bedrock (IRSA) |
| `openclaw-bedrock` | OpenClaw CLI on AWS Bedrock (IRSA) |
| `strands-bedrock` | Strands Agents Python SDK on AWS Bedrock (IRSA) |
| `langgraph-agent` | LangGraph agent-mode reference |
| `minimal-shell` | Minimal shell, no pre-installed tooling |

Example template manifest:

```yaml
apiVersion: agenttier.io/v1alpha1
kind: ClusterSandboxTemplate
metadata:
  name: claude-code-bedrock
spec:
  description: "AI coding environment with Claude Code CLI on Bedrock"
  image:
    repository: ghcr.io/agenttier/sandbox-claude-code
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

---

## Architecture

```
+------------------------------------------------------------------+
|                      Kubernetes Cluster                          |
|                                                                  |
|  +------------+  +------------+  +----------+  +-----------+   |
|  | Controller |  |   Router   |  |  Web UI  |  |    etcd   |   |
|  | (operator) |  | (API + WS) |  | (nginx)  |  | (built-in)|   |
|  +-----+------+  +-----+------+  +----------+  +-----------+   |
|        |                |                                        |
|  +-----+----------------+--------------------------------------+ |
|  |                  Sandbox Namespace(s)                        | |
|  |  +----------+  +----------+  +----------+                  | |
|  |  |Sandbox 1 |  |Sandbox 2 |  |Sandbox N |  ...            | |
|  |  |Pod + PVC |  |Pod + PVC |  |Pod + PVC |                  | |
|  |  |+ NetPol  |  |+ NetPol  |  |+ NetPol  |                  | |
|  |  +----------+  +----------+  +----------+                  | |
|  +-------------------------------------------------------------+ |
+------------------------------------------------------------------+
```

Four binaries, three client surfaces:

- **controller** - Kubernetes operator; reconciles `Sandbox` objects through a phase state machine; manages CRDs on startup.
- **router** - REST + WebSocket API gateway; all user/SDK/UI traffic flows here; handles auth, governance, rate limiting.
- **sandbox-runtime** - small HTTP server baked into sandbox images; enables exec/PTY across Router replicas without SPDY.
- **cli** - the `agenttier` Go binary; talks to the Router REST API.
- **web-ui** - React + Vite + TypeScript dashboard.
- **python-sdk** - `pip install agenttier`; ships both the SDK and the `agenttier` CLI.

### CRD management

The controller applies its bundled CRDs on startup (`controller.manageCRDs=true` by default). Do not pre-apply `config/crd/` manually - the controller's startup apply is the canonical path and ensures new CRD fields from a `helm upgrade` are active immediately. Set `controller.manageCRDs=false` only when CRDs are managed out-of-band via GitOps.

---

## Configuration reference

All Helm values are documented in [`helm/agenttier/values.yaml`](helm/agenttier/values.yaml). Key settings:

| Value | Purpose |
|---|---|
| `auth.oidc.*` | OIDC provider configuration (Cognito, Okta, Azure AD) |
| `auth.devAuth` | Enable dev-auth - local development only; never set in production |
| `defaults.sandbox.*` | Default sandbox resources, storage, timeouts |
| `security.gvisor.enabled` | Enable gVisor kernel isolation |
| `optional.storageClass.enabled` | Deploy a gp3 EBS StorageClass (EKS) |
| `optional.storageClass.isDefaultClass` | Mark the deployed StorageClass as the cluster default |
| `observability.otelCollector.enabled` | Deploy bundled OpenTelemetry Collector |

---

## Requirements

- Kubernetes 1.27+
- CNI with NetworkPolicy support (Calico, Cilium, or AWS VPC CNI)
- CSI storage driver (EBS CSI, PD CSI, or any CSI-compliant driver)
- Helm 3.x

---

## Project structure

```
agenttier/
+-- cmd/controller/     # Kubernetes operator entrypoint
+-- cmd/router/         # REST API + WebSocket terminal server
+-- cmd/cli/            # CLI tool
+-- api/v1alpha1/       # CRD type definitions
+-- pkg/controller/     # Reconciliation logic
+-- pkg/router/         # HTTP handlers, auth, terminal bridge
+-- web-ui/             # React frontend (TypeScript + Vite)
+-- helm/agenttier/     # Helm chart
+-- terraform/aws-eks/  # AWS infrastructure (EKS + Cognito + ECR)
+-- images/             # Reference Dockerfiles for sandbox images
+-- python-sdk/         # Python SDK (pip install agenttier)
+-- docs/               # Documentation (MkDocs)
+-- hack/               # Scripts (deploy helpers, codegen, smoke test)
+-- deploy.sh           # Single deploy entrypoint (--target=local|eks)
+-- config/             # Configuration surface (config.env.example)
```

---

## Development

### Build and test

```bash
make build            # build controller, router, cli -> bin/
make test             # unit tests: go test -race ./pkg/... ./api/...
make lint             # golangci-lint v2 (see .golangci.yml)
make fmt              # gofmt -s + goimports
make vet
make generate manifests   # regenerate deepcopy + CRDs (after editing api/)
make verify-codegen       # fail if generated files are stale
make helm-lint            # helm lint helm/agenttier/
```

After editing anything in `api/`, run `make generate manifests` and commit `api/`, `config/crd/`, and `pkg/crds/` together. CI enforces this with `make verify-codegen`.

### Web UI

```bash
cd web-ui
npm ci
npm run dev      # Vite dev server
npm run build    # tsc + vite build (enforces <=750 KB bundle budget)
npm run lint     # eslint --max-warnings 0
```

### Python SDK

```bash
cd python-sdk
pip install -e ".[dev]"
pytest tests/
mypy src/agenttier/    # strict mode
ruff check .
```

### Go version

Go 1.25 is required (`go.mod` declares `go 1.25.0`). All Docker images and CI use Go 1.25.

---

## Troubleshooting

### Sandbox stuck in "Creating" with ImagePullBackOff

The sandbox image cannot be pulled. Check:

1. Template image reference: `kubectl get clustersandboxtemplate <name> -o jsonpath='{.spec.image.repository}'`
2. On EKS, the node role must have `AmazonEC2ContainerRegistryReadOnly`. On a local cluster, images must be side-loaded by `deploy.sh`.
3. For private registries, set `spec.image.pullSecret` in the sandbox spec.

### Terminal disconnects after long idle periods

The Router sends RFC 6455 WebSocket pings every 30 seconds. Any load balancer with an idle timeout >= 60s will keep the connection open. On AWS ALB, the chart's default annotations set `idle_timeout.timeout_seconds=4000`. Verify: `kubectl get ingress agenttier-webui -n agenttier -o yaml`.

### Terminal shows garbled text / line-wrapping issues

Run `stty size` inside the terminal - it should show your actual dimensions (e.g., `40 120`), not `0 0`. Ensure the Router image includes the `Tty: true` fix in StreamOptions.

### Docker Hub rate limits during image build

All Dockerfiles use `public.ecr.aws/docker/library/*` base images pinned by digest. If you see 429 errors, verify your Dockerfiles are not referencing Docker Hub directly.

---

## Installing from published artifacts (secondary path)

If you want to install from pre-built images and the published Helm chart rather than building from source:

```bash
# 1. Add the Helm repo and refresh
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update

# 2. Install (CRDs are bundled; the controller applies them on startup)
helm install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace

# 3. Create a sandbox
kubectl apply -f - <<'EOF'
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
```

For a production EKS deployment from published artifacts, see the [documentation site](https://agenttier.github.io/agenttier/quickstart/).

---

## Contributing

We welcome contributions. See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, coding standards, testing requirements, and the pull request process.

---

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

---

## Acknowledgments

Built with:
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) — Kubernetes operator framework
- [kubebuilder](https://github.com/kubernetes-sigs/kubebuilder) — CRD scaffolding
- [gorilla/websocket](https://github.com/gorilla/websocket) — WebSocket implementation
- [xterm.js](https://xtermjs.org/) — Terminal emulator for the browser
- [React](https://react.dev/) — Web UI framework
