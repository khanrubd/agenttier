# Quickstart Guide

Get from zero to a running sandbox with terminal access in under 10 minutes.

## Prerequisites

- Kubernetes 1.27+ cluster (EKS, GKE, AKS, or Kind)
- Helm 3.x installed
- kubectl configured for your cluster
- A CNI that supports NetworkPolicy (Calico, Cilium)
- A CSI storage driver (EBS CSI, PD CSI, or default)

## Step 1: Install AgentTier

```bash
# Add the Helm repository
helm repo add agenttier https://charts.agenttier.io
helm repo update

# Install with default settings (includes MongoDB)
helm install agenttier agenttier/agenttier \
  --namespace agenttier \
  --create-namespace
```

For production, configure OIDC authentication:

```bash
helm install agenttier agenttier/agenttier \
  --namespace agenttier \
  --create-namespace \
  -f values.yaml
```

## Step 2: Verify Installation

```bash
# Check pods are running
kubectl get pods -n agenttier

# Expected output:
# NAME                                    READY   STATUS    RESTARTS   AGE
# agenttier-controller-xxx                1/1     Running   0          30s
# agenttier-router-xxx                    1/1     Running   0          30s
# agenttier-webui-xxx                     1/1     Running   0          30s
# agenttier-mongodb-0                     1/1     Running   0          30s

# Check CRDs installed
kubectl get crd | grep agenttier
# sandboxes.agenttier.io
# sandboxtemplates.agenttier.io
# clustersandboxtemplates.agenttier.io

# Check default templates
kubectl get clustersandboxtemplates
```

## Step 3: Create Your First Sandbox

```bash
kubectl apply -f - <<EOF
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

## Step 4: Watch It Start

```bash
# Watch the sandbox status
kubectl get sandbox my-first-sandbox -w

# Expected progression:
# NAME                STATUS     TEMPLATE         AGE
# my-first-sandbox    Creating   general-coding   5s
# my-first-sandbox    Running    general-coding   8s
```

## Step 5: Open a Terminal

```bash
# Using the CLI
agenttier sandbox exec my-first-sandbox

# Or port-forward the Web UI
kubectl port-forward svc/agenttier-webui -n agenttier 8080:80
# Then open http://localhost:8080 and click "Open Terminal"
```

## Step 6: Work in Your Sandbox

```bash
# You're now inside the sandbox!
$ whoami
sandbox

$ pwd
/workspace

$ node --version
v20.x.x

$ python3 --version
Python 3.11.x

$ git clone https://github.com/your-org/your-repo.git
```

## Step 7: Stop and Resume

```bash
# Stop (preserves all files)
kubectl patch sandbox my-first-sandbox --type=merge -p '{"spec":{"__stop":true}}'
# Or via CLI: agenttier sandbox stop my-first-sandbox

# Resume later
kubectl patch sandbox my-first-sandbox --type=merge -p '{"spec":{"__resume":true}}'
# Or via CLI: agenttier sandbox resume my-first-sandbox

# Your files, packages, and git repos are exactly as you left them!
```

## Step 8: Clean Up

```bash
# Delete the sandbox (permanent — removes all data)
kubectl delete sandbox my-first-sandbox
```

## Next Steps

- [Installation Guide](installation.md) — Full Helm configuration reference
- [Template Authoring](templates.md) — Create custom sandbox templates
- [Python SDK](sdk.md) — Programmatic sandbox management
- [Governance](governance.md) — Set up policies and limits
