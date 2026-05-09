# Copyright 2024 AgentLoft Authors.
# SPDX-License-Identifier: Apache-2.0

"""AgentLoft client for managing sandboxes."""

from __future__ import annotations

from typing import Optional

import httpx

from agentloft.auth import AuthProvider, auto_detect_auth
from agentloft.models import SandboxSpec, Template
from agentloft.sandbox import Sandbox


class AgentLoftClient:
    """High-level client for the AgentLoft REST API.

    Usage:
        client = AgentLoftClient(api_url="https://agentloft.company.com")
        sandbox = client.create_sandbox(template="general-coding", name="my-sandbox")
        sandbox.wait_until_running()
        result = sandbox.commands.run("echo hello")
        print(result.stdout)
        sandbox.terminate()
    """

    def __init__(
        self,
        api_url: str,
        auth: Optional[AuthProvider] = None,
        timeout: float = 30.0,
    ) -> None:
        """Initialize the AgentLoft client.

        Args:
            api_url: Base URL of the AgentLoft API (e.g., "https://agentloft.company.com")
            auth: Authentication provider. Auto-detected if not provided.
            timeout: Default request timeout in seconds.
        """
        self._api_url = api_url.rstrip("/")
        self._auth = auth or auto_detect_auth()
        self._http = httpx.Client(
            base_url=f"{self._api_url}/api/v1",
            timeout=timeout,
            event_hooks={"request": [self._apply_auth]},
        )

    def _apply_auth(self, request: httpx.Request) -> None:
        """Apply authentication to outgoing requests."""
        self._auth.apply(request)

    def create_sandbox(
        self,
        template: str,
        name: str,
        namespace: str = "default",
        timeout: Optional[str] = None,
        idle_timeout: Optional[str] = None,
        storage_size: Optional[str] = None,
    ) -> Sandbox:
        """Create a new sandbox from a template.

        Args:
            template: Name of the SandboxTemplate to use.
            name: Name for the new sandbox.
            namespace: Kubernetes namespace.
            timeout: Max runtime duration (e.g., "8h"). None = use template default.
            idle_timeout: Max idle duration (e.g., "1h"). None = use template default.
            storage_size: PVC size (e.g., "20Gi"). None = use template default.

        Returns:
            A Sandbox handle for the created sandbox.
        """
        body: dict = {
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

        resp = self._http.post("/sandboxes", json=body)
        resp.raise_for_status()
        data = resp.json()

        return Sandbox(self._http, data["sandboxId"], data.get("name", name), namespace)

    def list_sandboxes(
        self,
        namespace: Optional[str] = None,
        status: Optional[str] = None,
        template: Optional[str] = None,
    ) -> list[SandboxSpec]:
        """List sandboxes with optional filtering.

        Args:
            namespace: Filter by namespace.
            status: Filter by status (Running, Stopped, etc.).
            template: Filter by template name.

        Returns:
            List of sandbox specs.
        """
        params: dict[str, str] = {}
        if namespace:
            params["namespace"] = namespace
        if status:
            params["status"] = status
        if template:
            params["template"] = template

        resp = self._http.get("/sandboxes", params=params)
        resp.raise_for_status()
        data = resp.json()

        return [SandboxSpec(**s) for s in data.get("sandboxes", [])]

    def get_sandbox(self, sandbox_id: str) -> Sandbox:
        """Get a sandbox by ID.

        Args:
            sandbox_id: The sandbox identifier.

        Returns:
            A Sandbox handle.
        """
        resp = self._http.get(f"/sandboxes/{sandbox_id}")
        resp.raise_for_status()
        data = resp.json()

        return Sandbox(
            self._http,
            data["sandboxId"],
            data.get("name", sandbox_id),
            data.get("namespace", "default"),
        )

    def list_templates(self) -> list[Template]:
        """List available sandbox templates.

        Returns:
            List of templates.
        """
        resp = self._http.get("/templates")
        resp.raise_for_status()
        data = resp.json()

        return [Template(**t) for t in data.get("templates", [])]

    def close(self) -> None:
        """Close the HTTP client."""
        self._http.close()

    def __enter__(self) -> "AgentLoftClient":
        return self

    def __exit__(self, *args: object) -> None:
        self.close()
