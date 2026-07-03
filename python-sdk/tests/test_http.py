# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Unit tests for agenttier._http — error-deserialization and deprecation-
warning paths not already exercised end-to-end via test_client.py /
test_async_client.py (which cover the 401/403/404/409 mapping)."""

from __future__ import annotations

import warnings

import httpx
import pytest

from agenttier._http import (
    _decode_body,
    _extract_message,
    _warned_deprecations,
    default_user_agent,
    raise_for_status,
    warn_if_deprecated,
)
from agenttier.exceptions import APIError, AuthorizationError


def _response(status_code: int, **kwargs) -> httpx.Response:
    request = httpx.Request("GET", "http://test/x")
    return httpx.Response(status_code, request=request, **kwargs)


def test_raise_for_status_success_is_noop() -> None:
    raise_for_status(_response(200, json={"ok": True}))  # must not raise


def test_raise_for_status_generic_5xx_maps_to_api_error() -> None:
    resp = _response(500, json={"error": "internal error"})
    with pytest.raises(APIError) as exc_info:
        raise_for_status(resp)
    assert exc_info.value.status_code == 500
    assert exc_info.value.body == {"error": "internal error"}
    assert "internal error" in str(exc_info.value)


def test_raise_for_status_403_without_policy_violation_body() -> None:
    # 403 without the specific policy_violation error code should fall back
    # to plain AuthorizationError, not PolicyViolationError.
    resp = _response(403, json={"error": "forbidden"})
    with pytest.raises(AuthorizationError):
        raise_for_status(resp)


def test_raise_for_status_uses_reason_phrase_when_body_empty() -> None:
    resp = _response(500)
    with pytest.raises(APIError) as exc_info:
        raise_for_status(resp)
    # No body at all — message falls back to the HTTP reason phrase or the
    # generic "request failed" text, but must not be empty/crash.
    assert str(exc_info.value)


def test_decode_body_returns_none_for_empty_content() -> None:
    assert _decode_body(_response(204)) is None


def test_decode_body_falls_back_to_text_on_invalid_json() -> None:
    resp = _response(500, content=b"not json {{{")
    assert _decode_body(resp) == "not json {{{"


def test_decode_body_parses_valid_json() -> None:
    resp = _response(400, json={"error": "bad"})
    assert _decode_body(resp) == {"error": "bad"}


def test_extract_message_prefers_error_key() -> None:
    assert _extract_message({"error": "e1", "message": "m1"}, "fallback") == "e1"


def test_extract_message_falls_back_to_message_key() -> None:
    assert _extract_message({"message": "m1"}, "fallback") == "m1"


def test_extract_message_string_body() -> None:
    assert _extract_message("plain text error", "fallback") == "plain text error"


def test_extract_message_falls_back_when_body_has_no_usable_field() -> None:
    assert _extract_message({"unrelated": "x"}, "fallback text") == "fallback text"


def test_extract_message_falls_back_to_generic_when_everything_empty() -> None:
    assert _extract_message(None, "") == "request failed"


def test_default_user_agent_format() -> None:
    assert default_user_agent("1.2.3") == "agenttier-python-sdk/1.2.3"


def test_warn_if_deprecated_emits_warning_once_per_endpoint() -> None:
    _warned_deprecations.clear()
    resp = _response(200, headers={"Deprecation": "true", "Sunset": "2027-01-01"})
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        warn_if_deprecated(resp)
        warn_if_deprecated(resp)  # second call on the same (method, url) is a no-op
    deprecation_warnings = [w for w in caught if issubclass(w.category, DeprecationWarning)]
    assert len(deprecation_warnings) == 1
    assert "deprecated" in str(deprecation_warnings[0].message)
    assert "2027-01-01" in str(deprecation_warnings[0].message)


def test_warn_if_deprecated_includes_successor_link() -> None:
    _warned_deprecations.clear()
    resp = _response(
        200,
        headers={
            "Deprecation": "true",
            "Link": '<https://docs.example.com/v2>; rel="successor-version"',
        },
    )
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        warn_if_deprecated(resp)
    assert any("successor-version" in str(w.message) for w in caught)


def test_warn_if_deprecated_noop_when_header_absent() -> None:
    _warned_deprecations.clear()
    resp = _response(200)
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        warn_if_deprecated(resp)
    assert len(caught) == 0


def test_warn_if_deprecated_respects_opt_out_env_var(monkeypatch: pytest.MonkeyPatch) -> None:
    _warned_deprecations.clear()
    monkeypatch.setenv("AGENTTIER_DEPRECATION_WARNINGS", "off")
    resp = _response(200, headers={"Deprecation": "true"})
    with warnings.catch_warnings(record=True) as caught:
        warnings.simplefilter("always")
        warn_if_deprecated(resp)
    assert len(caught) == 0
