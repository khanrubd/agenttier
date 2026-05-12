# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Authentication providers for the AgentTier SDK.

Three concrete providers are shipped:

* :class:`APIKeyAuth` — sends ``X-API-Key``.
* :class:`BearerTokenAuth` — sends ``Authorization: Bearer <token>`` (OIDC JWT).
* :class:`KubeconfigAuth` — uses the in-cluster ServiceAccount token when
  available; falls back to unauthenticated (the Router's dev mode accepts this).

:func:`auto_detect_auth` picks the best available provider from environment
variables and file system state.
"""

from __future__ import annotations

import os
from abc import ABC, abstractmethod
from pathlib import Path
from typing import Optional

import httpx


class AuthProvider(ABC):
    """Attaches credentials to outgoing HTTP requests."""

    @abstractmethod
    def apply(self, request: httpx.Request) -> None:
        """Mutate ``request`` in place to carry credentials."""


class BearerTokenAuth(AuthProvider):
    """Static bearer token (typically an OIDC JWT)."""

    def __init__(self, token: str) -> None:
        if not token:
            raise ValueError("token must be a non-empty string")
        self._token = token

    def apply(self, request: httpx.Request) -> None:
        request.headers["Authorization"] = f"Bearer {self._token}"


class APIKeyAuth(AuthProvider):
    """AgentTier API key."""

    def __init__(self, api_key: str) -> None:
        if not api_key:
            raise ValueError("api_key must be a non-empty string")
        self._api_key = api_key

    def apply(self, request: httpx.Request) -> None:
        request.headers["X-API-Key"] = self._api_key


# Standard in-cluster ServiceAccount token path (Kubernetes Downward API).
_IN_CLUSTER_TOKEN_PATH = Path("/var/run/secrets/kubernetes.io/serviceaccount/token")


class KubeconfigAuth(AuthProvider):
    """In-cluster ServiceAccount token, if one is mounted.

    A proper kubeconfig parser is intentionally out of scope; if you need
    kubeconfig-driven auth against a remote cluster, extract the token
    yourself and pass it to :class:`BearerTokenAuth`.
    """

    def __init__(self, token_path: Optional[str | Path] = None) -> None:
        path = Path(token_path) if token_path else _IN_CLUSTER_TOKEN_PATH
        self._token: Optional[str] = None
        if path.exists():
            try:
                self._token = path.read_text().strip() or None
            except OSError:
                # Permission problems etc. — fall back to unauthenticated.
                self._token = None

    def apply(self, request: httpx.Request) -> None:
        if self._token:
            request.headers["Authorization"] = f"Bearer {self._token}"


def auto_detect_auth() -> AuthProvider:
    """Return the best available auth provider for the current environment.

    Priority order:

    1. ``AGENTTIER_API_KEY``
    2. ``AGENTTIER_TOKEN`` (bearer / OIDC JWT)
    3. In-cluster ServiceAccount token at ``/var/run/secrets/...``
    4. Unauthenticated — the Router accepts this only in dev mode (no OIDC
       configured); production deployments will return 401.
    """
    api_key = os.environ.get("AGENTTIER_API_KEY")
    if api_key:
        return APIKeyAuth(api_key)

    token = os.environ.get("AGENTTIER_TOKEN")
    if token:
        return BearerTokenAuth(token)

    return KubeconfigAuth()
