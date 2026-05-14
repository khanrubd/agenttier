# Tutorial: Agent mode in depth

Agent mode turns AgentTier into a runtime for AI agents — both turnkey harnesses and code that uses agent frameworks. You configure a sandbox once with your agent code, then call `/invoke` to run it on demand. Output streams back as Server-Sent Events.

By the end of this tutorial you will have a working LangGraph agent running on the live cluster, invoked from the SDK, the CLI, and the Web UI.

**Time:** ~30 minutes
**Prerequisites:** AgentTier installed; the `langgraph-agent` template visible in `kubectl get clustersandboxtemplates`.

## 1. The mental model

Agent mode adds two endpoints to a sandbox:

- `POST /api/v1/sandboxes/{id}/configure` — uploads files into the PVC and runs a one-shot install. Idempotent.
- `POST /api/v1/sandboxes/{id}/invoke` — runs the configured entrypoint, pipes the request body to stdin, streams stdout / stderr / exit as SSE events.

AgentTier does **not** run the agent loop, manage models, or own tool dispatch. Your code (or the framework you pick) does. AgentTier owns lifecycle, auth, secrets injection, network policy, streaming transport, audit, OTel, and governance.

A typical agent sandbox config is:

- `entrypoint`: `["python", "/workspace/agent.py"]` — runs every invoke.
- `installCommand`: `["pip", "install", "-r", "/workspace/requirements.txt"]` — runs once at configure time.
- `workingDir`: `/workspace`.
- `env`: API keys, model endpoints, `MEM0_BASE_URL`, etc.
- `maxConcurrentInvokes`: optional cap on parallel invokes per sandbox.
- `defaultInvokeTimeout`: per-invoke wall-clock cap.

## 2. Create an agent-mode sandbox

```bash
kubectl apply -f - <<'EOF'
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata:
  name: agent-tutorial
  namespace: default
spec:
  templateRef:
    name: langgraph-agent
    kind: ClusterSandboxTemplate
EOF

kubectl wait --for=jsonpath='{.status.phase}'=Running \
  sandbox/agent-tutorial --timeout=180s
```

The `langgraph-agent` template sets `mode: agent` and points at `ghcr.io/agenttier/sandbox-langgraph:v0.3.0`, which preinstalls Python 3.11, LangGraph, LangChain, httpx, and the `mem0` client.

## 3. Configure with your agent code

The simplest agent code, copied from the bundled example:

```python
# /tmp/agent.py
import json
import sys

def main():
    payload = sys.stdin.read()
    try:
        data = json.loads(payload) if payload.strip() else {}
    except json.JSONDecodeError:
        data = {"prompt": payload}

    prompt = data.get("prompt", "hello")
    # In a real agent, this is where LangGraph / LangChain takes over.
    # For the tutorial we just echo back deterministically.
    print(json.dumps({"reply": f"You said: {prompt}", "echo": data}))

if __name__ == "__main__":
    main()
```

Write the file:

```bash
cat > /tmp/agent.py <<'EOF'
import json
import sys

def main():
    payload = sys.stdin.read()
    try:
        data = json.loads(payload) if payload.strip() else {}
    except json.JSONDecodeError:
        data = {"prompt": payload}
    prompt = data.get("prompt", "hello")
    print(json.dumps({"reply": f"You said: {prompt}", "echo": data}))

if __name__ == "__main__":
    main()
EOF
```

Now run `configure` from the CLI:

```bash
agenttier configure agent-tutorial \
  --file agent.py=/tmp/agent.py \
  --entrypoint "python /workspace/agent.py"
```

(No `--install` needed — the langgraph image already has Python and stdlib.)

Status afterward:

```bash
kubectl get sandbox agent-tutorial -o jsonpath='{.status.agentConfigure}'
# {"lastConfiguredAt":"...", "installCommandHash":"...", "entrypoint":[...], "installExitCode":0}
```

The `installCommandHash` makes `configure` idempotent: re-running with the same install command is a no-op.

## 4. Invoke from the CLI

```bash
agenttier invoke agent-tutorial --prompt "hello world"
# {"reply": "You said: hello world", "echo": {"prompt": "hello world"}}
# (exit 0)
```

The CLI streams stdout / stderr to your terminal in real time and exits with the entrypoint's exit code. Pipe-friendly:

```bash
agenttier invoke agent-tutorial --input @/path/to/body.json | jq .reply
```

## 5. Invoke from the SDK

```python
from agenttier import AgentTierClient

with AgentTierClient(api_url="http://localhost:8081") as client:
    sbx = client.get_sandbox("agent-tutorial")

    # one-shot
    result = sbx.agent.invoke({"prompt": "hello from python"})
    print(result.exit_code)        # 0
    print(result.stdout)           # JSON
    print(result.duration_ms)      # 200ish

    # streaming
    for event in sbx.agent.invoke_stream({"prompt": "stream please"}):
        if event.stream == "stdout":
            print(event.data, end="")
        elif event.stream == "stderr":
            print(f"[STDERR] {event.data}", end="")
        elif event.stream == "exit":
            print(f"\nexit={event.data['exit_code']}")
```

For long-running invokes, `invoke_stream` lets you render output incrementally — exactly what the Web UI does.

## 6. Invoke from the Web UI

Open the Web UI, expand **Advanced** on the agent-tutorial card, click the **Agent** tab. Three panels:

- **Configure** — re-run installs, edit entrypoint, see `lastConfiguredAt` and the install log.
- **Invoke** — paste a prompt or JSON body, click **Invoke**, watch stdout/stderr stream live. The **Cancel** button SIGTERMs the entrypoint mid-flight.
- **Recent invokes** — last few invokes from this session with status, duration, and (if OTel is wired) a trace link.

Try canceling: invoke a long-running script and click **Cancel** before it finishes. The exit event reports `exit_code: -1` (SIGTERM) and the recent-invokes list shows it as `cancelled`.

## 7. Cancel an in-flight invoke programmatically

The first SSE event always carries an `invoke_id`. Save it and POST to `/invoke/cancel`:

```python
import threading
import time

invoke_id_holder = {}

def consume():
    for event in sbx.agent.invoke_stream({"prompt": "..."}):
        if event.stream == "start":
            invoke_id_holder["id"] = event.data["invoke_id"]
        # ...

t = threading.Thread(target=consume)
t.start()

time.sleep(2)
sbx.agent.invoke_cancel(invoke_id_holder["id"])
t.join()
```

For most cases, prefer simply closing the SSE stream — the Router treats client disconnect as cancel and SIGTERMs the entrypoint automatically.

## 8. Concurrency caps

Set `maxConcurrentInvokes` to throttle. From the SDK's perspective, over-cap requests get `HTTP 429` with `Retry-After: 5` and a structured body. Patch the spec:

```bash
kubectl patch sandbox agent-tutorial --type=merge -p \
  '{"spec":{"harness":{"agent":{"maxConcurrentInvokes":2}}}}'
```

Now any third concurrent invoke is rejected:

```python
from agenttier.exceptions import HTTPError

try:
    sbx.agent.invoke({"prompt": "..."})
except HTTPError as e:
    if e.status_code == 429:
        print(f"throttled — try again in {e.retry_after}s")
```

Cluster-wide ceilings are enforced by governance — see [Governance](../governance.md).

## 9. Optional `mem0` memory sidecar

For a quick local memory backend, enable the sidecar at install time:

```bash
helm upgrade --install agenttier agenttier/agenttier \
  -n agenttier --reuse-values \
  --set optional.agentMemorySidecar.enabled=true
```

Once enabled, AgentTier injects a `mem0` container into every `mode: agent` Pod, listens on `127.0.0.1:11434`, and sets `MEM0_BASE_URL` automatically. Your agent code can:

```python
from mem0 import Memory
m = Memory()
m.add("user prefers dark mode", user_id="alice")
# ...later invoke...
print(m.search("preferences", user_id="alice"))
```

Memory persists in `/workspace/.agenttier/memory/` so it survives stop / resume.

For production, you typically prefer an external service (AgentCore Memory, Pinecone, Postgres + pgvector, OpenSearch). See [Agent memory](../agent-memory.md) for those patterns.

> The mem0 sidecar is opt-in and disabled by default because the upstream image is currently arm64-only.

## 10. Audit and observability

Every invoke emits:

- An OTel span `agenttier.invoke` with sandbox / template / actor / invoke_id / duration / exit_code attributes.
- A line in the audit log via `GET /api/v1/audit/events`.
- Prometheus metrics: `agenttier_invoke_requests_total`, `agenttier_invoke_duration_seconds`, `agenttier_invoke_throttled_total`.

`/configure` emits parallel `agenttier.configure` spans and metrics.

By default argv and stdin are **not** recorded — they may contain secrets. Operators can flip `audit.includeInvokePayloads=true` for development clusters.

## 11. Bring your own framework

The `langgraph-agent` template is one example. The contract is:

- Image must be `mode: agent`-compatible: it should accept the entrypoint command and read stdin if applicable.
- `installCommand` runs once and produces a usable `/workspace`.
- `entrypoint` is invoked on every `/invoke`.

Any framework that fits — Strands Agents, AutoGen, CrewAI, Pydantic AI, OpenAI Agents SDK — drops in. Build your own image (or extend `sandbox-langgraph`), reference it in a SandboxTemplate, configure, invoke.

For fully turnkey harnesses (OpenHands, OpenClaw), the same flow applies: the harness owns the loop, AgentTier owns the runtime.

## 12. Clean up

```bash
agenttier sandbox delete agent-tutorial
```

## What you just learned

- Agent mode is `mode: agent` plus a `HarnessSpec.agent` block — entrypoint, install, env, concurrency cap.
- Configure once, invoke many. Both endpoints stream SSE.
- Cancel via `/invoke/cancel` or by closing the SSE stream.
- Concurrency capped per-sandbox by `maxConcurrentInvokes`, cluster-wide by governance.
- Optional `mem0` sidecar for local memory; external services preferred for production.
- AgentTier does not own the agent loop or model providers — your code or framework does.

## What to read next

- [Agent mode reference](../agent-mode.md) — every endpoint and field.
- [Agent memory patterns](../agent-memory.md) — PVC-local, sidecar, external services, NetworkPolicy egress snippets.
- [Governance](../governance.md) — cluster ceilings on agent caps and image registries.
- [SDK reference](../sdk.md) — full `sandbox.agent` API surface.
