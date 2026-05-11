# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Sandbox handle for lifecycle management, commands, and file operations."""

from __future__ import annotations

import time
from typing import Optional

import httpx

from agenttier.commands import CommandsAPI
from agenttier.files import FilesAPI
from agenttier.models import CommandResult, SandboxStatus


class Sandbox:
    """Handle to a remote sandbox with lifecycle, command, and file operations.

    Usage:
        sandbox = client.create_sandbox(template="general-coding", name="my-sandbox")
        sandbox.wait_until_running(timeout=60)
        result = sandbox.commands.run("ls /workspace")
        sandbox.files.write("/workspace/hello.txt", b"Hello, world!")
        sandbox.stop()
        sandbox.resume()
        sandbox.terminate()
    """

    def __init__(self, http: httpx.Client, sandbox_id: str, name: str, namespace: str) -> None:
        self._http = http
        self.id = sandbox_id
        self.name = name
        self.namespace = namespace
        self.commands = CommandsAPI(http, sandbox_id)
        self.files = FilesAPI(http, sandbox_id)

    @property
    def status(self) -> SandboxStatus:
        """Get the current sandbox status."""
        resp = self._http.get(f"/sandboxes/{self.id}")
        resp.raise_for_status()
        data = resp.json()
        return SandboxStatus(
            phase=data.get("status", "Unknown"),
            pod_name=data.get("podName"),
            pvc_name=data.get("pvcName"),
            resolved_template=data.get("templateRef"),
            restart_count=data.get("restartCount", 0),
            message=data.get("message"),
        )

    def wait_until_running(self, timeout: float = 60.0, poll_interval: float = 2.0) -> None:
        """Block until the sandbox reaches Running status.

        Args:
            timeout: Maximum seconds to wait.
            poll_interval: Seconds between status checks.

        Raises:
            TimeoutError: If the sandbox doesn't reach Running within the timeout.
            RuntimeError: If the sandbox enters Error state.
        """
        deadline = time.time() + timeout
        while time.time() < deadline:
            status = self.status
            if status.phase == "Running":
                return
            if status.phase == "Error":
                raise RuntimeError(f"Sandbox entered Error state: {status.message}")
            time.sleep(poll_interval)

        raise TimeoutError(f"Sandbox {self.id} did not reach Running within {timeout}s")

    def stop(self) -> None:
        """Stop the sandbox (preserves PVC)."""
        resp = self._http.post(f"/sandboxes/{self.id}/stop")
        resp.raise_for_status()

    def resume(self) -> None:
        """Resume a stopped sandbox."""
        resp = self._http.post(f"/sandboxes/{self.id}/resume")
        resp.raise_for_status()

    def terminate(self) -> None:
        """Permanently delete the sandbox and all its data."""
        resp = self._http.delete(f"/sandboxes/{self.id}")
        resp.raise_for_status()

    def clone(self, name: str) -> "Sandbox":
        """Clone this sandbox (creates a copy via VolumeSnapshot).

        Args:
            name: Name for the cloned sandbox.

        Returns:
            A new Sandbox handle for the clone.
        """
        resp = self._http.post(f"/sandboxes/{self.id}/clone", json={"name": name})
        resp.raise_for_status()
        data = resp.json()
        return Sandbox(self._http, data["sandboxId"], name, self.namespace)

    def exec(self, command: str, timeout: int = 30) -> CommandResult:
        """Execute a command in the sandbox (shortcut for commands.run).

        Args:
            command: Shell command to execute.
            timeout: Maximum execution time in seconds.

        Returns:
            CommandResult with stdout, stderr, and exit_code.
        """
        return self.commands.run(command, timeout=timeout)

    def __repr__(self) -> str:
        return f"Sandbox(id={self.id!r}, name={self.name!r}, namespace={self.namespace!r})"
