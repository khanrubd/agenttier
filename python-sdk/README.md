# AgentTier Python SDK

Programmatic client for [AgentTier](https://github.com/agenttier/agenttier) —
manage isolated, persistent Kubernetes sandboxes for humans and AI agents
from Python.

```
pip install agenttier
```

## Synchronous quick start

```python
from agenttier import AgentTierClient

with AgentTierClient(api_url="https://agenttier.company.com") as client:
    sandbox = client.create_sandbox(template="general-coding", name="demo")
    sandbox.wait_until_running()

    result = sandbox.exec("echo 'hello from AgentTier'")
    print(result.stdout, "exit", result.exit_code)

    port = sandbox.forward_port(8080)
    print("Forwarded:", port.preview_url or port.internal_url)

    sandbox.terminate()
```

## Async

```python
import asyncio
from agenttier import AsyncAgentTierClient

async def main():
    async with AsyncAgentTierClient(api_url="https://agenttier.company.com") as client:
        sandbox = await client.create_sandbox(template="general-coding", name="demo")
        await sandbox.wait_until_running()
        result = await sandbox.exec("uname -a")
        print(result.stdout)
        await sandbox.terminate()

asyncio.run(main())
```

## Authentication

The SDK auto-detects credentials in this order:

1. `AGENTTIER_API_KEY` — sent as `X-API-Key`.
2. `AGENTTIER_TOKEN` — sent as `Authorization: Bearer <token>` (OIDC JWT).
3. In-cluster ServiceAccount token at `/var/run/secrets/kubernetes.io/serviceaccount/token`.
4. Unauthenticated (accepted only in the Router's dev mode).

Or pass an explicit provider:

```python
from agenttier import AgentTierClient, APIKeyAuth, BearerTokenAuth

client = AgentTierClient(
    api_url="https://agenttier.company.com",
    auth=APIKeyAuth("sk_live_..."),
)
```

## Error handling

Every error inherits from `AgentTierError` so you can catch them all at once.
The common subclasses you'll want to handle individually:

| Exception | When |
| --- | --- |
| `AuthenticationError` | 401 — token / API key missing or invalid |
| `AuthorizationError` | 403 — authenticated but not permitted |
| `PolicyViolationError` | 403 with governance body; exposes `.violations` |
| `NotFoundError` | 404 — resource doesn't exist |
| `ConflictError` | 409 — operation invalid for current state |
| `SandboxTimeoutError` | `wait_until_running` timed out |
| `SandboxErrorState` | sandbox entered the `Error` phase while waiting |
| `APIError` | anything else; carries `.status_code` and `.body` |

## Supported API surface (v0.1.1)

Only endpoints that the Router server implements in v0.1.0 are exposed:

- **Sandboxes** — `create_sandbox`, `list_sandboxes`, `get_sandbox`, `stop`,
  `resume`, `terminate`, `exec`, `wait_until_running`, `status`.
- **Port forwarding** — `forward_port`, `list_ports`, `remove_port`.
- **Templates** — `list_templates`, `get_template`.
- **Identity** — `current_user`.

Endpoints that are not yet implemented on the server (file transfer, sharing,
cloning, WebSocket terminal from Python) are **not exposed** by the SDK and
will be added in a future release once the server ships them.

## Supported Python versions

3.10, 3.11, 3.12, 3.13. Runtime dependencies: `httpx` and `pydantic`.

## License

Apache-2.0. Source at
[github.com/agenttier/agenttier/tree/main/python-sdk](https://github.com/agenttier/agenttier/tree/main/python-sdk).
