# Strands Agents on AWS Bedrock reference image

Reference sandbox image for the [Strands Agents SDK](https://strandsagents.com/)
preconfigured to talk to **AWS Bedrock** via IRSA-injected credentials.

Strands is AWS's model-driven Python SDK for building AI agents. By
default it talks to Bedrock and uses the AWS SDK default credential
chain — so on an EKS sandbox with IRSA wired up, it Just Works with
zero runtime configuration.

## Quick start

Use the `strands-bedrock` ClusterSandboxTemplate that ships with the
Helm chart:

```bash
agenttier sandbox create my-strands --template strands-bedrock
agenttier sandbox terminal my-strands
# inside the pod:
python /opt/agenttier/examples/agent.py
# or write your own:
cat > /workspace/myagent.py <<'EOF'
from strands import Agent
agent = Agent()
print(agent("Explain Kubernetes operators in one sentence."))
EOF
python /workspace/myagent.py
```

Or invoke headless via `/invoke`:

```bash
agenttier sandbox configure my-strands \
  --file agent.py=images/strands-bedrock/agent.py \
  --entrypoint "python /workspace/agent.py"
agenttier sandbox invoke my-strands --prompt "Say hi in three words"
```

## Bedrock auth — how it works

Strands authenticates via the **AWS SDK default credential chain**.
AgentTier wires that up two ways:

1. **IRSA (recommended).** Annotate the namespace's `default`
   ServiceAccount (or per-template SA) with
   `eks.amazonaws.com/role-arn=arn:aws:iam::ACCOUNT:role/ROLE`. EKS
   injects `AWS_ROLE_ARN` and `AWS_WEB_IDENTITY_TOKEN_FILE` into the
   container, and boto3 (which Strands uses under the hood) exchanges
   that for a session token at first call.
2. **Static keys via Kubernetes Secret.** Mount a Secret with
   `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` (and optionally
   `AWS_SESSION_TOKEN`) using the `credentials:` block of the Sandbox
   spec. Useful for clusters without OIDC or for cross-account access.

### Required IAM permissions on the IRSA role

| Action | Why |
| --- | --- |
| `bedrock:InvokeModel` | Synchronous model calls |
| `bedrock:InvokeModelWithResponseStream` | Streaming Converse API (default in Strands) |
| `bedrock:ListFoundationModels` | (Optional) model discovery |
| `bedrock:ListInferenceProfiles` | (Optional) cross-region inference profiles |

The AWS-managed policy `AmazonBedrockFullAccess` grants all of these.

## What's preinstalled

| Package                | Version  |
| ---------------------- | -------- |
| Python                 | 3.11     |
| `strands-agents`       | 1.41.0   |
| `strands-agents-tools` | 0.6.0    |
| `boto3` / `botocore`   | 1.43.16  |
| AWS CLI v2             | latest   |
| `git`, `tmux`, `jq`    | (stable) |

## Picking a different model

Strands defaults to Bedrock Claude Sonnet. To pick a specific model:

```python
from strands import Agent
from strands.models.bedrock import BedrockModel

model = BedrockModel(
    model_id="us.anthropic.claude-sonnet-4-5-20250929-v1:0",
    region_name="us-east-1",
)
agent = Agent(model=model)
print(agent("Hello"))
```

Override the region via the SandboxTemplate `env:` block (the image
default is `us-east-1`).

## Building locally

```bash
docker build -t agenttier/sandbox-strands-bedrock:dev -f images/strands-bedrock/Dockerfile .
```

CI builds linux/amd64 + linux/arm64 multi-arch on every `v*` tag and
publishes to `ghcr.io/agenttier/sandbox-strands-bedrock:vX.Y.Z`
(cosign-signed, SBOM-attached, anonymous-pullable).
