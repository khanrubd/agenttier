# Python SDK

`pip install agenttier` gives you sync and async clients, typed Pydantic
models, and streaming file transfers.

## Quick start

```python
from agenttier import AgentTierClient

client = AgentTierClient(api_url="https://agenttier.company.com")

sandbox = client.create_sandbox(template="general-coding", name="my-sandbox")
sandbox.wait_until_running()

result = sandbox.commands.run("uname -a")
print(result.stdout, "exit", result.exit_code)

sandbox.files.write("/workspace/hello.py", "print('hi')")
sandbox.terminate()
```

## Authentication

Auto-detected in this order:

1. `AGENTTIER_API_KEY` env var
2. `AGENTTIER_TOKEN` env var (OIDC bearer)
3. Kubeconfig in-cluster ServiceAccount token

Or pass an explicit provider:

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
from agenttier.async_client import AsyncAgentTierClient

async def main():
    async with AsyncAgentTierClient(api_url="https://agenttier.company.com") as client:
        sb = await client.create_sandbox(template="general-coding", name="demo")
        await sb.wait_until_running()
        r = await sb.commands.run("uname -a")
        print(r.stdout)

asyncio.run(main())
```

## API surface

- `AgentTierClient` / `AsyncAgentTierClient` — top-level clients
- `Sandbox` — handle with `create`, `stop`, `resume`, `terminate`, `status`,
  `wait_until_running`
- `sandbox.commands.run(cmd, timeout=…)` — shell exec
- `sandbox.files.{read,write,list,upload,download}` — file transfer
- Pydantic models: `Template`, `SandboxSpec`, `SandboxStatus`, `CommandResult`,
  `FileInfo`

Source: [python-sdk/](https://github.com/agenttier/agenttier/tree/main/python-sdk).
