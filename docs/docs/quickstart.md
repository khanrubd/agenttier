# Quickstart

Get from zero to a running sandbox in under ten minutes.

## Prerequisites

- Kubernetes **1.27+** (EKS, GKE, AKS, kind, or any CNI/CSI-capable cluster)
- **Helm 3.x** and **kubectl** configured for that cluster
- CNI with NetworkPolicy support (Calico, Cilium, AWS VPC CNI with NetworkPolicy enabled)
- A CSI storage driver (EBS CSI, PD CSI, Azure Disk CSI, or any RWO-capable CSI)

If you just want to see it work, a local `kind` cluster with the default CNI + local-path-provisioner is enough.

## 1. Install AgentTier

```bash
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update
helm install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace
```

That's it. Images pull anonymously from `ghcr.io/agenttier/*`. CRDs and the two reference templates (`general-coding`, `claude-code-bedrock`) are installed automatically.

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

If you prefer the CLI:

```bash
agenttier sandbox terminal my-first-sandbox
```

See the [CLI guide](cli.md) for install instructions.

## 5. Run commands programmatically

Install the Python SDK:

```bash
pip install agenttier
```

```python
from agenttier import AgentTierClient

with AgentTierClient(api_url="http://localhost:8080") as client:
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
