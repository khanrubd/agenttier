# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Tests for the agent-mode wrappers on Sandbox + AsyncSandbox.

Covers /configure, /invoke, /invoke/cancel. Stubs the SSE wire format with
pytest-httpx so we exercise the SDK's parser end-to-end without a real
Router.
"""

from __future__ import annotations

import pytest
from pytest_httpx import HTTPXMock

from agenttier import AgentTierClient, AsyncAgentTierClient
from agenttier.agent import _build_configure_payload, _encode_invoke_body, _iter_sse

API_URL = "http://router.test"

_SANDBOX_RESP = {
    "sandboxId": "sb1",
    "name": "sb1",
    "namespace": "default",
    "status": "Running",
}


def _register_get_sandbox(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1",
        json=_SANDBOX_RESP,
    )


# --- pure unit tests for the helpers --------------------------------------

def test_build_configure_payload_normalizes_inline_dicts() -> None:
    payload = _build_configure_payload(
        files=[{"path": "/workspace/agent.py", "content": "print('hi')"}],
        install_command=["pip", "install", "x"],
        entrypoint=["python", "agent.py"],
    )
    assert payload == {
        "files": [{"path": "/workspace/agent.py", "content": "print('hi')"}],
        "installCommand": ["pip", "install", "x"],
        "entrypoint": ["python", "agent.py"],
    }


def test_build_configure_payload_reads_local_path(tmp_path) -> None:
    src = tmp_path / "agent.py"
    src.write_text("print('local')")
    payload = _build_configure_payload(
        files=[("/workspace/agent.py", src)],
        install_command=None,
        entrypoint=None,
    )
    assert payload["files"][0]["path"] == "/workspace/agent.py"
    assert payload["files"][0]["content"] == "print('local')"


def test_build_configure_payload_base64_for_binary() -> None:
    binary = bytes([0x80, 0xFF, 0x00, 0x01])  # not valid UTF-8
    payload = _build_configure_payload(
        files=[("/workspace/binary.bin", binary)],
        install_command=None,
        entrypoint=None,
    )
    assert "contentBase64" in payload["files"][0]
    assert "content" not in payload["files"][0]


def test_build_configure_payload_rejects_unsupported_source() -> None:
    with pytest.raises(TypeError):
        _build_configure_payload(
            files=[("/workspace/x", 123)],  # type: ignore[list-item]
            install_command=None,
            entrypoint=None,
        )


def test_encode_invoke_body_dict_to_json() -> None:
    body, ctype = _encode_invoke_body({"prompt": "hi"})
    assert ctype == "application/json"
    assert body == b'{"prompt": "hi"}'


def test_encode_invoke_body_string_passthrough() -> None:
    body, ctype = _encode_invoke_body("hello")
    assert ctype.startswith("text/plain")
    assert body == b"hello"


def test_encode_invoke_body_none_is_empty() -> None:
    body, ctype = _encode_invoke_body(None)
    assert body == b""
    assert ctype == "application/octet-stream"


def test_iter_sse_parses_typed_events() -> None:
    raw = [
        "event: start",
        'data: {"invokeId":"inv-1","startedAt":1}',
        "",
        ": keepalive",
        "",
        "event: log",
        'data: {"stream":"stdout","data":"hello"}',
        "",
        "event: exit",
        'data: {"invokeId":"inv-1","exitCode":0,"durationMs":42,"reason":"completed"}',
        "",
    ]
    events = list(_iter_sse(iter(raw)))
    assert [e.event for e in events] == ["start", "log", "exit"]
    assert events[0].data["invokeId"] == "inv-1"
    assert events[1].data["data"] == "hello"
    assert events[2].data["exitCode"] == 0


def test_iter_sse_handles_trailing_event_without_blank_line() -> None:
    raw = [
        "event: result",
        'data: {"installExitCode":0}',
    ]
    events = list(_iter_sse(iter(raw)))
    assert len(events) == 1
    assert events[0].event == "result"


# --- HTTP-level tests (sync) ----------------------------------------------

# SSE responses are just bytes in pytest-httpx; we hand-craft the wire format.
_INVOKE_SSE_BODY = (
    b"event: start\n"
    b'data: {"invokeId":"inv-abc","startedAt":1}\n\n'
    b"event: log\n"
    b'data: {"stream":"stdout","data":"hello"}\n\n'
    b"event: log\n"
    b'data: {"stream":"stderr","data":"warn"}\n\n'
    b"event: exit\n"
    b'data: {"invokeId":"inv-abc","exitCode":0,"durationMs":123,"reason":"completed"}\n\n'
)


def test_invoke_returns_aggregated_result(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="POST",
        url=f"{API_URL}/api/v1/sandboxes/sb1/invoke",
        content=_INVOKE_SSE_BODY,
        headers={"Content-Type": "text/event-stream"},
    )
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        result = sb.agent.invoke({"prompt": "hi"})
    assert result.invoke_id == "inv-abc"
    assert result.exit_code == 0
    assert result.duration_ms == 123
    assert result.reason == "completed"
    assert result.stdout == "hello"
    assert result.stderr == "warn"


def test_invoke_stream_yields_events(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="POST",
        url=f"{API_URL}/api/v1/sandboxes/sb1/invoke",
        content=_INVOKE_SSE_BODY,
        headers={"Content-Type": "text/event-stream"},
    )
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        events = list(sb.agent.invoke_stream("prompt-bytes"))
    names = [e.event for e in events]
    assert names == ["start", "log", "log", "exit"]


_CONFIGURE_SSE_BODY = (
    b"event: log\n"
    b'data: {"stream":"stdout","data":"installing..."}\n\n'
    b"event: result\n"
    b'data: {"installCommandHash":"abc","installExitCode":0,"entrypoint":["python","/workspace/agent.py"],"skipped":false}\n\n'
)


def test_configure_returns_typed_result(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="POST",
        url=f"{API_URL}/api/v1/sandboxes/sb1/configure",
        content=_CONFIGURE_SSE_BODY,
        headers={"Content-Type": "text/event-stream"},
    )
    captured: list[tuple[str, str]] = []
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        result = sb.agent.configure(
            files=[{"path": "/workspace/agent.py", "content": "print('hi')"}],
            install_command=["pip", "install", "x"],
            entrypoint=["python", "/workspace/agent.py"],
            on_log=lambda stream, line: captured.append((stream, line)),
        )
    assert result.install_command_hash == "abc"
    assert result.install_exit_code == 0
    assert result.entrypoint == ["python", "/workspace/agent.py"]
    assert result.skipped is False
    assert captured == [("stdout", "installing...")]


def test_configure_raises_on_error_event(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="POST",
        url=f"{API_URL}/api/v1/sandboxes/sb1/configure",
        content=(
            b"event: error\n"
            b'data: {"phase":"install","message":"pip not found"}\n\n'
        ),
        headers={"Content-Type": "text/event-stream"},
    )
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        with pytest.raises(RuntimeError, match="pip not found"):
            sb.agent.configure(install_command=["pip", "install", "x"])


def test_invoke_cancel_posts_invoke_id(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="POST",
        url=f"{API_URL}/api/v1/sandboxes/sb1/invoke/cancel",
        status_code=204,
    )
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        sb.agent.invoke_cancel("inv-abc")
    # pytest-httpx asserts the request was made; we additionally verify the body.
    requests = httpx_mock.get_requests(
        method="POST", url=f"{API_URL}/api/v1/sandboxes/sb1/invoke/cancel"
    )
    assert len(requests) == 1
    assert requests[0].read() == b'{"invokeId":"inv-abc"}'


def test_invoke_cancel_rejects_empty_id(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        with pytest.raises(ValueError):
            sb.agent.invoke_cancel("")


# --- async smoke tests ----------------------------------------------------

@pytest.mark.asyncio
async def test_async_invoke_aggregates(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1",
        json=_SANDBOX_RESP,
    )
    httpx_mock.add_response(
        method="POST",
        url=f"{API_URL}/api/v1/sandboxes/sb1/invoke",
        content=_INVOKE_SSE_BODY,
        headers={"Content-Type": "text/event-stream"},
    )
    async with AsyncAgentTierClient(api_url=API_URL) as client:
        sb = await client.get_sandbox("sb1")
        result = await sb.agent.invoke({"prompt": "hi"})
    assert result.exit_code == 0
    assert result.stdout == "hello"


@pytest.mark.asyncio
async def test_async_configure_typed_result(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1",
        json=_SANDBOX_RESP,
    )
    httpx_mock.add_response(
        method="POST",
        url=f"{API_URL}/api/v1/sandboxes/sb1/configure",
        content=_CONFIGURE_SSE_BODY,
        headers={"Content-Type": "text/event-stream"},
    )
    async with AsyncAgentTierClient(api_url=API_URL) as client:
        sb = await client.get_sandbox("sb1")
        result = await sb.agent.configure(
            install_command=["pip", "install", "x"],
            entrypoint=["python", "/workspace/agent.py"],
        )
    assert result.install_command_hash == "abc"
    assert result.install_exit_code == 0
