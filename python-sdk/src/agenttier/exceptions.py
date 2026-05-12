# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Exception hierarchy for the AgentTier SDK.

All SDK errors inherit from ``AgentTierError`` so callers can catch everything
with a single except-clause.
"""

from __future__ import annotations

from typing import Any


class AgentTierError(Exception):
    """Base class for all SDK errors."""


class AuthenticationError(AgentTierError):
    """Raised when authentication fails (401)."""


class AuthorizationError(AgentTierError):
    """Raised when the caller is not permitted to perform an action (403)."""


class NotFoundError(AgentTierError):
    """Raised when a requested resource does not exist (404)."""


class ConflictError(AgentTierError):
    """Raised when an operation conflicts with the resource's current state (409)."""


class PolicyViolationError(AgentTierError):
    """Raised when a governance policy rejects a sandbox create.

    The server returns a structured body with per-rule violation codes; those
    are preserved on the exception so callers can inspect them programmatically.
    """

    def __init__(self, message: str, violations: list[dict[str, Any]]) -> None:
        super().__init__(message)
        self.violations = violations


class SandboxTimeoutError(AgentTierError, TimeoutError):
    """Raised when ``wait_until_running`` times out."""


class SandboxErrorState(AgentTierError):
    """Raised when a sandbox transitions to the Error phase while being awaited."""


class APIError(AgentTierError):
    """Generic HTTP error wrapping the server's response body."""

    def __init__(self, status_code: int, message: str, body: Any = None) -> None:
        super().__init__(f"{status_code}: {message}")
        self.status_code = status_code
        self.body = body
