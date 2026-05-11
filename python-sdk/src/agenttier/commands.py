# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Command execution API for sandboxes."""

from __future__ import annotations

import httpx

from agenttier.models import CommandResult


class CommandsAPI:
    """Execute commands inside a sandbox.

    Usage:
        result = sandbox.commands.run("ls -la /workspace")
        print(result.stdout)
        print(f"Exit code: {result.exit_code}")
    """

    def __init__(self, http: httpx.Client, sandbox_id: str) -> None:
        self._http = http
        self._sandbox_id = sandbox_id

    def run(self, command: str, timeout: int = 30) -> CommandResult:
        """Execute a command and return the result.

        Args:
            command: Shell command to execute.
            timeout: Maximum execution time in seconds.

        Returns:
            CommandResult with stdout, stderr, and exit_code.

        Raises:
            httpx.HTTPStatusError: If the API returns an error.
        """
        resp = self._http.post(
            f"/sandboxes/{self._sandbox_id}/exec",
            json={"command": command, "timeout": timeout},
            timeout=timeout + 5,  # HTTP timeout slightly longer than command timeout
        )
        resp.raise_for_status()
        data = resp.json()

        return CommandResult(
            stdout=data.get("stdout", ""),
            stderr=data.get("stderr", ""),
            exit_code=data.get("exitCode", -1),
        )
