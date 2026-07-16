# Quickstart

Get from zero to a running sandbox in under ten minutes.

## Prerequisites

**Build tools (required for building from source):**

- **Go 1.25+** — `go version`
- **Docker with buildx** — `docker buildx version` (local path and default EKS path; **not needed** for the EKS CodeBuild path, which builds in AWS — auto-selected when Docker is absent or forced with `AGENTTIER_USE_CODEBUILD=true`)
- **Helm 3.x** — `helm version`
- **kubectl** configured for your target cluster
- **kind** (local path) or **Terraform >= 1.10** + **AWS CLI v2** + **jq** + **zip** (EKS path)

**Cluster requirements:**

- Kubernetes **1.27+**
- CNI with NetworkPolicy support (Calico, Cilium, AWS VPC CNI with NetworkPolicy enabled)
- A CSI storage driver with a default StorageClass (EBS CSI, PD CSI, or local-path on kind)

## 1. Deploy AgentTier

### Option A — Local (kind or minikube)

```bash
# Clone and deploy in one shot — creates a kind cluster, builds images, loads them, installs Helm chart
git clone https://github.com/agenttier/agenttier.git
cd agenttier
./deploy.sh --target=local
# Or force minikube instead of kind (autodetection prefers kind when both are installed):
./deploy.sh --target=local --cluster-tool=minikube
```

This creates a kind (or minikube) cluster (if none exists), builds the controller/router/web-ui/sandbox images from source, loads them, and installs the Helm chart with dev-auth enabled. Smoke test runs at the end. Use `--cluster-tool=kind` or `--cluster-tool=minikube` (or the `AGENTTIER_CLUSTER_TOOL` env var) to force a choice; unset autodetects.

> **Image build fails with a `proxy.golang.org` DNS timeout?** Some networks (corporate VPNs,
> certain home/coffee-shop Wi-Fi) can't reach `proxy.golang.org` from inside Docker's build
> network even though the host can. Re-run with `GOPROXY=direct ./deploy.sh --target=local` to
> fetch Go modules straight from their VCS origins instead of through the proxy.

### Option B — AWS EKS (Terraform + ECR)

```bash
./deploy.sh --target=eks
```

Runs `terraform apply` (VPC + EKS + ECR + Cognito), builds and pushes images to ECR, installs the Helm chart wired to Cognito OIDC, and runs the smoke test. See [terraform/aws-eks/README.md](https://github.com/agenttier/agenttier/tree/main/terraform/aws-eks) for variables and cost estimates.

### Option C — Install from a published release

If you want to install a released version without building from source:

```bash
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update
helm install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace
```

Images pull anonymously from `ghcr.io/agenttier/*`. CRDs and the six bundled templates (`general-coding`, `claude-code-bedrock`, `openclaw-bedrock`, `strands-bedrock`, `langgraph-agent`, `rl-rollout`) are installed automatically.

## 2. Verify

```bash
kubectl get pods -n agenttier
# agenttier-controller-xxx   1/1   Running
# agenttier-router-xxx       1/1   Running
# agenttier-webui-xxx        1/1   Running

kubectl get crd | grep agenttier
# clustersandboxtemplates.agenttier.io
# sandboxes.agenttier.io
# sandboxtemplates.agenttier.io

kubectl get clustersandboxtemplates
# NAME                    AGE
# general-coding          10s
# claude-code-bedrock     10s
```

## 3. Create a sandbox

```bash
kubectl apply -f - <<'EOF'
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata:
  name: my-first-sandbox
  namespace: default
spec:
  templateRef:
    name: general-coding
    kind: ClusterSandboxTemplate
EOF
```

Watch it start:

```bash
kubectl get sandbox my-first-sandbox -w
# NAME                STATUS     TEMPLATE         AGE
# my-first-sandbox    Creating   general-coding   5s
# my-first-sandbox    Running    general-coding   8s
```

Cold start is ~10 seconds on most clusters. Enable the warm pool (see [Installation](installation.md#warm-pool)) to get sub-second creation.

## 4. Open a terminal

Port-forward the Web UI and open a browser:

```bash
kubectl port-forward -n agenttier svc/agenttier-webui 8080:80
# Open http://localhost:8080
```

Click **Open Terminal** on the sandbox card. You get a full PTY with resize, ANSI colors, and 30-second reconnection on network blips.

There is no CLI equivalent to an interactive terminal — the CLI (Python distribution) runs one-shot commands instead:

```bash
agenttier sandbox exec my-first-sandbox -- bash -lc 'echo hello'
```

See the [CLI guide](cli.md) for install instructions.

## 5. Run commands programmatically

Install the Python SDK:

```bash
pip install agenttier
```

The SDK talks to the Router, not the Web UI, so port-forward that service instead:

```bash
kubectl port-forward -n agenttier svc/agenttier-router 8081:8080
```

```python
from agenttier import AgentTierClient

with AgentTierClient(api_url="http://localhost:8081") as client:
    sandbox = client.get_sandbox("my-first-sandbox")
    result = sandbox.exec("python -c 'print(2+2)'")
    print(result.stdout)  # "4\n"
```

See the [SDK guide](sdk.md) for the full API surface.

## 6. Stop, resume, delete

```bash
# Stop: deletes the Pod, preserves the PVC + files.
kubectl annotate sandbox my-first-sandbox agenttier.io/action=stop

# Resume later — re-attaches the same PVC to a new Pod. ~2 seconds.
kubectl annotate --overwrite sandbox my-first-sandbox agenttier.io/action=resume

# Delete: permanently removes the sandbox and its workspace.
kubectl delete sandbox my-first-sandbox
```

The Web UI exposes the same actions as one-click buttons on the sandbox card.

## Next

- [Tutorials](tutorials/index.md) — hands-on walkthroughs for the Web UI, Python SDK, code mode, and agent mode.
- [Installation](installation.md) — production Helm values, OIDC, warm pool, ingress.
- [Templates](templates.md) — author custom agent blueprints.
- [Governance](governance.md) — cluster and per-namespace policy limits.
- [SDK](sdk.md) — sync and async Python clients.
- [CLI](cli.md) — terminal-native management.
- [Port forwarding](port-forwarding.md) — expose container ports to users.
