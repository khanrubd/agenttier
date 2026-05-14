# LangGraph reference image

Phase 10 reference image for `mode: agent` sandboxes. Bundles Python 3.11 + LangGraph + LangChain + httpx + the `mem0` client preinstalled so callers can drop an `agent.py` into `/workspace/` and call `/invoke` without first running pip via `/configure`.

## Quick start

Use the `langgraph-agent` template (shipped in `helm/agenttier/templates/default-templates.yaml`):

```bash
agenttier create my-agent --template langgraph-agent
agenttier configure my-agent \
  --file agent.py=./agent.py \
  --entrypoint "python /workspace/agent.py"
agenttier invoke my-agent --prompt "hello world"
```

If you don't want to write your own agent yet, copy the bundled example:

```bash
agenttier exec my-agent -- cp /opt/agenttier/examples/agent.py /workspace/
agenttier configure my-agent --entrypoint "python /workspace/agent.py"
agenttier invoke my-agent --prompt "hello"
# echo: hello
```

## What's preinstalled

| Package        | Version  |
| -------------- | -------- |
| langgraph      | 0.6.4    |
| langchain      | 0.3.27   |
| langchain-core | 0.3.78   |
| httpx          | 0.28.1   |
| mem0ai         | 0.1.115  |
| pydantic       | 2.10.4   |

The `mem0ai` client is preinstalled to pair with the optional [mem0 sidecar](../../docs/docs/agent-memory.md). When `optional.agentMemorySidecar.enabled=true` is set in your Helm values, `MEM0_BASE_URL` is wired into the sandbox container's env automatically — your code can `from mem0 import Memory; m = Memory(base_url=os.environ['MEM0_BASE_URL'])`.

## Wiring in your model provider

The bundled `agent.py` is deliberately model-free so it runs without provisioning Bedrock / OpenAI credentials. To call a real model:

```python
# Bedrock via boto3 (use IRSA on EKS)
import boto3
client = boto3.client("bedrock-runtime", region_name="us-east-1")

def agent_node(state):
    resp = client.invoke_model(
        modelId="anthropic.claude-3-5-sonnet-20241022-v2:0",
        body=json.dumps({"messages": [...]}),
    )
    state["output"] = json.loads(resp["body"].read())["content"][0]["text"]
    return state
```

For OpenAI / Anthropic / etc., add the API key as a Kubernetes Secret and reference it from the sandbox spec — see [templates.md](../../docs/docs/templates.md).

## Building locally

```bash
docker build -t agenttier/sandbox-langgraph:dev images/langgraph/
```

CI builds linux/amd64 + linux/arm64 multi-arch on every `v*` tag and publishes to `ghcr.io/agenttier/sandbox-langgraph:vX.Y.Z` (cosign-signed, SBOM-attached, anonymous-pullable).
