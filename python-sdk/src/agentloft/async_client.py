# Copyright 2024 AgentLoft Authors.
# SPDX-License-Identifier: Apache-2.0

"""Async client for AgentLoft using httpx.AsyncClient."""

from __future__ import annotations

import asyncio
from typing import Optional

import httpx

from agentloft.auth import AuthProvider, auto_detect_auth
from agentloft.models import CommandResult, FileInfo, SandboxSpec, Template


class AsyncAgentLoftClient:
    """Async client for the AgentLoft REST API.

    Usage:
        async with AsyncAgentLoftClient(api_url="https://agentloft.company.com") as client:
            sandbox = await client.create_sandbox(template="general-coding", name="my-sandbox")
            await sandbox.wait_until_running()
            result = await sandbox.exec("echo hello")
            print(result.stdout)
            await sandbox.terminate()
    """

    def __init__(
        self,
        api_url: str,
        auth: Optional[AuthProvider] = None,
        timeout: float = 30.0,
    ) -> None:
        self._api_url = api_url.rstrip("/")
        self._auth = auth or auto_detect_auth()
        self._http = httpx.AsyncClient(
            base_url=f"{self._api_url}/api/v1",
            timeout=timeout,
            event_hooks={"request": [self._apply_auth]},
        )

    async def _apply_auth(self, request: httpx.Request) -> None:
        self._auth.apply(request)

    async def create_sandbox(
        self,
        template: str,
        name: str,
        namespace: str = "default",
        timeout: Optional[str] = None,
    ) -> "AsyncSandbox":
        body: dict = {
            "name": name,
            "namespace": namespace,
            "templateRef": {"name": template, "kind": "ClusterSandboxTemplate"},
        }
        if timeout:
            body["timeout"] = timeout

        resp = await self._http.post("/sandboxes", json=body)
        resp.raise_for_status()
        data = resp.json()
        return AsyncSandbox(self._http, data["sandboxId"], name, namespace)

    async def list_sandboxes(self, namespace: Optional[str] = None, status: Optional[str] = None) -> list[SandboxSpec]:
        params: dict[str, str] = {}
        if namespace:
            params["namespace"] = namespace
        if status:
            params["status"] = status

        resp = await self._http.get("/sandboxes", params=params)
        resp.raise_for_status()
        return [SandboxSpec(**s) for s in resp.json().get("sandboxes", [])]

    async def get_sandbox(self, sandbox_id: str) -> "AsyncSandbox":
        resp = await self._http.get(f"/sandboxes/{sandbox_id}")
        resp.raise_for_status()
        data = resp.json()
        return AsyncSandbox(self._http, data["sandboxId"], data.get("name", sandbox_id), data.get("namespace", "default"))

    async def list_templates(self) -> list[Template]:
        resp = await self._http.get("/templates")
        resp.raise_for_status()
        return [Template(**t) for t in resp.json().get("templates", [])]

    async def close(self) -> None:
        await self._http.aclose()

    async def __aenter__(self) -> "AsyncAgentLoftClient":
        return self

    async def __aexit__(self, *args: object) -> None:
        await self.close()


class AsyncSandbox:
    """Async sandbox handle."""

    def __init__(self, http: httpx.AsyncClient, sandbox_id: str, name: str, namespace: str) -> None:
        self._http = http
        self.id = sandbox_id
        self.name = name
        self.namespace = namespace

    async def wait_until_running(self, timeout: float = 60.0, poll_interval: float = 2.0) -> None:
        deadline = asyncio.get_event_loop().time() + timeout
        while asyncio.get_event_loop().time() < deadline:
            resp = await self._http.get(f"/sandboxes/{self.id}")
            resp.raise_for_status()
            phase = resp.json().get("status", "")
            if phase == "Running":
                return
            if phase == "Error":
                raise RuntimeError(f"Sandbox entered Error state: {resp.json().get('message')}")
            await asyncio.sleep(poll_interval)
        raise TimeoutError(f"Sandbox {self.id} did not reach Running within {timeout}s")

    async def exec(self, command: str, timeout: int = 30) -> CommandResult:
        resp = await self._http.post(
            f"/sandboxes/{self.id}/exec",
            json={"command": command, "timeout": timeout},
            timeout=timeout + 5,
        )
        resp.raise_for_status()
        data = resp.json()
        return CommandResult(stdout=data.get("stdout", ""), stderr=data.get("stderr", ""), exit_code=data.get("exitCode", -1))

    async def stop(self) -> None:
        resp = await self._http.post(f"/sandboxes/{self.id}/stop")
        resp.raise_for_status()

    async def resume(self) -> None:
        resp = await self._http.post(f"/sandboxes/{self.id}/resume")
        resp.raise_for_status()

    async def terminate(self) -> None:
        resp = await self._http.delete(f"/sandboxes/{self.id}")
        resp.raise_for_status()

    async def clone(self, name: str) -> "AsyncSandbox":
        resp = await self._http.post(f"/sandboxes/{self.id}/clone", json={"name": name})
        resp.raise_for_status()
        data = resp.json()
        return AsyncSandbox(self._http, data["sandboxId"], name, self.namespace)
