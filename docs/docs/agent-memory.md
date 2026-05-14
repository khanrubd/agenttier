# Agent memory

AgentTier deliberately does **not** own a memory subsystem. Agent code running inside a `mode: agent` sandbox decides what to store, where to store it, and when. AgentTier handles the lifecycle (PVC, pod, network policy), the auth (OIDC, API keys), the streaming transport (`/invoke` SSE), and the audit trail. Memory is your call.

This page covers the three patterns we ship guidance for.

## 1. PVC-local (default, zero config)

The simplest pattern. Anything your agent writes under `/workspace` lands on the persistent volume the sandbox already owns. Stop and resume the sandbox, and the data is still there. No extra moving parts, no extra dependencies.

Examples that fit this pattern:

- LangGraph's `SqliteSaver` checkpointer — set its path to `/workspace/.memory/checkpoints.sqlite`.
- Chroma persistent client — set `persist_directory="/workspace/.memory/chroma"`.
- A flat JSONL file that your agent appends to.

```python
# /workspace/agent.py
from pathlib import Path
import json

MEMORY_FILE = Path("/workspace/.memory/turns.jsonl")
MEMORY_FILE.parent.mkdir(parents=True, exist_ok=True)

def remember(turn: dict) -> None:
    with MEMORY_FILE.open("a") as f:
        f.write(json.dumps(turn) + "\n")
```

**Trade-offs:** Free. Survives stop/resume on the same sandbox. Does *not* survive sandbox deletion, and is not shared across sandboxes. Good for per-session memory and short-lived experiments.

## 2. mem0 sidecar (opt-in via Helm flag)

When you want a real memory API but don't want to operate one externally, AgentTier can inject a [mem0](https://mem0.ai) sidecar into every `mode: agent` Pod. The sidecar listens on `127.0.0.1:8000` inside the Pod's network namespace; AgentTier sets `MEM0_BASE_URL=http://localhost:8000` in your sandbox container's environment so framework code reaches it without any network policy changes. Storage lives at `/workspace/.agenttier/memory` on the same workspace PVC, so memory survives stop/resume the same way user code does.

**Platform requirement.** As of late 2025 mem0 only publishes an arm64 image at `docker.io/mem0/mem0-api-server`. Running the sidecar on amd64 nodes (the default for most EKS / GKE setups) requires either an arm64 node group or a custom multi-arch rebuild of the mem0 server. The flag is opt-in and disabled by default.

Enable it in your Helm values (arm64 nodes only):

```yaml
optional:
  agentMemorySidecar:
    enabled: true
    # mem0 only publishes the `latest` tag — pinning by digest gives you
    # a reproducible reference. Update the digest when you want a newer build.
    image: "mem0/mem0-api-server@sha256:2fcf4bb713cfc584d454bf06993cc7e2fe51540695995bd3aa9c7008b7065c75"
```

Then in your agent code:

```python
# /workspace/agent.py
import os
import httpx

MEM0_URL = os.environ["MEM0_BASE_URL"]

def remember(content: str, user_id: str = "default") -> None:
    httpx.post(f"{MEM0_URL}/memories", json={
        "messages": [{"role": "user", "content": content}],
        "user_id": user_id,
    })

def search(query: str, user_id: str = "default") -> list:
    return httpx.get(f"{MEM0_URL}/search", params={"query": query, "user_id": user_id}).json()
```

The sidecar exposes mem0's REST API directly. The `mem0ai` Python client is preinstalled in the `sandbox-langgraph` reference image so users can also `from mem0 import MemoryClient` and pass `host=MEM0_BASE_URL`.

**Trade-offs:** Free local memory store with a real API. Memory is per-sandbox — there's no automatic sharing across sandboxes (each Pod has its own sidecar). Good for dev clusters and single-tenant agents that want a quick win.

## 3. External managed services (bring your own)

For production deployments and any scenario that needs cross-sandbox memory, route to an external service. AgentTier doesn't block this — your agent code makes the outbound call directly. You configure egress (NetworkPolicy, IRSA / Secret credentials) the same way you would for any external dependency.

Common patterns:

- **AWS Bedrock AgentCore Memory** — via boto3 in agent code, IRSA-injected credentials.
- **Pinecone** — `PINECONE_API_KEY` from a Secret reference, vector ops over HTTPS.
- **Postgres + pgvector** — connection string from a Secret, network egress to your Postgres host.
- **OpenSearch / Elasticsearch** — IAM SigV4 if you're on AWS, basic auth otherwise.

### Egress NetworkPolicy snippet

The default AgentTier sandbox NetworkPolicy is `deny-all` egress with DNS allowed. If your default policy is restrictive, opt the agent template into the egress it needs:

```yaml
apiVersion: agenttier.io/v1alpha1
kind: ClusterSandboxTemplate
metadata:
  name: langgraph-with-pinecone
spec:
  mode: agent
  network:
    allowedDomains:
      - "*.pinecone.io"
      - "api.openai.com"
  # ... rest of the template
```

`allowedDomains` requires a DNS-aware CNI (Calico, Cilium). On AWS VPC CNI clusters, fall back to `egressRules` with explicit CIDRs. See [templates.md](templates.md) for the full network spec.

**Trade-offs:** Production-grade durability, scalability, and cross-sandbox memory. Costs money. Requires you to operate the backend (or pay someone else to). Best for shared deployments and teams that already have a vector / KV store.

## Choosing a pattern

| Pattern        | Setup       | Persistence              | Cross-sandbox? | Cost          |
| -------------- | ----------- | ------------------------ | -------------- | ------------- |
| PVC-local      | None        | Per-sandbox, stop-safe   | No             | Free          |
| mem0 sidecar   | Helm flag   | Per-sandbox, stop-safe   | No             | Free          |
| External       | Egress + creds | Anywhere your store reaches | Yes        | Pay-per-use   |

A reasonable progression: start with PVC-local, switch to the mem0 sidecar once you outgrow flat files, move to a managed service once you need durability across sandbox lifecycles or sharing across agents. Your agent code doesn't need to change between (1) and (2) if you build it against the mem0 client from the start — the env var routes the calls.
