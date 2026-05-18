# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Tests for the file transfer wrappers on Sandbox + AsyncSandbox.

These exercise the public surface only; the Router-side behavior is covered
by router integration tests. We use pytest-httpx to stub HTTP responses.
"""

from __future__ import annotations

import pytest
from pytest_httpx import HTTPXMock

from agenttier import AgentTierClient, AsyncAgentTierClient

API_URL = "http://router.test"

# Response body the Router returns for GET /api/v1/sandboxes/sb1
_SANDBOX_RESP = {
    "sandboxId": "sb1",
    "name": "sb1",
    "namespace": "default",
    "status": "Running",
}


def _register_get_sandbox(httpx_mock: HTTPXMock) -> None:
    """Stub the GET the client makes from ``get_sandbox`` before handing back the handle."""
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1",
        json=_SANDBOX_RESP,
    )


def test_files_list_parses_entries(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1/files/?path=%2Fworkspace",
        json={
            "path": "/workspace",
            "entries": [
                {"name": "hello.txt", "size": 5, "isDir": False, "mode": "-rw-r--r--", "modifiedAt": 100},
                {"name": "subdir", "size": 4096, "isDir": True, "mode": "drwxr-xr-x", "modifiedAt": 200},
            ],
        },
    )
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        entries = sb.files.list()
    assert [e.name for e in entries] == ["hello.txt", "subdir"]
    assert entries[0].is_dir is False
    assert entries[1].is_dir is True
    assert entries[0].size == 5


def test_files_write_and_read_roundtrip(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="PUT",
        url=f"{API_URL}/api/v1/sandboxes/sb1/files/workspace/hello.txt",
        status_code=201,
        json={"path": "/workspace/hello.txt", "bytes": 5},
    )
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1/files/workspace/hello.txt",
        content=b"hello",
    )
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        sb.files.write("/workspace/hello.txt", "hello")
        body = sb.files.read("/workspace/hello.txt")
    assert body == b"hello"


def test_files_upload_from_disk(tmp_path, httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    src = tmp_path / "payload.bin"
    src.write_bytes(b"\x00\x01\x02 binary payload")
    httpx_mock.add_response(
        method="PUT",
        url=f"{API_URL}/api/v1/sandboxes/sb1/files/workspace/payload.bin",
        status_code=201,
        json={"path": "/workspace/payload.bin", "bytes": len(src.read_bytes())},
    )
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        n = sb.files.upload("/workspace/payload.bin", str(src))
    assert n == len(src.read_bytes())


def test_files_upload_rejects_oversize(tmp_path, httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        big = tmp_path / "too-big.bin"
        big.write_bytes(b"x" * (sb.files.MAX_BYTES + 1))
        with pytest.raises(ValueError, match="max"):
            sb.files.upload("/workspace/too-big.bin", str(big))


def test_files_list_rejects_empty_path(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        with pytest.raises(ValueError):
            sb.files.list("")


def test_files_read_rejects_directory_only_path(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        with pytest.raises(ValueError, match="file name"):
            sb.files.read("/")


@pytest.mark.asyncio
async def test_async_files_list_and_read(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1",
        json=_SANDBOX_RESP,
    )
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1/files/?path=%2Fworkspace",
        json={"path": "/workspace", "entries": [{"name": "a.txt", "size": 1}]},
    )
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1/files/workspace/a.txt",
        content=b"a",
    )
    async with AsyncAgentTierClient(api_url=API_URL) as client:
        sb = await client.get_sandbox("sb1")
        entries = await sb.files.list()
        data = await sb.files.read("/workspace/a.txt")
    assert [e.name for e in entries] == ["a.txt"]
    assert data == b"a"


def test_files_archive_streams_to_destination(tmp_path, httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    # The Router's /archive endpoint streams a real .zip; for the SDK
    # contract we just need to verify (a) the right URL is hit, (b) the
    # path query param is forwarded, and (c) bytes are written verbatim
    # to the destination. We use a sentinel byte string in place of a
    # real zip file — the SDK does not parse the stream.
    payload = b"PK\x03\x04 fake-zip-bytes"
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1/archive?path=%2Fworkspace",
        content=payload,
        headers={"Content-Type": "application/zip"},
    )
    out = tmp_path / "ws.zip"
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        n = sb.files.archive(str(out))
    assert n == len(payload)
    assert out.read_bytes() == payload


def test_files_archive_forwards_subpath(tmp_path, httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1/archive?path=%2Fworkspace%2Fsrc",
        content=b"PK\x03\x04",
    )
    out = tmp_path / "src.zip"
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        n = sb.files.archive(str(out), path="/workspace/src")
    assert n == 4


def test_files_archive_rejects_empty_path(httpx_mock: HTTPXMock) -> None:
    _register_get_sandbox(httpx_mock)
    with AgentTierClient(api_url=API_URL) as client:
        sb = client.get_sandbox("sb1")
        with pytest.raises(ValueError):
            sb.files.archive("/tmp/x.zip", path="")


@pytest.mark.asyncio
async def test_async_files_archive(tmp_path, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1",
        json=_SANDBOX_RESP,
    )
    httpx_mock.add_response(
        method="GET",
        url=f"{API_URL}/api/v1/sandboxes/sb1/archive?path=%2Fworkspace",
        content=b"PKzip",
    )
    out = tmp_path / "ws.zip"
    async with AsyncAgentTierClient(api_url=API_URL) as client:
        sb = await client.get_sandbox("sb1")
        n = await sb.files.archive(str(out))
    assert n == 5
    assert out.read_bytes() == b"PKzip"
