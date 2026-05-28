# Tutorial: Python SDK walkthrough

Drive AgentTier from Python: lifecycle, exec, file transfer, port forwarding, and async patterns.

**Time:** ~25 minutes
**Prerequisites:** AgentTier installed and a way to reach the Router HTTP API (port-forward or Ingress URL).

## 1. Install

```bash
pip install agenttier
```

The wheel ships a `py.typed` marker so mypy and pyright pick up types automatically. Python 3.9+ is required.

## 2. Connect

The simplest connection target is a `kubectl port-forward`:

```bash
kubectl port-forward -n agenttier svc/agenttier-router 8081:80
```

Then in Python:

```python
from agenttier import AgentTierClient

client = AgentTierClient(api_url="http://localhost:8081")
print(client.health())  # {"status": "ok", "version": "0.5.0"}
```

The client auto-detects credentials in this order: explicit `api_key=` / `bearer_token=` / `kubeconfig=` arguments → `AGENTTIER_API_KEY` env var → `AGENTTIER_BEARER_TOKEN` env var → in-cluster service account → kubeconfig. For a port-forwarded dev cluster with the auth bypass on, no credentials are needed.

For production, prefer:

```python
client = AgentTierClient(
    api_url="https://agenttier.example.com",
    api_key="ak_xxx",
    timeout=30,
)
```

Always use `with` to make sure connections close cleanly:

```python
with AgentTierClient(api_url="http://localhost:8081") as client:
    ...
```

## 3. Create a sandbox

```python
sandbox = client.create_sandbox(
    name="sdk-tutorial",
    template="general-coding",
    namespace="default",
)
print(sandbox.status)  # "Creating"
sandbox.wait_until_running(timeout=120)
print(sandbox.status)  # "Running"
```

`create_sandbox` accepts an optional `overrides` dict matching `Sandbox.spec` for ad-hoc customization. For long-term differences, edit the template instead.

## 4. Run commands

```python
result = sandbox.exec("python3 -c 'print(2+2)'")
print(result.stdout)     # "4\n"
print(result.exit_code)  # 0
```

Exec returns a typed `ExecResult` with `stdout`, `stderr`, `exit_code`, and `duration_ms`. It is single-shot (request → response). For interactive PTY use, open the WebSocket terminal from the Web UI or the CLI.

Pass a list to skip the shell:

```python
result = sandbox.exec(["bash", "-lc", "echo $USER"])
```

Set `timeout=` to bound long-running commands; AgentTier sends SIGTERM on timeout, then SIGKILL after a grace period.

## 5. File transfer

```python
# upload from local path
sandbox.files.upload("./script.py", "/workspace/script.py")

# write content directly
sandbox.files.write("/workspace/data.txt", b"hello from python")

# read it back
content = sandbox.files.read("/workspace/data.txt")
print(content)  # b"hello from python"

# list a directory
for entry in sandbox.files.list("/workspace"):
    print(entry.name, entry.size, entry.is_dir)

# download to local
sandbox.files.download("/workspace/data.txt", "./data-from-sandbox.txt")
```

Each transfer goes through the Router and obeys the 32 MiB per-call cap. For larger payloads, split into chunks or `tar` first.

## 6. Lifecycle

```python
sandbox.stop()         # Pod gone, PVC kept
sandbox.resume()       # back to Running on the same PVC
sandbox.refresh()      # pull latest status from API
sandbox.delete()       # permanent
```

Each call returns when the API call completes; status changes are eventually consistent, so use `wait_until_running()` / `wait_until_stopped()` if you need a deterministic state.

## 7. Port forwarding

Open a port from the sandbox to your local laptop or to other tooling that can reach the Router:

```python
# inside the sandbox, start a server first
sandbox.exec(["bash", "-c", "nohup python3 -m http.server 8000 > /tmp/srv.log 2>&1 &"])

# now forward it
forward = sandbox.port_forwards.create(name="web", port=8000)
print(forward.preview_url)
# http://router.example.com/api/v1/sandboxes/sdk-tutorial/preview/8000/
```

The preview URL is auth-gated by the same Router middleware as the rest of the API. List and delete forwards:

```python
for fwd in sandbox.port_forwards.list():
    print(fwd.name, fwd.port, fwd.preview_url)

sandbox.port_forwards.delete("web")
```

If the Helm chart is configured with `networking.previewDomain`, the SDK also returns `forward.ingress_url` for direct DNS access.

## 8. Find existing sandboxes

```python
# list everything you can see
for s in client.list_sandboxes():
    print(s.name, s.status, s.template)

# get a specific one
existing = client.get_sandbox("sdk-tutorial")
existing.exec("ls /workspace")
```

`list_sandboxes()` accepts `namespace=`, `status=`, and `template=` filters.

## 9. Async client

Same surface, async/await:

```python
import asyncio
from agenttier import AsyncAgentTierClient

async def main():
    async with AsyncAgentTierClient(api_url="http://localhost:8081") as client:
        sandbox = await client.create_sandbox(
            name="async-demo",
            template="general-coding",
        )
        await sandbox.wait_until_running()
        result = await sandbox.exec("uname -a")
        print(result.stdout)
        await sandbox.delete()

asyncio.run(main())
```

The async sandbox handle exposes the same namespaces — `sandbox.files`, `sandbox.port_forwards`, `sandbox.agent` — every method is awaitable.

## 10. Error handling

The SDK raises typed exceptions you can catch:

```python
from agenttier import (
    AgentTierError,
    AuthenticationError,
    NotFoundError,
    PolicyViolationError,
    TimeoutError as AgentTierTimeout,
)

try:
    s = client.get_sandbox("does-not-exist")
except NotFoundError:
    print("not there")

try:
    s = client.create_sandbox(name="too-much", template="general-coding",
                              overrides={"resources": {"cpu": "999"}})
except PolicyViolationError as e:
    # governance rejected the request
    print(e.violations)
```

Every error includes the underlying HTTP status code and the structured body the Router returned, so you can render useful messages.

## 11. Clean up

```python
sandbox.delete()
client.close()  # if not using `with`
```

## What you just learned

- Sync (`AgentTierClient`) and async (`AsyncAgentTierClient`) clients with the same API.
- `Sandbox` handles expose `exec`, `files`, `port_forwards`, lifecycle, and `agent` (next tutorial).
- Auth is auto-detected; explicit args override env override kubeconfig.
- Errors are typed and structured.

## What to read next

- [Agent mode in depth](agent-mode-tutorial.md) — `sandbox.agent.configure()` and `invoke_stream()`.
- [SDK reference](../sdk.md) — full method list and signatures.
- [Code mode in depth](code-mode.md) — what to do once you have a long-lived sandbox.
