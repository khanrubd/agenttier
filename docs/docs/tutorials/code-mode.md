# Tutorial: Code mode in depth

Code mode is the default sandbox flavor: a long-lived dev environment for humans (or for agents that drive the terminal directly, like Claude Code). This tutorial walks through the production-grade patterns: persistent workspaces, IRSA-backed AWS access, allowed egress domains, and the Claude Code reference image.

**Time:** ~20 minutes
**Prerequisites:** AgentTier installed; kubectl + the `agenttier` CLI on your laptop. Some steps assume an EKS cluster with IRSA configured for the AWS-specific section — skip those on non-EKS clusters.

## 1. The mental model

Code mode is "give me a Pod with a writable workspace and a way in." AgentTier handles:

- Pod creation from a template
- Persistent `/workspace` PVC
- Default-deny egress NetworkPolicy
- Per-sandbox ServiceAccount (for IRSA, GKE workload identity, etc.)
- WebSocket terminal, file API, port forwarding, exec API

Anything you would normally do on a long-lived dev VM — clone a repo, run a build, start a server, edit files — works inside the sandbox. The PVC persists across stop / resume so your work survives.

## 2. Create a code-mode sandbox

```bash
kubectl apply -f - <<'EOF'
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata:
  name: dev-tutorial
  namespace: default
spec:
  templateRef:
    name: general-coding
    kind: ClusterSandboxTemplate
EOF

kubectl wait --for=jsonpath='{.status.phase}'=Running sandbox/dev-tutorial --timeout=120s
```

`mode` defaults to `code`, so you do not need to set it. The Pod, PVC, NetworkPolicy, and ServiceAccount are all created automatically.

## 3. Open a terminal from the CLI

```bash
agenttier sandbox terminal dev-tutorial
```

You get the same PTY the Web UI gives you, attached to your terminal emulator. `Ctrl+D` exits. Reconnect anytime with the same command.

## 4. Use it like a dev VM

```bash
git clone https://github.com/your-org/repo.git /workspace/repo
cd /workspace/repo
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
pytest
```

Everything under `/workspace` lives on the PVC. `/tmp` is ephemeral on the Pod and clears on resume.

The general-coding image preinstalls Python 3.11, Node 20, Go 1.25, build-essential, jq, and curl. For other languages, build a custom image with the additions you need and reference it in a SandboxTemplate.

## 5. Persistence across stop / resume

Stop the sandbox:

```bash
agenttier sandbox stop dev-tutorial
kubectl get sandbox dev-tutorial -o jsonpath='{.status.phase}'
# Stopped
```

The Pod is gone. The PVC is preserved. Cost drops to whatever your storage provisioner charges per GiB-month for an idle volume.

Resume:

```bash
agenttier sandbox resume dev-tutorial
agenttier sandbox terminal dev-tutorial
ls /workspace/repo  # still there
```

Resume reuses the existing PVC, so you skip both the Pod scheduling delay and any image pull (the image is cached on the node from last time).

## 6. Allowed egress domains

The default NetworkPolicy is `deny-all` for egress except DNS. Most real workflows need outbound HTTPS — `pip install`, `npm install`, `git push`, model APIs.

Edit the template (`Templates → general-coding`) and add:

```yaml
spec:
  network:
    allowedDomains:
      - github.com
      - pypi.org
      - files.pythonhosted.org
      - registry.npmjs.org
```

The controller turns this into a NetworkPolicy with a `to.ipBlock` per resolved domain. Domains are re-resolved every 5 minutes; flaky resolutions surface in the Pod's events.

For tutorials, you can also override at create time:

```yaml
spec:
  network:
    allowedDomains: ["github.com", "pypi.org"]
```

## 7. Inject credentials via IRSA (EKS)

Pre-req: a working IAM role with the AWS permissions you want, and an OIDC trust relationship for your EKS cluster. Standard IRSA setup, see [AWS docs](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html).

Annotate the per-sandbox ServiceAccount via the template:

```yaml
spec:
  serviceAccount:
    annotations:
      eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/AgentTierSandboxRole
```

Now from inside the sandbox:

```bash
aws sts get-caller-identity
# {"Account": "123456789012", "Arn": "arn:aws:sts::.../AgentTierSandboxRole/..."}
```

The AWS SDKs auto-discover the IRSA token. No env vars, no static keys. The sandbox-claude-code reference image uses this exact mechanism to talk to Bedrock.

For non-EKS clusters, similar mechanisms exist:

- **GKE**: workload identity (annotate the SA with `iam.gke.io/gcp-service-account`).
- **AKS**: Azure workload identity (annotate with `azure.workload.identity/client-id`).
- **Generic**: mount a Kubernetes Secret with the credentials as env vars or a file.

## 8. Claude Code on Bedrock

The `claude-code-bedrock` template ships a turnkey AI coding agent. Create one:

```bash
kubectl apply -f - <<'EOF'
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata:
  name: claude-tutorial
spec:
  templateRef:
    name: claude-code-bedrock
    kind: ClusterSandboxTemplate
EOF

kubectl wait --for=jsonpath='{.status.phase}'=Running sandbox/claude-tutorial --timeout=180s
agenttier sandbox terminal claude-tutorial
```

Inside the sandbox:

```bash
claude --version             # 2.x
claude -p "what is 2+2"      # routes through Bedrock
cd /workspace
git clone https://github.com/your-org/repo.git
cd repo
claude                       # interactive coding session
```

The image preinstalls `@anthropic-ai/claude-code`, sets the env vars Bedrock expects, and binds AWS credentials via IRSA. The default model is Claude in Bedrock; override via env in the template.

## 9. Resource limits and timeouts

Override defaults at create time:

```yaml
spec:
  resources:
    cpu: "2"
    memory: "8Gi"
    ephemeralStorage: "20Gi"
  storage:
    size: "50Gi"
  idleTimeout: "8h"
  maxRuntime: "24h"
```

Governance policies (cluster + namespace) clamp these. If `maxCpu: "4"` is set on the namespace, a sandbox requesting `cpu: "8"` is rejected with a `policy_violation` error.

`idleTimeout` triggers an auto-stop after the user disconnects from the terminal for that long. `maxRuntime` is a hard cap; the sandbox auto-stops at the deadline regardless of activity. Both can be overridden per sandbox up to the governance ceiling.

## 10. Inspect Pod-level details

The CRD status surfaces the most useful fields:

```bash
kubectl get sandbox dev-tutorial -o yaml | yq '.status'
# phase, pod, podIP, lastActivityTimestamp, startupDurationMs,
# resolvedTemplate (the merged spec), forwardedPorts, conditions
```

For a deep dive, jump to the Pod:

```bash
POD=$(kubectl get sandbox dev-tutorial -o jsonpath='{.status.pod}')
kubectl describe pod -n default $POD
kubectl logs -n default $POD --previous   # crashed Pod logs
```

The controller emits Kubernetes events on every transition, so `kubectl describe sandbox` is the fastest way to debug.

## 11. Clean up

```bash
agenttier sandbox delete dev-tutorial
agenttier sandbox delete claude-tutorial
```

## What you just learned

- Code mode is the default; nothing extra is needed.
- PVC persists across stop / resume; delete is permanent.
- `network.allowedDomains` opens egress without giving up default-deny.
- IRSA / workload identity inject AWS / GCP / Azure credentials into the sandbox without baking secrets into images.
- The `claude-code-bedrock` template is a one-line setup for an AI coding agent on AWS.

## What to read next

- [Agent mode in depth](agent-mode-tutorial.md) — when you want `/configure` + `/invoke` instead of a terminal.
- [Templates reference](../templates.md) — every field on `SandboxTemplate`.
- [Governance](../governance.md) — clamping resources, runtime, and image registries.
