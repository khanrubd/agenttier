# OpenClaw reference image

Reference sandbox image for the [OpenClaw CLI](https://github.com/openclaw/openclaw)
preconfigured to talk to **AWS Bedrock** via IRSA-injected credentials.

OpenClaw is an open-source personal AI assistant that runs as a local
gateway and connects to a wide range of model providers. AgentTier
ships it the same way it ships Claude Code: as a turnkey image you can
drop into a `ClusterSandboxTemplate`, scale via warm pods, and govern
with the same policies that already cover the other sandbox types.

## Quick start

Use the `openclaw-bedrock` ClusterSandboxTemplate that ships with the
Helm chart:

```bash
agenttier sandbox create my-openclaw --template openclaw-bedrock
agenttier sandbox terminal my-openclaw
# inside the pod:
openclaw models list                # discover Bedrock models via IRSA
openclaw chat "summarize this repo" # default model: Claude Sonnet 3.5 v2 on Bedrock
```

If your account has a different default region, point Bedrock at it via
the template `env:` block (overrides the image default of `us-east-1`):

```yaml
env:
  - name: AWS_DEFAULT_REGION
    value: "us-west-2"
  - name: AWS_REGION
    value: "us-west-2"
```

## Bedrock auth — how it actually works

OpenClaw's Bedrock provider authenticates via the **AWS SDK default
credential chain**. AgentTier wires that up two ways:

1. **IRSA (recommended).** Set `defaults.openclaw.irsaRoleArn` in your
   Helm values. The chart annotates the per-sandbox ServiceAccount with
   `eks.amazonaws.com/role-arn`, EKS injects `AWS_ROLE_ARN` and
   `AWS_WEB_IDENTITY_TOKEN_FILE` into the container, and the SDK
   exchanges that for a session token at first call.

2. **Static keys via Kubernetes Secret.** Mount a Secret with
   `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` (and optionally
   `AWS_SESSION_TOKEN`) using the `credentials:` block of the Sandbox
   spec. Useful for clusters without OIDC or for cross-account access.

The image already enables `plugins.entries.amazon-bedrock.config.discovery.enabled`
in its baked config (`/etc/openclaw/openclaw.json`). Without that flag
OpenClaw only auto-detects Bedrock when it sees `AWS_PROFILE`,
`AWS_ACCESS_KEY_ID`, or `AWS_BEARER_TOKEN_BEDROCK` — IRSA sets neither,
so the explicit opt-in is required. The runtime auth path still uses
the SDK default chain, so IRSA, env vars, and EC2 instance roles all
work transparently.

### Required IAM permissions on the IRSA role

| Action                                    | Why                                          |
| ----------------------------------------- | -------------------------------------------- |
| `bedrock:InvokeModel`                     | Synchronous model calls                      |
| `bedrock:InvokeModelWithResponseStream`   | Streaming Converse API (default in OpenClaw) |
| `bedrock:ListFoundationModels`            | Auto-discovery of available models           |
| `bedrock:ListInferenceProfiles`           | Cross-region inference profiles              |

The AWS-managed policy `AmazonBedrockFullAccess` grants all four. For
a tighter scope, write a custom policy with just these four actions
and a `Resource: "*"` constraint (Bedrock model ARNs are needed for
fine-grained restrictions; consult Bedrock docs for the exact shape).

## What's preinstalled

| Package                | Version    | Why                                          |
| ---------------------- | ---------- | -------------------------------------------- |
| Node.js                | 22.x       | OpenClaw requires Node ≥ 22.16               |
| `openclaw`             | 2026.6.5   | Pinned, auto-update disabled                 |
| AWS CLI v2             | latest     | `aws sts get-caller-identity` etc.           |
| `git`, `python3`, `tmux`, `jq` | (stable) | Standard coding-sandbox toolkit              |

## Configuring a different default model

The baked config sets the primary model to
`amazon-bedrock/us.anthropic.claude-opus-4-7`. To switch:

```bash
# inside the sandbox
openclaw models list                                       # see what's available
openclaw models set amazon-bedrock/anthropic.claude-3-7-sonnet-20250219-v1:0
```

The change persists across stop/resume because the config seed lives
on the writable PVC at `/workspace/.openclaw/openclaw.json` (seeded
from the image's baked `/etc/openclaw/openclaw.json` on first launch,
never overwritten after that).

## Building locally

```bash
docker build -t agenttier/sandbox-openclaw:dev -f images/openclaw/Dockerfile .
```

CI builds linux/amd64 + linux/arm64 multi-arch on every `v*` tag and
publishes to `ghcr.io/agenttier/sandbox-openclaw:vX.Y.Z` (cosign-signed,
SBOM-attached, anonymous-pullable).
