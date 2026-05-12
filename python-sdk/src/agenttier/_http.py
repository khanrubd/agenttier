# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Internal HTTP utilities.

Kept private (underscore prefix) so we can change the shape freely without a
compat guarantee. The public clients are the only consumers.
"""

from __future__ import annotations

from typing import Any

import httpx

from agenttier.exceptions import (
    APIError,
    AuthenticationError,
    AuthorizationError,
    ConflictError,
    NotFoundError,
    PolicyViolationError,
)


def _decode_body(response: httpx.Response) -> Any:
    """Return a JSON body, a text body, or None, whichever succeeds."""
    if not response.content:
        return None
    try:
        return response.json()
    except ValueError:
        return response.text or None


def raise_for_status(response: httpx.Response) -> None:
    """Translate non-2xx responses into typed SDK exceptions.

    The Router returns structured JSON error bodies (``{"error": ..., ...}``)
    so we can offer crisp exception types without parsing error strings.
    """
    if response.is_success:
        return

    body = _decode_body(response)
    status = response.status_code
    message = _extract_message(body, response.reason_phrase)

    if status == 401:
        raise AuthenticationError(message)
    if status == 403:
        if isinstance(body, dict) and body.get("error") == "policy_violation":
            raise PolicyViolationError(message, list(body.get("violations", [])))
        raise AuthorizationError(message)
    if status == 404:
        raise NotFoundError(message)
    if status == 409:
        raise ConflictError(message)
    raise APIError(status, message, body)


def _extract_message(body: Any, fallback: str) -> str:
    if isinstance(body, dict):
        for key in ("error", "message"):
            v = body.get(key)
            if isinstance(v, str) and v:
                return v
    if isinstance(body, str) and body:
        return body
    return fallback or "request failed"


def default_user_agent(version: str) -> str:
    return f"agenttier-python-sdk/{version}"
