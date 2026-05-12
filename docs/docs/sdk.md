# Python SDK

`pip install agenttier` gives you sync and async clients with typed Pydantic
models, auto-detected auth, streaming HTTP, and a typed exception hierarchy.

Source: [`python-sdk/`](https://github.com/agenttier/agenttier/tree/main/python-sdk).
PyPI: [pypi.org/project/agenttier](https://pypi.org/project/agenttier/).

## Install

```bash
pip install agenttier
```

Supported Python: 3.10, 3.11, 3.12, 3.13. Runtime deps are `httpx` and
`pydantic` — nothing else.

## Hello sandbox

```python
from agenttier import AgentTierClient

with AgentTierClient(api_url="https://agenttier.company.com") as client:
    sandbox = client.create_sandbox(template="general-coding", name="hello")
    sandbox.wait_until_running()

    result = sandbox.exec("uname -a")
    print(result.stdout.strip())
    print("exit:", result.exit_code)

    sandbox.terminate()
```

## Authentication

The SDK auto-detects credentials in priority order:

1. `AGENTTIER_API_KEY` env var → sent as `X-API-Key`.
2. `AGENTTIER_TOKEN` env var → sent as `Authorization: Bearer <token>` (OIDC JWT).
3. In-cluster ServiceAccount token at `/var/run/secrets/kubernetes.io/serviceaccount/token`.
4. Unauthenticated — accepted only in the Router's dev mode.

Or pass an explicit provider:

```python
from agenttier import AgentTierClient, APIKeyAuth, BearerTokenAuth

# API key
client = AgentTierClient(
    api_url="https://agenttier.company.com",
    auth=APIKeyAuth("sk_live_abc123"),
)

# OIDC JWT you fetched yourself
client = AgentTierClient(
    api_url="https://agenttier.company.com",
    auth=BearerTokenAuth(jwt_string),
)
```

## Async

```python
import asyncio
from agenttier import AsyncAgentTierClient

async def main():
    async with AsyncAgentTierClient(api_url="https://agenttier.company.com") as client:
        sandbox = await client.create_sandbox(
            template="general-coding", name="async-demo",
        )
        await sandbox.wait_until_running()
        result = await sandbox.exec("python -c 'print(2+2)'")
        print(result.stdout)
        await sandbox.terminate()

asyncio.run(main())
```

Both clients share the same exception hierarchy and model types.

## API reference

### Clients

- `AgentTierClient(api_url, auth=None, timeout=30.0, verify=True)`
- `AsyncAgentTierClient(api_url, auth=None, timeout=30.0, verify=True)`

Use them as context managers (`with` / `async with`) so the underlying HTTPX
client closes cleanly. Methods:

| Method | Returns |
| --- | --- |
| `create_sandbox(template, name, namespace="default", timeout=None, idle_timeout=None, storage_size=None)` | `Sandbox` |
| `list_sandboxes(namespace=None, status=None)` | `list[SandboxSummary]` |
| `get_sandbox(sandbox_id)` | `Sandbox` |
| `list_templates()` | `list[Template]` |
| `get_template(name)` | `Template` |
| `current_user()` | `CurrentUser` (includes `is_admin` bit) |

### Sandbox handle

```python
sandbox = client.create_sandbox(template="general-coding", name="demo")

# State
sandbox.status()                    # SandboxSummary
sandbox.phase                       # SandboxPhase enum
sandbox.wait_until_running(timeout=120.0)  # SandboxSummary

# Lifecycle
sandbox.stop()
sandbox.resume()
sandbox.terminate()   # alias: .delete()

# Execution
result = sandbox.exec("ls -la /workspace", timeout=30)
# CommandResult(stdout=..., stderr=..., exit_code=...)

# Port forwarding
fp = sandbox.forward_port(8080)
# ForwardedPort(port=8080, protocol='http', internal_url=..., preview_url=...)
sandbox.list_ports()
sandbox.remove_port(8080)
```

The async counterpart `AsyncSandbox` has the same methods with `await` and the
same return types.

## Models

Models are Pydantic v2 objects. Field names are snake_case in Python but
accept the Router's camelCase JSON transparently.

- `SandboxSummary` — `sandbox_id`, `name`, `namespace`, `status`, `phase`, `pod_name`, `pvc_name`, `template_ref`, `created_at`, `last_activity_at`, `created_by`, `message`.
- `SandboxPhase` — enum (`CREATING`, `RUNNING`, `STOPPED`, `ERROR`, `DELETING`, `UNKNOWN`).
- `CommandResult` — `stdout`, `stderr`, `exit_code`.
- `ForwardedPort` — `port`, `protocol`, `preview_url`, `internal_url`.
- `Template` — `name`, `description`, `image`, `resource_version`, `spec` (free-form dict).
- `CurrentUser` — `sub`, `email`, `name`, `groups`, `is_admin`.
- `CreatedBy` — `email`, `display_name`.
- `AuditEvent` — `timestamp`, `event_type`, `sandbox_id`, `sandbox_name`, `namespace`, `user_email`, `details`.
- `UsageAnalytics` — fleet-wide rollups.

## Error handling

Every SDK error inherits from `AgentTierError`. Catch that to handle anything.

```python
from agenttier import (
    AgentTierClient,
    AgentTierError,
    AuthenticationError,
    AuthorizationError,
    PolicyViolationError,
    NotFoundError,
    ConflictError,
    SandboxTimeoutError,
    SandboxErrorState,
    APIError,
)

try:
    sandbox = client.create_sandbox(template="general-coding", name="demo")
    sandbox.wait_until_running(timeout=60)
except PolicyViolationError as e:
    for v in e.violations:
        print(f"Rejected by policy [{v['code']}]: {v['message']}")
except AuthenticationError:
    print("Bad credentials. Check AGENTTIER_API_KEY or AGENTTIER_TOKEN.")
except AuthorizationError:
    print("Authenticated but not authorized.")
except NotFoundError:
    print("Sandbox, template, or resource not found.")
except ConflictError:
    print("Operation is invalid for the current state.")
except SandboxTimeoutError:
    print("Sandbox didn't reach Running in time.")
except SandboxErrorState as e:
    print(f"Sandbox went to Error phase: {e}")
except APIError as e:
    print(f"HTTP {e.status_code}: {e}")
except AgentTierError as e:
    print(f"SDK error: {e}")
```

`PolicyViolationError.violations` is a list of `{"code": ..., "message": ...}`
dicts. The codes (`user_quota_exceeded`, `template_not_allowed`,
`cpu_limit_exceeded`, etc.) are stable — safe to branch on in UI code. See
[Governance](governance.md#violations) for the full list.

## Real-world snippets

### Run tests in a sandbox and download the result

```python
with AgentTierClient(api_url=API) as client:
    sbx = client.create_sandbox(template="general-coding", name="ci-run-123")
    sbx.wait_until_running()

    # Clone and test
    r = sbx.exec(
        "git clone https://github.com/org/repo /workspace/r && "
        "cd /workspace/r && make test > /tmp/test.log 2>&1; echo $?",
        timeout=300,
    )
    print("tests finished, exit:", r.stdout.strip())

    # Stream the log back via exec (file transfer API lands in 0.2.x).
    log = sbx.exec("cat /tmp/test.log").stdout
    with open("test.log", "w") as f:
        f.write(log)

    sbx.terminate()
```

### Orchestrate a handful of agents in parallel

```python
import asyncio
from agenttier import AsyncAgentTierClient

API = "https://agenttier.company.com"
TASKS = ["fix-auth-bug", "add-feature-x", "upgrade-deps"]

async def run_agent(client: AsyncAgentTierClient, name: str) -> str:
    sbx = await client.create_sandbox(template="claude-code-bedrock", name=name)
    try:
        await sbx.wait_until_running()
        r = await sbx.exec(
            f"claude -p 'Task: {name}. Work in /workspace, commit when done.'",
            timeout=1800,
        )
        return r.stdout
    finally:
        await sbx.terminate()

async def main():
    async with AsyncAgentTierClient(api_url=API) as client:
        results = await asyncio.gather(*[run_agent(client, t) for t in TASKS])
    for task, output in zip(TASKS, results):
        print(f"--- {task} ---\n{output[:500]}…")

asyncio.run(main())
```

### Preview a running web server through a forwarded port

```python
with AgentTierClient(api_url=API) as client:
    sbx = client.create_sandbox(template="general-coding", name="preview")
    sbx.wait_until_running()

    # Start a server inside the sandbox (fire-and-forget is just a timeout=1).
    sbx.exec("nohup python3 -m http.server 8080 > /tmp/srv.log 2>&1 &", timeout=5)

    port = sbx.forward_port(8080)
    print("Internal URL:", port.internal_url)
    print("Preview URL:", port.preview_url or "(set networking.previewDomain to enable public URL)")
```

## Command-line install

Quick one-off without a venv:

```bash
python3 -m pip install --user agenttier
python3 -c "from agenttier import __version__; print(__version__)"
```

For a pinned install in your own project, prefer an explicit range:

```toml
# pyproject.toml
[project]
dependencies = ["agenttier>=0.1.1,<0.2"]
```

## Upgrading the SDK

```bash
pip install --upgrade agenttier
```

The SDK follows the same version as the rest of the platform, but it is
versioned independently on PyPI. You can pair an older SDK with a newer server
as long as both support the endpoints you call; the SDK tolerates new server
fields gracefully thanks to Pydantic's `extra="ignore"` setting on every model.

## License

Apache-2.0.
