# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Internal HTTP utilities.

Kept private (underscore prefix) so we can change the shape freely without a
compat guarantee. The public clients are the only consumers.
"""

from __future__ import annotations

import os
import warnings
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

# Endpoints we've already warned about (method, url) so a deprecation notice
# fires at most once per process per endpoint.
_warned_deprecations: set[tuple[str, str]] = set()


def warn_if_deprecated(response: httpx.Response) -> None:
    """Emit a one-time DeprecationWarning when the Router flags an endpoint.

    The Router stamps ``Deprecation: true`` (+ optional ``Sunset``) on
    endpoints superseded by a newer API version. We surface that to SDK users
    once per endpoint per process. Silence with
    ``AGENTTIER_DEPRECATION_WARNINGS=off``.
    """
    if response.headers.get("Deprecation", "").lower() != "true":
        return
    if os.environ.get("AGENTTIER_DEPRECATION_WARNINGS", "").lower() == "off":
        return
    method = response.request.method if response.request is not None else ""
    url = str(response.url)
    key = (method, url)
    if key in _warned_deprecations:
        return
    _warned_deprecations.add(key)
    msg = f"AgentTier API endpoint {method} {url} is deprecated"
    sunset = response.headers.get("Sunset", "")
    if sunset:
        msg += f" and will be removed after {sunset}"
    link = response.headers.get("Link", "")
    if "successor-version" in link:
        msg += f"; see {link}"
    warnings.warn(msg, DeprecationWarning, stacklevel=3)


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

    For streaming responses (``client.stream()``), httpx defers reading the
    body until ``.read()`` / ``.iter_*()`` is called. We explicitly read the
    body when the response is non-2xx so the body-decode logic below works
    on either streaming or buffered responses without bifurcating the API.
    """
    warn_if_deprecated(response)
    if response.is_success:
        return

    # On a streaming response the body hasn't been fetched yet. Pull it in
    # here so _decode_body can access response.content.
    if not getattr(response, "_content", None):
        try:
            response.read()
        except Exception:  # pragma: no cover — body already consumed
            pass

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
