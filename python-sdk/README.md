# AgentTier Python SDK

Programmatic sandbox management for [AgentTier](https://github.com/agenttier/agenttier).

## Install

```bash
pip install agenttier
```

## Quick start

```python
from agenttier import AgentTierClient

client = AgentTierClient(api_url="https://agenttier.company.com")

sandbox = client.create_sandbox(template="general-coding", name="my-sandbox")
sandbox.wait_until_running()

result = sandbox.commands.run("echo 'Hello from AgentTier!'")
print(result.stdout)  # "Hello from AgentTier!"

sandbox.files.write("/workspace/hello.py", "print('works!')")
sandbox.terminate()
```

## Authentication

The SDK auto-detects credentials in this order:

1. `AGENTTIER_API_KEY` environment variable
2. `AGENTTIER_TOKEN` (bearer / OIDC JWT) environment variable
3. Kubeconfig or in-cluster ServiceAccount token (via `KUBECONFIG`)

You can also pass an explicit provider:

```python
from agenttier import AgentTierClient
from agenttier.auth import APIKeyAuth

client = AgentTierClient(
    api_url="https://agenttier.company.com",
    auth=APIKeyAuth("sk_live_..."),
)
```

## Async client

```python
import asyncio
from agenttier import AsyncAgentTierClient

async def main():
    async with AsyncAgentTierClient(api_url="https://agenttier.company.com") as client:
        sandbox = await client.create_sandbox(template="general-coding", name="my-sandbox")
        await sandbox.wait_until_running()
        result = await sandbox.commands.run("uname -a")
        print(result.stdout)

asyncio.run(main())
```

## API surface

- `AgentTierClient` / `AsyncAgentTierClient` — top-level client
- `Sandbox` — handle with `create`, `stop`, `resume`, `terminate`, `status`, `wait_until_running`
- `sandbox.commands` — run shell commands (`run`, `exec`)
- `sandbox.files` — transfer files (`read`, `write`, `list`, `upload`, `download`)
- `Template` / `SandboxSpec` / `SandboxStatus` / `CommandResult` / `FileInfo` — Pydantic models

## License

Apache-2.0.
