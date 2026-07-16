# LangGraph agent example

End-to-end runnable example for `mode: agent` sandboxes using the [LangGraph](https://langchain-ai.github.io/langgraph/) framework. Pairs with the bundled `langgraph-agent` template and the `sandbox-langgraph` reference image (built from `images/langgraph/Dockerfile`; published to the configured registry, default `ghcr.io/agenttier/sandbox-langgraph`).

## What's in this directory

| File | Purpose |
| --- | --- |
| `agent.py` | A small LangGraph agent that echoes its input through a graph node. Replace `agent_node()` to wire in a real model provider. |
| `requirements.txt` | Pinned Python deps. The `sandbox-langgraph` image preinstalls these, but listing them lets you `pip install -r` if you swap to a different base image. |

## Try it (CLI)

```bash
# 1. Create the sandbox using the bundled template
agenttier create my-agent --template langgraph-agent

# 2. Configure: upload the agent code + persist the entrypoint. No install
#    command needed — sandbox-langgraph preinstalls everything in
#    requirements.txt.
agenttier configure my-agent \
  --file /workspace/agent.py=./agent.py \
  --entrypoint "python /workspace/agent.py"

# 3. Invoke
agenttier invoke my-agent --prompt "hello world"
# echo: hello world
```

## Try it (Python SDK)

```python
from agenttier import AgentTierClient

with AgentTierClient(api_url="https://agenttier.company.com") as client:
    sb = client.create_sandbox(template="langgraph-agent", name="my-agent")
    sb.wait_until_running()

    sb.agent.configure(
        files=[("/workspace/agent.py", "./agent.py")],
        entrypoint=["python", "/workspace/agent.py"],
    )

    result = sb.agent.invoke({"prompt": "hello world"})
    print(result.stdout)  # echo: hello world
```

## Wiring in a real model

The bundled `agent.py` is intentionally model-free so the example runs without provisioning Bedrock / OpenAI / Anthropic credentials. To call a real model:

```python
# Bedrock via boto3 (use IRSA on EKS — no credentials in the sandbox spec)
import boto3, json

client = boto3.client("bedrock-runtime", region_name="us-east-1")

def agent_node(state):
    resp = client.invoke_model(
        modelId="anthropic.claude-3-5-sonnet-20241022-v2:0",
        body=json.dumps({
            "anthropic_version": "bedrock-2023-05-31",
            "max_tokens": 1024,
            "messages": [{"role": "user", "content": state["input"]}],
        }),
    )
    body = json.loads(resp["body"].read())
    state["output"] = body["content"][0]["text"]
    return state
```

For `sk-`-style API keys, reference a Kubernetes Secret in the template's `credentials` block — see [`docs/docs/templates.md#credentials`](../../docs/docs/templates.md).

## Adding memory

Turn on the optional [mem0 sidecar](../../docs/docs/agent-memory.md) in your Helm values:

```yaml
optional:
  agentMemorySidecar:
    enabled: true
```

The sidecar listens on `127.0.0.1:11434` inside the Pod's network namespace; AgentTier sets `MEM0_BASE_URL` in your sandbox container's env automatically. Use it from agent code:

```python
import os
from mem0 import Memory

m = Memory(base_url=os.environ["MEM0_BASE_URL"])

def agent_node(state):
    relevant = m.search(state["input"], user_id="default")
    # ... use relevant memories in your prompt assembly ...
    m.add(state["output"], user_id="default")
    return state
```

For PVC-local persistence (LangGraph's `SqliteSaver`, Chroma, flat JSONL files), or external services (AgentCore Memory, Pinecone, Postgres + pgvector), see [`docs/docs/agent-memory.md`](../../docs/docs/agent-memory.md).
