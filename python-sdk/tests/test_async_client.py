# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Async-client integration tests."""

from __future__ import annotations

import pytest
from pytest_httpx import HTTPXMock

from agenttier import (
    APIKeyAuth,
    AsyncAgentTierClient,
    AsyncSandbox,
    AuthenticationError,
    PolicyViolationError,
    SandboxPhase,
)

API_URL = "http://async.router.test"
BASE = f"{API_URL}/api/v1"


async def _make_client() -> AsyncAgentTierClient:
    return AsyncAgentTierClient(api_url=API_URL, auth=APIKeyAuth("test-key"))


async def test_async_list_sandboxes(httpx_mock: HTTPXMock) -> None:
    client = await _make_client()
    try:
        httpx_mock.add_response(
            method="GET",
            url=f"{BASE}/sandboxes",
            json={
                "sandboxes": [
                    {"sandboxId": "a", "name": "a", "namespace": "default", "status": "Running"}
                ]
            },
        )
        items = await client.list_sandboxes()
        assert len(items) == 1
        assert items[0].phase is SandboxPhase.RUNNING
    finally:
        await client.close()


async def test_async_context_manager_closes(httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(method="GET", url=f"{BASE}/sandboxes", json={"sandboxes": []})
    async with AsyncAgentTierClient(api_url=API_URL, auth=APIKeyAuth("k")) as client:
        assert await client.list_sandboxes() == []


async def test_async_401_maps_to_authentication_error(httpx_mock: HTTPXMock) -> None:
    async with AsyncAgentTierClient(api_url=API_URL, auth=APIKeyAuth("bad")) as client:
        httpx_mock.add_response(
            method="GET", url=f"{BASE}/user/me", status_code=401, json={"error": "bad"}
        )
        with pytest.raises(AuthenticationError):
            await client.current_user()


async def test_async_policy_violation(httpx_mock: HTTPXMock) -> None:
    async with AsyncAgentTierClient(api_url=API_URL, auth=APIKeyAuth("k")) as client:
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE}/sandboxes",
            status_code=403,
            json={
                "error": "policy_violation",
                "violations": [{"code": "namespace_quota_exceeded", "message": "too many"}],
            },
        )
        with pytest.raises(PolicyViolationError):
            await client.create_sandbox(template="t", name="x")


async def test_async_exec(httpx_mock: HTTPXMock) -> None:
    async with AsyncAgentTierClient(api_url=API_URL, auth=APIKeyAuth("k")) as client:
        httpx_mock.add_response(
            method="GET",
            url=f"{BASE}/sandboxes/x",
            json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Running"},
        )
        sandbox = await client.get_sandbox("x")
        assert isinstance(sandbox, AsyncSandbox)
        httpx_mock.add_response(
            method="POST",
            url=f"{BASE}/sandboxes/x/exec",
            json={"stdout": "hello\n", "stderr": "", "exitCode": 0},
        )
        result = await sandbox.exec("echo hello")
        assert result.stdout == "hello\n"
