# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Async mirror of :mod:`agenttier.agent`.

Same surface as :class:`AgentAPI` but built around ``httpx.AsyncClient`` so
callers in async frameworks (FastAPI, asyncio, anyio) can ``await`` the
methods directly.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Any, AsyncIterator, Awaitable, Callable, Optional, Union

from agenttier._http import raise_for_status
from agenttier.agent import (
    ConfigureFile,
    _build_configure_payload,
    _encode_invoke_body,
    _parse_sse_data,
)
from agenttier.models import ConfigureResult, InvokeEvent, InvokeResult

if TYPE_CHECKING:  # pragma: no cover
    import httpx

    from agenttier.async_sandbox import AsyncSandbox


class AsyncAgentAPI:
    """Async wrapper for ``/configure`` + ``/invoke`` + ``/invoke/cancel``."""

    def __init__(self, sandbox: "AsyncSandbox") -> None:
        self._sandbox = sandbox
        self._http: "httpx.AsyncClient" = sandbox._http

    async def configure(
        self,
        *,
        files: Optional[list[ConfigureFile]] = None,
        install_command: Optional[list[str]] = None,
        entrypoint: Optional[list[str]] = None,
        on_log: Optional[Callable[[str, str], Awaitable[None]]] = None,
        timeout: float = 900.0,
    ) -> ConfigureResult:
        """Async :meth:`AgentAPI.configure`.

        ``on_log`` may be either a regular callable or a coroutine; we await
        coroutine callables. Both are supported because most async users
        already have async logging helpers.
        """
        payload = _build_configure_payload(files, install_command, entrypoint)

        result: Optional[ConfigureResult] = None
        async with self._http.stream(
            "POST",
            f"/sandboxes/{self._sandbox.id}/configure",
            json=payload,
            timeout=timeout,
            headers={"Accept": "text/event-stream"},
        ) as resp:
            raise_for_status(resp)
            async for event in _iter_sse_async(resp.aiter_lines()):
                if event.event == "log":
                    if on_log is not None:
                        outcome = on_log(
                            event.data.get("stream", "stdout"),
                            event.data.get("data", ""),
                        )
                        if hasattr(outcome, "__await__"):
                            await outcome
                elif event.event == "result":
                    result = ConfigureResult.model_validate(event.data)
                elif event.event == "error":
                    raise RuntimeError(
                        f"configure failed during {event.data.get('phase', 'unknown')}: "
                        f"{event.data.get('message', 'unknown error')}"
                    )

        if result is None:
            raise RuntimeError("configure stream ended without a result event")
        return result

    async def invoke(
        self,
        payload: Union[dict[str, Any], str, bytes, None] = None,
        *,
        prompt: Optional[str] = None,
        timeout: Optional[float] = None,
        invoke_timeout: Optional[str] = None,
    ) -> InvokeResult:
        """Async :meth:`AgentAPI.invoke`."""
        invoke_id = ""
        stdout_chunks: list[str] = []
        stderr_chunks: list[str] = []
        exit_code = -1
        duration_ms = 0
        reason = "error"

        async for event in self.invoke_stream(
            payload, prompt=prompt, timeout=timeout, invoke_timeout=invoke_timeout
        ):
            if event.event == "start":
                invoke_id = str(event.data.get("invokeId", ""))
            elif event.event == "log":
                line = str(event.data.get("data", ""))
                stream = str(event.data.get("stream", "stdout"))
                if stream == "stderr":
                    stderr_chunks.append(line)
                else:
                    stdout_chunks.append(line)
            elif event.event == "exit":
                exit_code = int(event.data.get("exitCode", -1))
                duration_ms = int(event.data.get("durationMs", 0))
                reason = str(event.data.get("reason", "completed"))
                if not invoke_id:
                    invoke_id = str(event.data.get("invokeId", ""))
            elif event.event == "error":
                raise RuntimeError(
                    f"invoke failed: {event.data.get('message', 'unknown error')}"
                )

        return InvokeResult(
            invokeId=invoke_id,
            exitCode=exit_code,
            durationMs=duration_ms,
            reason=reason,
            stdout="\n".join(stdout_chunks),
            stderr="\n".join(stderr_chunks),
        )

    async def invoke_stream(
        self,
        payload: Union[dict[str, Any], str, bytes, None] = None,
        *,
        prompt: Optional[str] = None,
        timeout: Optional[float] = None,
        invoke_timeout: Optional[str] = None,
    ) -> AsyncIterator[InvokeEvent]:
        """Async :meth:`AgentAPI.invoke_stream`."""
        body, content_type = _encode_invoke_body(payload)
        params: dict[str, Any] = {}
        if prompt is not None:
            params["prompt"] = prompt
        if invoke_timeout is not None:
            params["timeout"] = invoke_timeout
        http_timeout = timeout if timeout is not None else 30 * 60 + 30

        async with self._http.stream(
            "POST",
            f"/sandboxes/{self._sandbox.id}/invoke",
            content=body,
            headers={"Content-Type": content_type, "Accept": "text/event-stream"},
            params=params,
            timeout=http_timeout,
        ) as resp:
            raise_for_status(resp)
            async for event in _iter_sse_async(resp.aiter_lines()):
                yield event

    async def invoke_cancel(self, invoke_id: str) -> None:
        """Async :meth:`AgentAPI.invoke_cancel`."""
        if not invoke_id:
            raise ValueError("invoke_id must be a non-empty string")
        resp = await self._http.post(
            f"/sandboxes/{self._sandbox.id}/invoke/cancel",
            json={"invokeId": invoke_id},
        )
        raise_for_status(resp)


async def _iter_sse_async(lines: AsyncIterator[str]) -> AsyncIterator[InvokeEvent]:
    """Async SSE parser. Mirrors :func:`agenttier.agent._iter_sse` exactly
    but consumes httpx's async line iterator."""
    event_name = "message"
    data_lines: list[str] = []
    async for raw in lines:
        line = raw.rstrip("\r")
        if not line:
            if data_lines:
                payload = _parse_sse_data("\n".join(data_lines))
                yield InvokeEvent(event=event_name, data=payload)
            event_name = "message"
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        if line.startswith("event:"):
            event_name = line[len("event:"):].strip()
        elif line.startswith("data:"):
            data_lines.append(line[len("data:"):].lstrip())

    if data_lines:
        payload = _parse_sse_data("\n".join(data_lines))
        yield InvokeEvent(event=event_name, data=payload)
