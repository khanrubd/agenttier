# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Async sandbox handle mirroring :mod:`agenttier.sandbox`."""

from __future__ import annotations

import asyncio
from typing import TYPE_CHECKING, Optional

from agenttier._http import raise_for_status
from agenttier.exceptions import SandboxErrorState, SandboxTimeoutError
from agenttier.models import CommandResult, ForwardedPort, SandboxPhase, SandboxSummary

if TYPE_CHECKING:  # pragma: no cover
    import httpx

_DEFAULT_WAIT_TIMEOUT = 120.0
_DEFAULT_POLL_INTERVAL = 2.0


class AsyncSandbox:
    """Async remote handle for a sandbox."""

    def __init__(
        self,
        http: "httpx.AsyncClient",
        sandbox_id: str,
        name: str,
        namespace: str,
    ) -> None:
        self._http = http
        self.id = sandbox_id
        self.name = name
        self.namespace = namespace

    async def status(self) -> SandboxSummary:
        resp = await self._http.get(f"/sandboxes/{self.id}")
        raise_for_status(resp)
        return SandboxSummary.model_validate(resp.json())

    async def wait_until_running(
        self,
        timeout: float = _DEFAULT_WAIT_TIMEOUT,
        poll_interval: float = _DEFAULT_POLL_INTERVAL,
    ) -> SandboxSummary:
        loop = asyncio.get_event_loop()
        deadline = loop.time() + timeout
        last: Optional[SandboxSummary] = None
        while loop.time() < deadline:
            last = await self.status()
            if last.phase is SandboxPhase.RUNNING:
                return last
            if last.phase is SandboxPhase.ERROR:
                raise SandboxErrorState(last.message or f"sandbox {self.id} entered Error state")
            await asyncio.sleep(poll_interval)
        raise SandboxTimeoutError(
            f"sandbox {self.id} did not reach Running within {timeout:.0f}s "
            f"(last phase: {last.phase.value if last else 'unknown'})"
        )

    async def stop(self) -> None:
        resp = await self._http.post(f"/sandboxes/{self.id}/stop")
        raise_for_status(resp)

    async def resume(self) -> None:
        resp = await self._http.post(f"/sandboxes/{self.id}/resume")
        raise_for_status(resp)

    async def terminate(self) -> None:
        resp = await self._http.delete(f"/sandboxes/{self.id}")
        raise_for_status(resp)

    delete = terminate

    async def exec(self, command: str, timeout: int = 30) -> CommandResult:
        if not command:
            raise ValueError("command must be a non-empty string")
        if timeout <= 0:
            raise ValueError("timeout must be > 0")
        resp = await self._http.post(
            f"/sandboxes/{self.id}/exec",
            json={"command": command, "timeout": timeout},
            timeout=timeout + 10,
        )
        raise_for_status(resp)
        return CommandResult.model_validate(resp.json())

    async def list_ports(self) -> list[ForwardedPort]:
        resp = await self._http.get(f"/sandboxes/{self.id}/ports")
        raise_for_status(resp)
        ports = resp.json().get("ports") or []
        return [ForwardedPort.model_validate(p) for p in ports]

    async def forward_port(self, port: int, protocol: str = "http") -> ForwardedPort:
        if not 1 <= port <= 65535:
            raise ValueError("port must be between 1 and 65535")
        resp = await self._http.post(
            f"/sandboxes/{self.id}/ports",
            json={"port": port, "protocol": protocol},
        )
        raise_for_status(resp)
        return ForwardedPort.model_validate(resp.json())

    async def remove_port(self, port: int) -> None:
        resp = await self._http.delete(f"/sandboxes/{self.id}/ports/{port}")
        raise_for_status(resp)

    def __repr__(self) -> str:
        return f"AsyncSandbox(id={self.id!r}, name={self.name!r}, namespace={self.namespace!r})"
