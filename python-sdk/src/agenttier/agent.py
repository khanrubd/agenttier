# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Sync agent-mode helpers for ``mode: agent`` sandboxes.

Wraps the Router's POST ``/configure``, POST ``/invoke``, and POST
``/invoke/cancel`` endpoints. Lives in its own module so neither the public
``Sandbox`` class nor the equivalent async surface get cluttered with the
SSE-parsing machinery that's only relevant for agent mode.

The two endpoints both stream Server-Sent Events. We parse the wire format
into typed events so callers don't deal with raw bytes.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable, Iterator, Optional, Union

from agenttier._http import raise_for_status
from agenttier.models import ConfigureResult, InvokeEvent, InvokeResult

if TYPE_CHECKING:  # pragma: no cover
    import httpx

    from agenttier.sandbox import Sandbox


# Type alias for the file payload accepted by AgentAPI.configure(). Either a
# raw mapping the SDK forwards verbatim or a (path, source) tuple where source
# is a local file path (str / Path) or in-memory bytes.
ConfigureFile = Union[
    dict[str, Any],
    tuple[str, Union[str, Path, bytes]],
]


class AgentAPI:
    """Sync wrapper for ``/configure`` + ``/invoke`` + ``/invoke/cancel``.

    Use ``sandbox.agent`` to obtain an instance; don't construct directly.
    """

    def __init__(self, sandbox: "Sandbox") -> None:
        self._sandbox = sandbox
        self._http: "httpx.Client" = sandbox._http

    # ------- configure ---------------------------------------------------

    def configure(
        self,
        *,
        files: Optional[list[ConfigureFile]] = None,
        install_command: Optional[list[str]] = None,
        entrypoint: Optional[list[str]] = None,
        on_log: Optional[Callable[[str, str], None]] = None,
        timeout: float = 900.0,
    ) -> ConfigureResult:
        """Upload files + run an install command + set the agent entrypoint.

        Pass ``on_log(stream, line)`` to receive install output live (where
        ``stream`` is ``"stdout"`` or ``"stderr"``); the callback is invoked
        on each line as the SSE stream arrives. When omitted, the install
        runs to completion and only the final result is returned.

        :param files: Each element is either ``{"path": "...", "content": "..."}``
            or a tuple ``(path, source)`` where source is a local file path or
            bytes. Tuples are convenience for uploading from disk.
        :param install_command: Argv for the install step (e.g.
            ``["pip", "install", "-r", "/workspace/requirements.txt"]``).
            Idempotent: re-runs with the same files + command short-circuit.
        :param entrypoint: Argv for ``invoke()``. Persisted into
            ``sandbox.status.agentConfigure.entrypoint`` on success.
        :param timeout: HTTP-level timeout for the whole configure call.
            Defaults to 15 minutes — the same as the Router's soft install
            timeout — so the SDK doesn't cut off a slow pip install.
        """
        payload = _build_configure_payload(files, install_command, entrypoint)

        result: Optional[ConfigureResult] = None
        with self._http.stream(
            "POST",
            f"/sandboxes/{self._sandbox.id}/configure",
            json=payload,
            timeout=timeout,
            headers={"Accept": "text/event-stream"},
        ) as resp:
            raise_for_status(resp)
            for event in _iter_sse(resp.iter_lines()):
                if event.event == "log":
                    if on_log is not None:
                        on_log(event.data.get("stream", "stdout"), event.data.get("data", ""))
                elif event.event == "result":
                    result = ConfigureResult.model_validate(event.data)
                elif event.event == "error":
                    # Router-side error before result was emitted; surface
                    # the message rather than silently returning None.
                    raise RuntimeError(
                        f"configure failed during {event.data.get('phase', 'unknown')}: "
                        f"{event.data.get('message', 'unknown error')}"
                    )

        if result is None:
            raise RuntimeError("configure stream ended without a result event")
        return result

    # ------- invoke ------------------------------------------------------

    def invoke(
        self,
        payload: Union[dict[str, Any], str, bytes, None] = None,
        *,
        prompt: Optional[str] = None,
        timeout: Optional[float] = None,
        invoke_timeout: Optional[str] = None,
    ) -> InvokeResult:
        """Run the configured entrypoint and return the final result.

        Convenience wrapper around :meth:`invoke_stream`: consumes the entire
        SSE stream, accumulates stdout / stderr, and returns one typed result.
        Use :meth:`invoke_stream` directly when you need to render output live.

        :param payload: Body forwarded to the entrypoint on stdin. ``dict``
            values are JSON-encoded; ``str`` / ``bytes`` pass through.
        :param prompt: Convenience for chat-style agents — appended to argv as
            ``--prompt=<value>`` and (when ``payload`` is None) also fed to
            stdin.
        :param timeout: HTTP-level timeout for the whole invoke. Defaults to
            slightly more than the server-side default (30 min + 30 s slack)
            so the SDK doesn't cut off before the Router's exit event.
        :param invoke_timeout: Forwarded as the ``?timeout=`` query param so
            the Router caps the in-pod process. Format: Go duration string
            (``"5m"``, ``"30s"``, ``"1h"``).
        """
        invoke_id = ""
        stdout_chunks: list[str] = []
        stderr_chunks: list[str] = []
        exit_code = -1
        duration_ms = 0
        reason = "error"

        for event in self.invoke_stream(
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

    def invoke_stream(
        self,
        payload: Union[dict[str, Any], str, bytes, None] = None,
        *,
        prompt: Optional[str] = None,
        timeout: Optional[float] = None,
        invoke_timeout: Optional[str] = None,
    ) -> Iterator[InvokeEvent]:
        """Yield SSE events from a single ``/invoke`` call.

        Closing the iterator (or breaking out early) closes the underlying
        HTTP connection, which the Router treats as a client cancel — the
        in-pod process gets SIGTERM via SPDY exec teardown.

        Same parameters as :meth:`invoke`.
        """
        body, content_type = _encode_invoke_body(payload)
        params: dict[str, Any] = {}
        if prompt is not None:
            params["prompt"] = prompt
        if invoke_timeout is not None:
            params["timeout"] = invoke_timeout

        # Default httpx timeout is 5s — way too short for /invoke. Cap on the
        # server side via ?timeout=, with a buffer here so the SDK doesn't
        # cut off before the exit event lands.
        http_timeout = timeout if timeout is not None else 30 * 60 + 30

        with self._http.stream(
            "POST",
            f"/sandboxes/{self._sandbox.id}/invoke",
            content=body,
            headers={"Content-Type": content_type, "Accept": "text/event-stream"},
            params=params,
            timeout=http_timeout,
        ) as resp:
            raise_for_status(resp)
            yield from _iter_sse(resp.iter_lines())

    def invoke_cancel(self, invoke_id: str) -> None:
        """Cancel an in-flight invoke by ID.

        Best-effort. Returns silently on success; raises :class:`NotFoundError`
        when the invoke is no longer running. Pair this with the ``invokeId``
        from the first event of :meth:`invoke_stream`.
        """
        if not invoke_id:
            raise ValueError("invoke_id must be a non-empty string")
        resp = self._http.post(
            f"/sandboxes/{self._sandbox.id}/invoke/cancel",
            json={"invokeId": invoke_id},
        )
        raise_for_status(resp)


# --- shared helpers -------------------------------------------------------

def _build_configure_payload(
    files: Optional[list[ConfigureFile]],
    install_command: Optional[list[str]],
    entrypoint: Optional[list[str]],
) -> dict[str, Any]:
    """Build the JSON body POSTed to /configure.

    Tuple-shaped file entries are normalized into the dict shape the Router
    expects. Local-path sources are read into memory; bytes are
    base64-encoded so binary files round-trip cleanly.
    """
    payload: dict[str, Any] = {}
    if files:
        normalized: list[dict[str, Any]] = []
        for entry in files:
            if isinstance(entry, dict):
                normalized.append(entry)
                continue
            if not (isinstance(entry, tuple) and len(entry) == 2):
                raise TypeError(
                    "files entries must be {'path': ..., 'content': ...} or (path, source)"
                )
            path, source = entry
            if isinstance(source, (str, Path)):
                src_path = Path(source)
                raw = src_path.read_bytes()
                normalized.append(_file_dict_for_bytes(path, raw))
            elif isinstance(source, bytes):
                normalized.append(_file_dict_for_bytes(path, source))
            else:
                raise TypeError(
                    f"unsupported source type for {path}: {type(source).__name__}"
                )
        payload["files"] = normalized
    if install_command:
        payload["installCommand"] = list(install_command)
    if entrypoint:
        payload["entrypoint"] = list(entrypoint)
    return payload


def _file_dict_for_bytes(path: str, raw: bytes) -> dict[str, Any]:
    """Build a single configure file entry. Tries UTF-8 first; falls back
    to base64 for bytes that aren't valid UTF-8."""
    import base64

    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        return {"path": path, "contentBase64": base64.b64encode(raw).decode("ascii")}
    return {"path": path, "content": text}


def _encode_invoke_body(
    payload: Union[dict[str, Any], str, bytes, None],
) -> tuple[bytes, str]:
    """Return ``(body_bytes, content_type)`` for an invoke payload."""
    if payload is None:
        return b"", "application/octet-stream"
    if isinstance(payload, bytes):
        return payload, "application/octet-stream"
    if isinstance(payload, str):
        return payload.encode("utf-8"), "text/plain; charset=utf-8"
    if isinstance(payload, dict):
        return json.dumps(payload).encode("utf-8"), "application/json"
    raise TypeError(f"unsupported payload type: {type(payload).__name__}")


def _iter_sse(lines: Iterator[str]) -> Iterator[InvokeEvent]:
    """Parse a Server-Sent Events stream from ``response.iter_lines()``.

    Handles the standard SSE format: ``event:`` and ``data:`` lines separated
    from the next event by a blank line. Comment lines (``: keepalive``) are
    silently dropped. Reused by both /configure and /invoke since both speak
    the same wire format.
    """
    event_name = "message"
    data_lines: list[str] = []
    for raw in lines:
        line = raw.rstrip("\r")
        if not line:
            if data_lines:
                payload = _parse_sse_data("\n".join(data_lines))
                yield InvokeEvent(event=event_name, data=payload)
            event_name = "message"
            data_lines = []
            continue
        if line.startswith(":"):
            # Comment / keepalive
            continue
        if line.startswith("event:"):
            event_name = line[len("event:"):].strip()
        elif line.startswith("data:"):
            data_lines.append(line[len("data:"):].lstrip())
        # Other fields (id:, retry:) are ignored — the Router doesn't use them.

    # Trailing event with no closing blank line — flush it.
    if data_lines:
        payload = _parse_sse_data("\n".join(data_lines))
        yield InvokeEvent(event=event_name, data=payload)


def _parse_sse_data(raw: str) -> dict[str, Any]:
    """Decode an SSE data payload as JSON, falling back to a string-typed dict."""
    try:
        decoded = json.loads(raw)
    except json.JSONDecodeError:
        return {"data": raw}
    if isinstance(decoded, dict):
        return decoded
    return {"data": decoded}
