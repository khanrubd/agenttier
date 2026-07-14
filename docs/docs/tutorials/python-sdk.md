# Tutorial: Python SDK walkthrough

Drive AgentTier from Python: lifecycle, exec, file transfer, port forwarding, and async patterns.

**Time:** ~25 minutes
**Prerequisites:** AgentTier installed and a way to reach the Router HTTP API (port-forward or Ingress URL).

## 1. Install

```bash
pip install agenttier
```

The wheel ships a `py.typed` marker so mypy and pyright pick up types automatically. Python 3.10+ is required.

## 2. Connect

The simplest connection target is a `kubectl port-forward`:

```bash
kubectl port-forward -n agenttier svc/agenttier-router 8081:80
```

Then in Python:

```python
from agenttier import AgentTierClient

client = AgentTierClient(api_url="http://localhost:8081")
print(client.current_user())  # CurrentUser(sub=..., email=..., is_admin=...)
```

The client auto-detects credentials in this order: `AGENTTIER_API_KEY` env var → `AGENTTIER_TOKEN` env var (bearer / OIDC JWT) → in-cluster ServiceAccount token → unauthenticated (accepted only when the Router runs in dev mode). For a port-forwarded dev cluster with the auth bypass on, no credentials are needed.

For production, pass an explicit provider instead of relying on env vars:

```python
from agenttier import AgentTierClient, APIKeyAuth

client = AgentTierClient(
    api_url="https://agenttier.example.com",
    auth=APIKeyAuth("ak_xxx"),
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
sandbox = client.create_sandbox(template="general-coding", name="sdk-tutorial", namespace="default")
print(sandbox.phase)  # SandboxPhase.CREATING
sandbox.wait_until_running(timeout=120)
print(sandbox.phase)  # SandboxPhase.RUNNING
```

`create_sandbox` also takes optional `timeout=`/`idle_timeout=` (Go duration strings, e.g. `"8h"`/`"30m"`) and `storage_size=` (e.g. `"10Gi"`). For anything beyond those, edit the template instead — there is no free-form spec-override kwarg.

## 4. Run commands

```python
result = sandbox.exec("python3 -c 'print(2+2)'")
print(result.stdout)     # "4\n"
print(result.exit_code)  # 0
```

`exec` returns a typed `CommandResult` with `stdout`, `stderr`, `exit_code`. It takes a single shell command string (run via `sh -c`), not an argv list, and is single-shot (request → response). For interactive PTY use, open the terminal from the Web UI.

```python
result = sandbox.exec("bash -lc 'echo $USER'")
```

Set `timeout=` (seconds, default 30) to bound long-running commands.

## 5. File transfer

```python
# upload from local path (path is the remote destination, source is local)
sandbox.files.upload("/workspace/script.py", "./script.py")

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

Each transfer goes through the Router and obeys the 32 MiB per-call cap (`FilesAPI.MAX_BYTES`). For larger payloads, use `sandbox.files.archive(...)` to pull a whole directory as a `.zip`, or `tar` first and upload the archive.

## 6. Lifecycle

```python
sandbox.stop()         # Pod gone, PVC kept
sandbox.resume()       # back to Running on the same PVC
sandbox.status()       # fetch the latest SandboxSummary from the API
sandbox.terminate()    # permanent; `.delete()` is an alias for the same call
```

Each call returns when the API call completes; status changes are eventually consistent, so use `wait_until_running(timeout=...)` if you need a deterministic state (there is no `wait_until_stopped()`).

## 7. Port forwarding

Open a port from the sandbox to your local laptop or to other tooling that can reach the Router:

```python
# inside the sandbox, start a server first
sandbox.exec("nohup python3 -m http.server 8000 > /tmp/srv.log 2>&1 &")

# now forward it
forward = sandbox.forward_port(8000)
print(forward.preview_url or forward.internal_url)
# http://router.example.com/api/v1/sandboxes/sdk-tutorial/preview/8000/
```

The preview URL is auth-gated by the same Router middleware as the rest of the API. List and remove forwards (by port number — `ForwardedPort` has no name field):

```python
for fwd in sandbox.list_ports():
    print(fwd.port, fwd.protocol, fwd.preview_url)

sandbox.remove_port(8000)
```

`forward.preview_url` is only populated when the Helm chart is configured with `networking.previewDomain`; otherwise it's `None` and only `internal_url` (the in-Router proxy path) is set.

## 8. Find existing sandboxes

```python
# list everything you can see
for s in client.list_sandboxes():
    print(s.name, s.status, s.template_ref)

# get a specific one
existing = client.get_sandbox("sdk-tutorial")
existing.exec("ls /workspace")
```

`list_sandboxes()` accepts `namespace=` and `status=` filters — there is no `template=` filter.

## 9. Async client

Same surface, async/await:

```python
import asyncio
from agenttier import AsyncAgentTierClient

async def main():
    async with AsyncAgentTierClient(api_url="http://localhost:8081") as client:
        sandbox = await client.create_sandbox(template="general-coding", name="async-demo")
        await sandbox.wait_until_running()
        result = await sandbox.exec("uname -a")
        print(result.stdout)
        await sandbox.terminate()

asyncio.run(main())
```

The async sandbox handle exposes the same namespaces — `sandbox.files`, `sandbox.agent` — every method is awaitable. Port forwarding is the same `forward_port`/`list_ports`/`remove_port` trio, also awaitable.

## 10. Error handling

The SDK raises typed exceptions you can catch:

```python
from agenttier import (
    AgentTierError,
    AuthenticationError,
    NotFoundError,
    PolicyViolationError,
    APIError,
)

try:
    s = client.get_sandbox("does-not-exist")
except NotFoundError:
    print("not there")

try:
    s = client.create_sandbox(template="general-coding", name="too-much")
except PolicyViolationError as e:
    # governance rejected the request
    print(e.violations)
except APIError as e:
    print(f"HTTP {e.status_code}: {e}")
```

`APIError` carries `.status_code` and `.body` (the structured error the Router returned) for anything not covered by a more specific exception.

## 11. Clean up

```python
sandbox.terminate()
client.close()  # if not using `with`
```

## What you just learned

- Sync (`AgentTierClient`) and async (`AsyncAgentTierClient`) clients with the same API.
- `Sandbox` handles expose `exec`, `files`, port forwarding (`forward_port`/`list_ports`/`remove_port`), lifecycle, and `agent` (next tutorial).
- Auth is auto-detected from env vars, or set explicitly via an `AuthProvider`.
- Errors are typed and structured.

## What to read next

- [Agent mode in depth](agent-mode-tutorial.md) — `sandbox.agent.configure()` and `invoke_stream()`.
- [SDK reference](../sdk.md) — full method list and signatures.
- [Code mode in depth](code-mode.md) — what to do once you have a long-lived sandbox.
