# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Authentication providers for the AgentTier SDK."""

from __future__ import annotations

import os
from abc import ABC, abstractmethod
from pathlib import Path
from typing import Optional

import httpx


class AuthProvider(ABC):
    """Base class for authentication providers."""

    @abstractmethod
    def apply(self, request: httpx.Request) -> httpx.Request:
        """Apply authentication to an HTTP request."""
        ...


class BearerTokenAuth(AuthProvider):
    """Authenticate with a static bearer token (OIDC JWT)."""

    def __init__(self, token: str) -> None:
        self._token = token

    def apply(self, request: httpx.Request) -> httpx.Request:
        request.headers["Authorization"] = f"Bearer {self._token}"
        return request


class APIKeyAuth(AuthProvider):
    """Authenticate with an API key."""

    def __init__(self, api_key: str) -> None:
        self._api_key = api_key

    def apply(self, request: httpx.Request) -> httpx.Request:
        request.headers["X-API-Key"] = self._api_key
        return request


class KubeconfigAuth(AuthProvider):
    """Authenticate using a kubeconfig file (extract ServiceAccount token)."""

    def __init__(self, kubeconfig_path: Optional[str] = None) -> None:
        self._token = self._load_token(kubeconfig_path)

    def apply(self, request: httpx.Request) -> httpx.Request:
        if self._token:
            request.headers["Authorization"] = f"Bearer {self._token}"
        return request

    def _load_token(self, kubeconfig_path: Optional[str]) -> Optional[str]:
        """Load token from kubeconfig or in-cluster service account."""
        # Try in-cluster token first
        sa_token_path = Path("/var/run/secrets/kubernetes.io/serviceaccount/token")
        if sa_token_path.exists():
            return sa_token_path.read_text().strip()

        # Try kubeconfig
        path = kubeconfig_path or os.environ.get("KUBECONFIG", str(Path.home() / ".kube/config"))
        if Path(path).exists():
            # Simplified: in production, parse YAML and extract current-context token
            # For now, return None (user should use BearerTokenAuth or APIKeyAuth)
            return None

        return None


def auto_detect_auth() -> AuthProvider:
    """Auto-detect the best authentication method from environment.

    Priority:
    1. AGENTTIER_API_KEY environment variable
    2. AGENTTIER_TOKEN environment variable
    3. Kubeconfig / in-cluster ServiceAccount token
    """
    api_key = os.environ.get("AGENTTIER_API_KEY")
    if api_key:
        return APIKeyAuth(api_key)

    token = os.environ.get("AGENTTIER_TOKEN")
    if token:
        return BearerTokenAuth(token)

    return KubeconfigAuth()
