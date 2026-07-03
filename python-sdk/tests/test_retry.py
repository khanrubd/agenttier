# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Tests for the retry transport.

The transport is a thin wrapper over httpx.MockTransport, so the tests focus
on:

* Status-code retries (429 / 5xx) with the configured count.
* Connection errors retried only on idempotent methods unless ``retry_post``.
* SSE streams pass through without retry.
* ``Retry-After`` honored.
* Async variant has the same semantics.
"""

from __future__ import annotations

import time
from collections import Counter

import httpx
import pytest

from agenttier._retry import RetryConfig, _RetryTransport, wrap_async_transport, wrap_transport


def _make_transport(handler) -> httpx.MockTransport:
    return httpx.MockTransport(handler)


def test_retry_on_503_then_success() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        if counter["calls"] < 3:
            return httpx.Response(503, json={"error": "down"})
        return httpx.Response(200, json={"ok": True})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/foo")
    assert r.status_code == 200
    assert counter["calls"] == 3


def test_retry_exhausts_and_returns_last_response() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        return httpx.Response(503, json={"error": "still down"})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=2, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/foo")
    assert r.status_code == 503
    assert counter["calls"] == 3  # 1 initial + 2 retries


def test_no_retry_when_status_not_in_set() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        return httpx.Response(400, json={"error": "bad request"})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/foo")
    assert r.status_code == 400
    assert counter["calls"] == 1


def test_no_retry_for_post_by_default_on_connection_error() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        raise httpx.ConnectError("nope", request=request)

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        with pytest.raises(httpx.ConnectError):
            c.post("/foo", json={})
    assert counter["calls"] == 1


def test_post_retried_when_retry_post_true() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        raise httpx.ConnectError("nope", request=request)

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=2, backoff_factor=0.0, retry_post=True),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        with pytest.raises(httpx.ConnectError):
            c.post("/foo", json={})
    assert counter["calls"] == 3  # 1 + 2 retries before final raise


def test_get_retried_on_connection_error() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        if counter["calls"] < 2:
            raise httpx.ConnectError("temp", request=request)
        return httpx.Response(200, json={"ok": True})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/foo")
    assert r.status_code == 200
    assert counter["calls"] == 2


def test_sse_stream_passes_through_without_retry() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        return httpx.Response(503, headers={"content-type": "text/event-stream"})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/stream", headers={"Accept": "text/event-stream"})
    assert r.status_code == 503
    assert counter["calls"] == 1  # not retried


def test_retry_after_header_honored() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        if counter["calls"] < 2:
            return httpx.Response(429, headers={"Retry-After": "0"})
        return httpx.Response(200, json={"ok": True})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=2, backoff_factor=0.0),
    )
    started = time.monotonic()
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/foo")
    assert r.status_code == 200
    assert counter["calls"] == 2
    # Sanity: with Retry-After: 0, total wall time stays well under the
    # backoff_max default (8 s).
    assert time.monotonic() - started < 5


def test_retry_after_invalid_falls_back_to_backoff() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        if counter["calls"] < 2:
            return httpx.Response(429, headers={"Retry-After": "Wed, 21 Oct 2026 07:28:00 GMT"})
        return httpx.Response(200)

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=1, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/foo")
    # SDK doesn't crash; just falls back to exponential backoff (which is 0
    # because backoff_factor=0).
    assert r.status_code == 200


def test_disabled_when_max_retries_zero() -> None:
    transport = wrap_transport(_make_transport(lambda r: httpx.Response(503)), RetryConfig(max_retries=0))
    # When disabled, wrap_transport returns the inner transport unchanged.
    assert not isinstance(transport, _RetryTransport)


def test_disabled_when_config_none() -> None:
    inner = _make_transport(lambda r: httpx.Response(200))
    out = wrap_transport(inner, None)
    assert out is inner


# --- async variant ---------------------------------------------------------


@pytest.mark.asyncio
async def test_async_retry_on_503_then_success() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        if counter["calls"] < 3:
            return httpx.Response(503)
        return httpx.Response(200, json={"ok": True})

    transport = wrap_async_transport(
        httpx.MockTransport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    async with httpx.AsyncClient(transport=transport, base_url="http://test") as c:
        r = await c.get("/foo")
    assert r.status_code == 200
    assert counter["calls"] == 3


@pytest.mark.asyncio
async def test_async_sse_passes_through() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        return httpx.Response(503, headers={"content-type": "text/event-stream"})

    transport = wrap_async_transport(
        httpx.MockTransport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    async with httpx.AsyncClient(transport=transport, base_url="http://test") as c:
        r = await c.get("/stream", headers={"Accept": "text/event-stream"})
    assert r.status_code == 503
    assert counter["calls"] == 1


# --- POST idempotency gate on status retries (regression) ------------------


def test_post_not_retried_on_503_by_default() -> None:
    """Regression: a POST that gets a retryable 5xx must NOT be replayed when
    retry_post is False (the default) — replaying could double-create."""
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        return httpx.Response(503, json={"error": "down"})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),  # retry_post defaults False
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.post("/sandboxes", json={"name": "x"})
    assert r.status_code == 503
    assert counter["calls"] == 1  # NOT retried


def test_post_retried_on_503_when_retry_post_true() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        if counter["calls"] < 3:
            return httpx.Response(503)
        return httpx.Response(201, json={"ok": True})

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0, retry_post=True),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.post("/sandboxes", json={"name": "x"})
    assert r.status_code == 201
    assert counter["calls"] == 3


@pytest.mark.asyncio
async def test_async_post_not_retried_on_503_by_default() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        return httpx.Response(503)

    transport = wrap_async_transport(
        httpx.MockTransport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    async with httpx.AsyncClient(transport=transport, base_url="http://test") as c:
        r = await c.post("/sandboxes", json={"name": "x"})
    assert r.status_code == 503
    assert counter["calls"] == 1  # NOT retried


# --- _extract_retry_after edge cases ----------------------------------------


def test_extract_retry_after_missing_header_returns_none() -> None:
    resp = httpx.Response(429)
    assert _RetryTransport._extract_retry_after(resp) is None


def test_extract_retry_after_negative_value_clamped_to_zero() -> None:
    resp = httpx.Response(429, headers={"Retry-After": "-5"})
    assert _RetryTransport._extract_retry_after(resp) == 0.0


def test_extract_retry_after_numeric_seconds_parsed() -> None:
    resp = httpx.Response(429, headers={"Retry-After": "3"})
    assert _RetryTransport._extract_retry_after(resp) == 3.0


# --- async Retry-After honored (sync variant tested above; async has its
# own _AsyncRetryTransport._extract_retry_after call path via the shared
# staticmethod, but exercising it end-to-end catches wiring regressions) ----


@pytest.mark.asyncio
async def test_async_retry_after_header_honored() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        if counter["calls"] < 2:
            return httpx.Response(429, headers={"Retry-After": "0"})
        return httpx.Response(200, json={"ok": True})

    transport = wrap_async_transport(
        httpx.MockTransport(handler),
        RetryConfig(max_retries=2, backoff_factor=0.0),
    )
    async with httpx.AsyncClient(transport=transport, base_url="http://test") as c:
        r = await c.get("/foo")
    assert r.status_code == 200
    assert counter["calls"] == 2


@pytest.mark.asyncio
async def test_async_connection_error_exhausts_retries_and_raises() -> None:
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        raise httpx.ConnectError("still down", request=request)

    transport = wrap_async_transport(
        httpx.MockTransport(handler),
        RetryConfig(max_retries=2, backoff_factor=0.0),
    )
    async with httpx.AsyncClient(transport=transport, base_url="http://test") as c:
        with pytest.raises(httpx.ConnectError):
            await c.get("/foo")
    assert counter["calls"] == 3  # 1 initial + 2 retries


def test_no_retry_status_5xx_not_in_default_set_passes_through() -> None:
    # 501 is deliberately excluded from _DEFAULT_RETRY_STATUS (never resolves
    # on retry) — confirm it's returned as-is rather than retried.
    counter = Counter()

    def handler(request: httpx.Request) -> httpx.Response:
        counter["calls"] += 1
        return httpx.Response(501)

    transport = wrap_transport(
        _make_transport(handler),
        RetryConfig(max_retries=3, backoff_factor=0.0),
    )
    with httpx.Client(transport=transport, base_url="http://test") as c:
        r = c.get("/foo")
    assert r.status_code == 501
    assert counter["calls"] == 1
