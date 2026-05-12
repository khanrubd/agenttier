# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Client-level integration tests using pytest-httpx to mock the Router."""

from __future__ import annotations

import pytest
from pytest_httpx import HTTPXMock

from agenttier import (
    AgentTierClient,
    APIKeyAuth,
    AuthenticationError,
    AuthorizationError,
    ConflictError,
    NotFoundError,
    PolicyViolationError,
    Sandbox,
    SandboxErrorState,
    SandboxPhase,
    SandboxTimeoutError,
)

API_URL = "http://router.test"
BASE = f"{API_URL}/api/v1"


@pytest.fixture
def client() -> AgentTierClient:
    # APIKeyAuth keeps the auth header predictable across tests; we don't
    # touch environment variables here.
    return AgentTierClient(api_url=API_URL, auth=APIKeyAuth("test-key"))


def test_create_sandbox_returns_typed_handle(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/sandboxes",
        json={"sandboxId": "sbx-1", "name": "demo", "namespace": "default", "status": "Creating"},
        status_code=201,
    )
    sandbox = client.create_sandbox(template="general-coding", name="demo")
    assert isinstance(sandbox, Sandbox)
    assert sandbox.id == "sbx-1"
    assert sandbox.namespace == "default"


def test_create_sandbox_sends_template_and_auth_header(
    client: AgentTierClient, httpx_mock: HTTPXMock
) -> None:
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/sandboxes",
        json={"sandboxId": "sbx-1", "name": "demo", "namespace": "default", "status": "Creating"},
    )
    client.create_sandbox(template="claude-code-bedrock", name="demo", idle_timeout="30m")
    req = httpx_mock.get_request()
    assert req is not None
    assert req.headers["X-API-Key"] == "test-key"
    body = req.read().decode()
    # Make sure the body carries templateRef.kind so the server can resolve it.
    assert '"kind":"ClusterSandboxTemplate"' in body.replace(" ", "")
    assert '"idleTimeout":"30m"' in body.replace(" ", "")


def test_list_sandboxes_parses_camel_case(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes",
        json={
            "sandboxes": [
                {
                    "sandboxId": "sbx-1",
                    "name": "one",
                    "namespace": "default",
                    "status": "Running",
                    "templateRef": "general-coding",
                }
            ]
        },
    )
    sandboxes = client.list_sandboxes()
    assert len(sandboxes) == 1
    assert sandboxes[0].sandbox_id == "sbx-1"
    assert sandboxes[0].template_ref == "general-coding"
    assert sandboxes[0].phase is SandboxPhase.RUNNING


def test_list_sandboxes_tolerates_null_list(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    # The Router returns ``null`` for "no sandboxes" in some shapes; do not blow up.
    httpx_mock.add_response(method="GET", url=f"{BASE}/sandboxes", json={"sandboxes": None})
    assert client.list_sandboxes() == []


def test_401_maps_to_authentication_error(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET", url=f"{BASE}/sandboxes", status_code=401, json={"error": "invalid_api_key"}
    )
    with pytest.raises(AuthenticationError):
        client.list_sandboxes()


def test_403_maps_to_authorization_error(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET", url=f"{BASE}/sandboxes/x", status_code=403, json={"error": "forbidden"}
    )
    with pytest.raises(AuthorizationError):
        client.get_sandbox("x")


def test_policy_violation_surfaces_structured_body(
    client: AgentTierClient, httpx_mock: HTTPXMock
) -> None:
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/sandboxes",
        status_code=403,
        json={
            "error": "policy_violation",
            "violations": [{"code": "user_quota_exceeded", "message": "too many sandboxes"}],
        },
    )
    with pytest.raises(PolicyViolationError) as info:
        client.create_sandbox(template="t", name="demo")
    assert info.value.violations[0]["code"] == "user_quota_exceeded"


def test_404_maps_to_not_found(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(method="GET", url=f"{BASE}/sandboxes/nope", status_code=404, json={})
    with pytest.raises(NotFoundError):
        client.get_sandbox("nope")


def test_409_maps_to_conflict(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Running"},
    )
    sandbox = client.get_sandbox("x")
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/sandboxes/x/stop",
        status_code=409,
        json={"error": "cannot stop sandbox in phase Stopped"},
    )
    with pytest.raises(ConflictError):
        sandbox.stop()


def test_user_agent_header_present(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(method="GET", url=f"{BASE}/sandboxes", json={"sandboxes": []})
    client.list_sandboxes()
    req = httpx_mock.get_request()
    assert req is not None
    assert "agenttier-python-sdk" in req.headers["User-Agent"]


def test_context_manager_closes_http_client() -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    with client:
        pass
    # httpx marks the transport as closed; re-calling close should be safe.
    client.close()


def test_create_sandbox_validates_arguments() -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    with pytest.raises(ValueError):
        client.create_sandbox(template="", name="demo")
    with pytest.raises(ValueError):
        client.create_sandbox(template="t", name="")


def test_forward_port_validates_range(httpx_mock: HTTPXMock) -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Running"},
    )
    sandbox = client.get_sandbox("x")
    with pytest.raises(ValueError):
        sandbox.forward_port(0)
    with pytest.raises(ValueError):
        sandbox.forward_port(70000)


def test_forward_port_parses_response(httpx_mock: HTTPXMock) -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Running"},
    )
    sandbox = client.get_sandbox("x")
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/sandboxes/x/ports",
        status_code=201,
        json={
            "port": 8080,
            "protocol": "http",
            "internalUrl": "http://pf-x-8080.default.svc.cluster.local:8080",
            "previewUrl": "https://sandbox-x-8080.preview.example/",
        },
    )
    fp = sandbox.forward_port(8080)
    assert fp.port == 8080
    assert fp.preview_url == "https://sandbox-x-8080.preview.example/"


def test_wait_until_running_polls_until_ready(httpx_mock: HTTPXMock) -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Creating"},
    )
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Running"},
    )
    sandbox = Sandbox(client._http, "x", "x", "default")
    summary = sandbox.wait_until_running(timeout=5.0, poll_interval=0.0)
    assert summary.phase is SandboxPhase.RUNNING


def test_wait_until_running_raises_on_error_state(httpx_mock: HTTPXMock) -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={
            "sandboxId": "x",
            "name": "x",
            "namespace": "default",
            "status": "Error",
            "message": "image pull failed",
        },
    )
    sandbox = Sandbox(client._http, "x", "x", "default")
    with pytest.raises(SandboxErrorState, match="image pull failed"):
        sandbox.wait_until_running(timeout=1.0, poll_interval=0.0)


def test_wait_until_running_times_out(httpx_mock: HTTPXMock) -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    # pytest-httpx lets us add an unlimited matcher by not specifying a
    # call count cap for the same URL across retries.
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Creating"},
        is_reusable=True,
    )
    sandbox = Sandbox(client._http, "x", "x", "default")
    with pytest.raises(SandboxTimeoutError):
        sandbox.wait_until_running(timeout=0.2, poll_interval=0.05)


def test_exec_result_parses_exit_code(httpx_mock: HTTPXMock) -> None:
    client = AgentTierClient(api_url=API_URL, auth=APIKeyAuth("k"))
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/sandboxes/x",
        json={"sandboxId": "x", "name": "x", "namespace": "default", "status": "Running"},
    )
    sandbox = client.get_sandbox("x")
    httpx_mock.add_response(
        method="POST",
        url=f"{BASE}/sandboxes/x/exec",
        json={"stdout": "hi\n", "stderr": "", "exitCode": 0},
    )
    result = sandbox.exec("echo hi")
    assert result.stdout == "hi\n"
    assert result.exit_code == 0


def test_current_user_parses_is_admin(client: AgentTierClient, httpx_mock: HTTPXMock) -> None:
    httpx_mock.add_response(
        method="GET",
        url=f"{BASE}/user/me",
        json={"sub": "dev-user", "email": "d@e", "name": "Dev", "groups": ["a"], "isAdmin": True},
    )
    me = client.current_user()
    assert me.is_admin is True
    assert me.sub == "dev-user"
