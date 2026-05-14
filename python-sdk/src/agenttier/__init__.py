# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""AgentTier Python SDK.

Isolated, persistent Kubernetes sandboxes for humans and AI agents, from Python.

Typical usage::

    from agenttier import AgentTierClient

    with AgentTierClient(api_url="https://agenttier.company.com") as client:
        sandbox = client.create_sandbox(template="general-coding", name="demo")
        sandbox.wait_until_running()
        print(sandbox.exec("uname -a").stdout)
        sandbox.terminate()

See :mod:`agenttier.async_client` for the ``async/await`` variant.
"""

from agenttier._version import __version__
from agenttier.async_client import AsyncAgentTierClient
from agenttier.async_sandbox import AsyncSandbox
from agenttier.auth import APIKeyAuth, AuthProvider, BearerTokenAuth, KubeconfigAuth
from agenttier.client import AgentTierClient
from agenttier.exceptions import (
    AgentTierError,
    APIError,
    AuthenticationError,
    AuthorizationError,
    ConflictError,
    NotFoundError,
    PolicyViolationError,
    SandboxErrorState,
    SandboxTimeoutError,
)
from agenttier.models import (
    AuditEvent,
    CommandResult,
    ConfigureResult,
    CreatedBy,
    CurrentUser,
    ForwardedPort,
    InvokeEvent,
    InvokeResult,
    SandboxPhase,
    SandboxSummary,
    Template,
    UsageAnalytics,
)
from agenttier.sandbox import Sandbox

__all__ = [
    "__version__",
    # Clients
    "AgentTierClient",
    "AsyncAgentTierClient",
    # Handles
    "Sandbox",
    "AsyncSandbox",
    # Auth
    "AuthProvider",
    "APIKeyAuth",
    "BearerTokenAuth",
    "KubeconfigAuth",
    # Models
    "AuditEvent",
    "CommandResult",
    "ConfigureResult",
    "CreatedBy",
    "CurrentUser",
    "ForwardedPort",
    "InvokeEvent",
    "InvokeResult",
    "SandboxPhase",
    "SandboxSummary",
    "Template",
    "UsageAnalytics",
    # Exceptions
    "AgentTierError",
    "APIError",
    "AuthenticationError",
    "AuthorizationError",
    "ConflictError",
    "NotFoundError",
    "PolicyViolationError",
    "SandboxErrorState",
    "SandboxTimeoutError",
]
