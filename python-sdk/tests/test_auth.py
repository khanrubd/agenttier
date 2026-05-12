# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Auth-provider unit tests."""

from __future__ import annotations

from pathlib import Path

import httpx
import pytest

from agenttier.auth import (
    APIKeyAuth,
    BearerTokenAuth,
    KubeconfigAuth,
    auto_detect_auth,
)


def _request() -> httpx.Request:
    return httpx.Request("GET", "http://example/api/v1/ping")


def test_api_key_sets_header() -> None:
    provider = APIKeyAuth("sk_live_abc")
    req = _request()
    provider.apply(req)
    assert req.headers["X-API-Key"] == "sk_live_abc"


def test_bearer_sets_header() -> None:
    provider = BearerTokenAuth("jwt-token-value")
    req = _request()
    provider.apply(req)
    assert req.headers["Authorization"] == "Bearer jwt-token-value"


def test_auth_providers_reject_empty_credentials() -> None:
    with pytest.raises(ValueError):
        APIKeyAuth("")
    with pytest.raises(ValueError):
        BearerTokenAuth("")


def test_kubeconfig_auth_is_noop_when_token_missing(tmp_path: Path) -> None:
    missing = tmp_path / "does-not-exist"
    provider = KubeconfigAuth(token_path=missing)
    req = _request()
    provider.apply(req)
    assert "Authorization" not in req.headers


def test_kubeconfig_auth_reads_token_file(tmp_path: Path) -> None:
    token = tmp_path / "token"
    token.write_text("in-cluster-token\n")
    provider = KubeconfigAuth(token_path=token)
    req = _request()
    provider.apply(req)
    assert req.headers["Authorization"] == "Bearer in-cluster-token"


def test_auto_detect_prefers_api_key_over_token(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("AGENTTIER_API_KEY", "k")
    monkeypatch.setenv("AGENTTIER_TOKEN", "t")
    provider = auto_detect_auth()
    assert isinstance(provider, APIKeyAuth)


def test_auto_detect_uses_token_when_no_api_key(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("AGENTTIER_API_KEY", raising=False)
    monkeypatch.setenv("AGENTTIER_TOKEN", "t")
    provider = auto_detect_auth()
    assert isinstance(provider, BearerTokenAuth)


def test_auto_detect_falls_back_to_kubeconfig(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("AGENTTIER_API_KEY", raising=False)
    monkeypatch.delenv("AGENTTIER_TOKEN", raising=False)
    provider = auto_detect_auth()
    assert isinstance(provider, KubeconfigAuth)
