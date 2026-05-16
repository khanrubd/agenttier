# Agent mode

Two kinds of sandbox live inside AgentTier:

- **`mode: code`** (default) — interactive sandboxes for humans and IDEs. Driven via the terminal, file API, port forwards, exec API. Today's everything.
- **`mode: agent`** — sandboxes that run a configured entrypoint on demand. Driven via `POST /configure` (one-shot install) and `POST /invoke` (Server-Sent Events streaming runner).

Same Sandbox CRD, same Pod, same PVC, same NetworkPolicy, same governance, same warm pool. Mode is a single field on the spec — additive, defaults to `code`, all existing CRs keep working unchanged.

## When to use which mode

| Use code mode when... | Use agent mode when... |
| --- | --- |
| A human needs a terminal to debug, edit, or run ad-hoc commands. | An LLM, framework, or harness needs to drive code over an API. |
| You want classic developer flow — open editor, run tests, iterate. | You want every call to start fresh, or you want streaming output back to a controller. |
| You've been using AgentTier already; nothing changes. | You want governance, audit, and per-call concurrency caps for agent invocations. |

Agent mode does not replace code mode. Most clusters will run both side by side.

## The two-step lifecycle

```
┌──────────┐       ┌──────────────┐       ┌──────────────┐
│  Create  │──────▶│  Configure   │──────▶│  Invoke      │
│ sandbox  │       │ (once)       │       │ (per call)   │
└──────────┘       └──────────────┘       └──────────────┘
```

1. **Create** the sandbox — same `kubectl apply` / SDK / CLI / Web UI flow as a code-mode sandbox, with `mode: agent` on the spec.
2. **Configure** it once: upload your agent code, run an install command (e.g. `pip install -r requirements.txt`), set the entrypoint. Idempotent — re-running with the same files + install command short-circuits.
3. **Invoke** any time after that. AgentTier runs the entrypoint inside the sandbox, pipes stdout/stderr/exit back to the caller as a streaming SSE response.

The sandbox stays warm between invokes — same Pod, same PVC, same network policy. Memory you write under `/workspace` survives invokes, stop/resume cycles, and crashes the same way it survives terminal sessions in code mode.

## What AgentTier owns vs what your agent owns

AgentTier deliberately does **not** run the agent loop. The framework or harness inside the sandbox owns: the loop, model providers, tool dispatch, MCP, memory access, prompt assembly, retry policy, context truncation. AgentTier owns: lifecycle, auth, secrets injection, network policy, streaming transport, audit, OTel, billing.

This split keeps AgentTier as infrastructure rather than a competing agent framework. Bring your own LangGraph, Strands, AutoGen, CrewAI, OpenHands, OpenClaw, or hand-rolled loop — point AgentTier at the entrypoint and call `/invoke`.

## Quickstart — curl

```bash
# Create the sandbox using a mode: agent template
curl -X POST $AGENTTIER_API_URL/api/v1/sandboxes \
  -H "X-API-Key: $AGENTTIER_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "demo-agent", "templateRef": {"name": "langgraph-agent", "kind": "ClusterSandboxTemplate"}}'

# Wait for it to reach Running (kubectl get sandbox demo-agent shows status)

# Configure it: upload an agent.py + set the entrypoint
curl -X POST $AGENTTIER_API_URL/api/v1/sandboxes/demo-agent/configure \
  -H "X-API-Key: $AGENTTIER_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: text/event-stream" \
  -d '{
    "files": [{"path": "/workspace/agent.py", "content": "import sys\nprint(f\"hello {sys.stdin.read()}\")\n"}],
    "entrypoint": ["python", "/workspace/agent.py"]
  }'
# event: result
# data: {"installCommandHash":"...","installExitCode":0,...}

# Invoke it
curl -X POST $AGENTTIER_API_URL/api/v1/sandboxes/demo-agent/invoke \
  -H "X-API-Key: $AGENTTIER_API_KEY" \
  -H "Accept: text/event-stream" \
  -d 'world'
# event: start
# data: {"invokeId":"inv-...","startedAt":...}
# event: log
# data: {"stream":"stdout","data":"hello world"}
# event: exit
# data: {"invokeId":"inv-...","exitCode":0,"durationMs":42,"reason":"completed"}
```

## Quickstart — Python SDK

```python
from agenttier import AgentTierClient

with AgentTierClient(api_url="https://agenttier.company.com") as client:
    sb = client.create_sandbox(template="langgraph-agent", name="demo-agent")
    sb.wait_until_running()

    sb.agent.configure(
        files=[("/workspace/agent.py", "./agent.py")],
        install_command=["pip", "install", "-r", "/workspace/requirements.txt"],
        entrypoint=["python", "/workspace/agent.py"],
    )

    result = sb.agent.invoke({"prompt": "summarize this PR"})
    print(result.stdout)
```

See [sdk.md#agent-mode](sdk.md#agent-mode) for the full surface (sync, async, streaming, cancel).

## Quickstart — CLI

```bash
agenttier configure demo-agent \
  --file /workspace/agent.py=./agent.py \
  --install "pip install -r /workspace/requirements.txt" \
  --entrypoint "python /workspace/agent.py"

agenttier invoke demo-agent --prompt "summarize this PR"
```

The CLI exits with the same exit code as the entrypoint, so `agenttier invoke ... && echo ok` composes naturally in shell pipelines. Full flag reference at [cli.md#agent-mode-phase-10](cli.md#agent-mode-phase-10).

## What the Router enforces

When you call `/configure` or `/invoke`, the Router applies (in order):

- **Authentication** — same OIDC / API key middleware as every other endpoint. Anonymous calls get 401.
- **Mode check** — `/configure` and `/invoke` only work on `mode: agent` sandboxes. Code-mode sandboxes get a 400 with a clear message pointing you at the file-transfer + exec APIs.
- **Phase check** — sandbox must be `Running`. Stopped / creating / error sandboxes get 409.
- **Configure prerequisite** — `/invoke` requires `/configure` to have set an entrypoint at least once. Without it: 424 Failed Dependency.
- **Concurrency cap** — when `agent.maxConcurrentInvokes` is set on the template, over-cap requests get 429 with `Retry-After: 5` and a structured `concurrency_exceeded` body.
- **Governance** — namespace policies can cap `maxAgentSandboxes`, restrict `allowedAgentImages`, and clamp `maxConcurrentInvokesPerSandbox`. See [governance.md](governance.md#agent-mode-policies).

## What the Router emits

Every `/configure` and `/invoke` call:

- **Server-Sent Events** stream of `start` / `log` / `exit` (or `result` for configure) and optional `error` events. Keepalive comments every 15s.
- **OTel span** named `agenttier.invoke` or `agenttier.configure` with bounded attributes (template, actor, exit code, duration, bytes_stdout, bytes_stderr) — no per-user-ID labels per project policy.
- **Prometheus metrics** — `agenttier_invoke_requests_total`, `agenttier_invoke_duration_seconds`, `agenttier_invoke_throttled_total`, plus configure equivalents. Labels are `{template, outcome}`.
- **Kubernetes event** on the Sandbox CR. Visible via `kubectl describe sandbox <name>` and the existing `/api/v1/audit/events` endpoint.

Argv and stdin payloads are deliberately not recorded — they may contain secrets. A future Helm flag will toggle that on for development clusters.

## Memory, model providers, secrets

- **Memory** is your call. AgentTier ships an opt-in [mem0 sidecar](agent-memory.md), but bring-your-own (PVC-local SQLite, AgentCore Memory, Pinecone, Postgres + pgvector, OpenSearch) is fully supported.
- **Model providers** are dialed directly from your agent code. AgentTier injects credentials via the standard `credentials` block (Kubernetes Secret references) and supports IRSA on EKS for AWS-native flows like Bedrock.
- **Secrets** never appear in `/configure` payloads. Reference them by name on the Sandbox spec; the controller injects them as env vars or file mounts. See [templates.md](templates.md#credentials).

## Limits and trade-offs

- **CPU only.** Phase 10 explicitly does not add GPU support. Resource overrides at `/configure` time are also deferred. Both are tracked for follow-up releases.
- **Single Router replica for cancel routing.** The in-process invoke registry means `/invoke/cancel` only works against the Router instance that started the invoke. Multi-replica routers (rare today) would need a sticky-session ALB or a real registry; not blocking for v0.3.5.
- **No payload audit by default.** Argv and stdin are intentionally excluded from audit logs to avoid recording secrets. A Helm-flagged `audit.includeInvokePayloads=true` toggle is planned but not in v0.3.5.
- **One reference image.** v0.3.5 ships `sandbox-langgraph` only. Strands+Bedrock, OpenHands, OpenClaw, and an RL-rollout image are deferred to a future release.

## See also

- [agent-memory.md](agent-memory.md) — three memory patterns (PVC-local, mem0 sidecar, external services)
- [sdk.md#agent-mode](sdk.md#agent-mode) — full Python SDK reference for agent mode
- [cli.md#agent-mode-phase-10](cli.md#agent-mode-phase-10) — CLI reference
- [templates.md](templates.md) — full template spec including the `agent` block
- [governance.md](governance.md) — policy fields including the agent-specific ones
