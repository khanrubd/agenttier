# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Async client for the AgentTier REST API."""

from __future__ import annotations

from types import TracebackType
from typing import Optional

import httpx

from agenttier._http import default_user_agent, raise_for_status
from agenttier._version import __version__
from agenttier.async_sandbox import AsyncSandbox
from agenttier.auth import AuthProvider, auto_detect_auth
from agenttier.models import CurrentUser, SandboxSummary, Template

_API_PREFIX = "/api/v1"
_DEFAULT_TIMEOUT = 30.0


class AsyncAgentTierClient:
    """Async counterpart to :class:`AgentTierClient`."""

    def __init__(
        self,
        api_url: str,
        auth: Optional[AuthProvider] = None,
        timeout: float = _DEFAULT_TIMEOUT,
        *,
        verify: bool | str = True,
    ) -> None:
        if not api_url:
            raise ValueError("api_url must be a non-empty string")
        self._api_url = api_url.rstrip("/")
        self._auth = auth or auto_detect_auth()
        self._http = httpx.AsyncClient(
            base_url=f"{self._api_url}{_API_PREFIX}",
            timeout=timeout,
            verify=verify,
            headers={"User-Agent": default_user_agent(__version__)},
            event_hooks={"request": [self._apply_auth]},
        )

    async def __aenter__(self) -> "AsyncAgentTierClient":
        return self

    async def __aexit__(
        self,
        exc_type: Optional[type[BaseException]],
        exc: Optional[BaseException],
        tb: Optional[TracebackType],
    ) -> None:
        await self.close()

    async def close(self) -> None:
        await self._http.aclose()

    async def create_sandbox(
        self,
        template: str,
        name: str,
        namespace: str = "default",
        timeout: Optional[str] = None,
        idle_timeout: Optional[str] = None,
        storage_size: Optional[str] = None,
    ) -> AsyncSandbox:
        if not template:
            raise ValueError("template must be a non-empty string")
        if not name:
            raise ValueError("name must be a non-empty string")

        body: dict[str, object] = {
            "name": name,
            "namespace": namespace,
            "templateRef": {"name": template, "kind": "ClusterSandboxTemplate"},
        }
        if timeout:
            body["timeout"] = timeout
        if idle_timeout:
            body["idleTimeout"] = idle_timeout
        if storage_size:
            body["storage"] = {"size": storage_size}

        resp = await self._http.post("/sandboxes", json=body)
        raise_for_status(resp)
        data = resp.json()
        return AsyncSandbox(
            self._http,
            data["sandboxId"],
            data.get("name", name),
            data.get("namespace", namespace),
        )

    async def list_sandboxes(
        self,
        namespace: Optional[str] = None,
        status: Optional[str] = None,
    ) -> list[SandboxSummary]:
        params: dict[str, str] = {}
        if namespace:
            params["namespace"] = namespace
        if status:
            params["status"] = status
        resp = await self._http.get("/sandboxes", params=params)
        raise_for_status(resp)
        return [SandboxSummary.model_validate(s) for s in (resp.json().get("sandboxes") or [])]

    async def get_sandbox(self, sandbox_id: str) -> AsyncSandbox:
        if not sandbox_id:
            raise ValueError("sandbox_id must be a non-empty string")
        resp = await self._http.get(f"/sandboxes/{sandbox_id}")
        raise_for_status(resp)
        data = resp.json()
        return AsyncSandbox(
            self._http,
            data["sandboxId"],
            data.get("name", sandbox_id),
            data.get("namespace", "default"),
        )

    async def list_templates(self) -> list[Template]:
        resp = await self._http.get("/templates")
        raise_for_status(resp)
        return [Template.model_validate(t) for t in (resp.json().get("templates") or [])]

    async def get_template(self, name: str) -> Template:
        if not name:
            raise ValueError("name must be a non-empty string")
        resp = await self._http.get(f"/templates/{name}")
        raise_for_status(resp)
        return Template.model_validate(resp.json())

    async def current_user(self) -> CurrentUser:
        resp = await self._http.get("/user/me")
        raise_for_status(resp)
        return CurrentUser.model_validate(resp.json())

    async def _apply_auth(self, request: httpx.Request) -> None:
        self._auth.apply(request)
