# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Sync sandbox handle.

Use :meth:`AgentTierClient.create_sandbox` / :meth:`AgentTierClient.get_sandbox`
to obtain instances — don't construct :class:`Sandbox` directly.
"""

from __future__ import annotations

import time
from typing import TYPE_CHECKING, Optional

from agenttier._http import raise_for_status
from agenttier.exceptions import SandboxErrorState, SandboxTimeoutError
from agenttier.models import CommandResult, ForwardedPort, SandboxPhase, SandboxSummary

if TYPE_CHECKING:  # pragma: no cover
    import httpx

_DEFAULT_WAIT_TIMEOUT = 120.0
_DEFAULT_POLL_INTERVAL = 2.0


class Sandbox:
    """Remote handle for a sandbox running in an AgentTier cluster."""

    def __init__(
        self,
        http: "httpx.Client",
        sandbox_id: str,
        name: str,
        namespace: str,
    ) -> None:
        self._http = http
        self.id = sandbox_id
        self.name = name
        self.namespace = namespace

    # ------- state -------------------------------------------------------

    def status(self) -> SandboxSummary:
        """Fetch the latest status from the server."""
        resp = self._http.get(f"/sandboxes/{self.id}")
        raise_for_status(resp)
        return SandboxSummary.model_validate(resp.json())

    @property
    def phase(self) -> SandboxPhase:
        """Shortcut returning the typed phase of the current status."""
        return self.status().phase

    def wait_until_running(
        self,
        timeout: float = _DEFAULT_WAIT_TIMEOUT,
        poll_interval: float = _DEFAULT_POLL_INTERVAL,
    ) -> SandboxSummary:
        """Block until the sandbox reaches ``Running``.

        Returns the final :class:`SandboxSummary` on success.

        Raises :class:`SandboxTimeoutError` on timeout and
        :class:`SandboxErrorState` if the sandbox transitions to Error.
        """
        deadline = time.monotonic() + timeout
        last: Optional[SandboxSummary] = None
        while time.monotonic() < deadline:
            last = self.status()
            if last.phase is SandboxPhase.RUNNING:
                return last
            if last.phase is SandboxPhase.ERROR:
                raise SandboxErrorState(last.message or f"sandbox {self.id} entered Error state")
            time.sleep(poll_interval)
        raise SandboxTimeoutError(
            f"sandbox {self.id} did not reach Running within {timeout:.0f}s "
            f"(last phase: {last.phase.value if last else 'unknown'})"
        )

    # ------- lifecycle ---------------------------------------------------

    def stop(self) -> None:
        """Delete the sandbox Pod while preserving the PVC."""
        resp = self._http.post(f"/sandboxes/{self.id}/stop")
        raise_for_status(resp)

    def resume(self) -> None:
        """Re-create the Pod for a stopped sandbox; re-uses the same PVC."""
        resp = self._http.post(f"/sandboxes/{self.id}/resume")
        raise_for_status(resp)

    def terminate(self) -> None:
        """Permanently delete the sandbox and its workspace."""
        resp = self._http.delete(f"/sandboxes/{self.id}")
        raise_for_status(resp)

    # Alias kept for consistency with the REST name.
    delete = terminate

    # ------- execution ---------------------------------------------------

    def exec(self, command: str, timeout: int = 30) -> CommandResult:
        """Run a shell command inside the sandbox and wait for the result.

        ``timeout`` is applied both server-side (to the exec) and on the HTTP
        call (with a small buffer for overhead).
        """
        if not command:
            raise ValueError("command must be a non-empty string")
        if timeout <= 0:
            raise ValueError("timeout must be > 0")
        # Give the HTTP call a small buffer over the server-side exec timeout
        # so the server error bubbles up instead of httpx cutting us off.
        resp = self._http.post(
            f"/sandboxes/{self.id}/exec",
            json={"command": command, "timeout": timeout},
            timeout=timeout + 10,
        )
        raise_for_status(resp)
        return CommandResult.model_validate(resp.json())

    # ------- port forwarding --------------------------------------------

    def list_ports(self) -> list[ForwardedPort]:
        """Return the ports currently forwarded from this sandbox."""
        resp = self._http.get(f"/sandboxes/{self.id}/ports")
        raise_for_status(resp)
        ports = resp.json().get("ports") or []
        return [ForwardedPort.model_validate(p) for p in ports]

    def forward_port(self, port: int, protocol: str = "http") -> ForwardedPort:
        """Expose a container port via a ClusterIP Service (and Ingress if configured)."""
        if not 1 <= port <= 65535:
            raise ValueError("port must be between 1 and 65535")
        resp = self._http.post(
            f"/sandboxes/{self.id}/ports",
            json={"port": port, "protocol": protocol},
        )
        raise_for_status(resp)
        return ForwardedPort.model_validate(resp.json())

    def remove_port(self, port: int) -> None:
        """Tear down a previously-forwarded port."""
        resp = self._http.delete(f"/sandboxes/{self.id}/ports/{port}")
        raise_for_status(resp)

    # ------- misc --------------------------------------------------------

    def __repr__(self) -> str:
        return f"Sandbox(id={self.id!r}, name={self.name!r}, namespace={self.namespace!r})"
