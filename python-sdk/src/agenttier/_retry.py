# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""Retry transport for the AgentTier SDK.

Wraps an ``httpx.BaseTransport`` so connection errors and a configurable set of
5xx / 429 responses get retried with jittered exponential backoff. Honors the
``Retry-After`` header on 429 responses so clients respect the Router's
governance throttle.

Design notes:

* Retries are **opt-in**. The default ``AgentTierClient`` ships with retries
  off so calls that fail fast continue to fail fast. Users with flaky
  networks or autoscaling Routers turn it on with ``retry=RetryConfig(...)``.
* SSE-streaming endpoints (``/invoke``, ``/configure``) are skipped by the
  retry logic — replaying half a stream would emit duplicate events. The
  transport detects ``Accept: text/event-stream`` and bails out of the
  retry loop on the first response.
* Backoff is ``min(backoff_max, backoff_factor * (2 ** attempt))`` plus 0–25%
  random jitter so simultaneous client retries don't synchronize and
  hammer a recovering server.
* Idempotent methods (``GET``, ``HEAD``, ``OPTIONS``, ``PUT``, ``DELETE``) are
  always retried on connection errors. ``POST`` is retried on connection
  errors only when ``retry_post=True`` since POSTs are typically
  side-effecting.
"""

from __future__ import annotations

import random
import time
from dataclasses import dataclass, field
from typing import Optional

import httpx


# Status codes that warrant a retry. 408 (request timeout), 429 (throttled),
# 500-504 (server errors / bad gateway / service unavailable / gateway
# timeout) are the reasonable defaults. 501 (not implemented) is excluded —
# it never resolves on retry.
_DEFAULT_RETRY_STATUS = frozenset({408, 429, 500, 502, 503, 504})

# Methods httpx considers safe to retry on a connection error without
# re-asking the user. POST is omitted by default (potentially side-effecting).
_IDEMPOTENT_METHODS = frozenset({"GET", "HEAD", "OPTIONS", "PUT", "DELETE"})


@dataclass(frozen=True)
class RetryConfig:
    """Retry policy for an :class:`AgentTierClient`.

    Defaults are conservative — three retries with a 250 ms initial backoff
    capped at 8 s, jittered. That translates to roughly 0.25, 0.5, 1.0 s
    inter-attempt waits in the worst case, finishing under 2 s of total
    wall time before giving up.

    :param max_retries: Number of *additional* attempts after the first
        failure. ``0`` disables retries entirely (the same as not setting a
        :class:`RetryConfig`).
    :param backoff_factor: Multiplier for the exponential backoff. Each
        attempt waits ``min(backoff_max, backoff_factor * (2 ** attempt))``
        seconds plus jitter.
    :param backoff_max: Upper bound on a single wait (seconds).
    :param retry_status: HTTP status codes that trigger a retry.
    :param retry_post: When True, POST requests are also retried on
        connection errors. Most Router endpoints are not idempotent so
        the default is False.
    :param respect_retry_after: When True, honor ``Retry-After`` header on
        429 / 503 responses (cap to ``backoff_max`` to avoid runaway sleeps).
    """

    max_retries: int = 3
    backoff_factor: float = 0.25
    backoff_max: float = 8.0
    retry_status: frozenset[int] = field(default_factory=lambda: _DEFAULT_RETRY_STATUS)
    retry_post: bool = False
    respect_retry_after: bool = True


class _RetryTransport(httpx.BaseTransport):
    """Synchronous retry transport. Wraps the user's HTTPTransport.

    Honors ``RetryConfig`` for status-code retries and connection errors.
    Streaming responses (``Accept: text/event-stream``) are passed through
    without retry — replaying SSE would emit duplicate events.
    """

    def __init__(self, inner: httpx.BaseTransport, config: RetryConfig) -> None:
        self._inner = inner
        self._config = config

    def handle_request(self, request: httpx.Request) -> httpx.Response:
        # SSE responses cannot be safely retried — the upstream emits state
        # transitions that aren't idempotent (start event with new invokeId,
        # log lines, etc). Pass through.
        if request.headers.get("Accept", "").startswith("text/event-stream"):
            return self._inner.handle_request(request)

        method = request.method.upper()
        attempts_remaining = self._config.max_retries
        last_exc: Optional[Exception] = None

        for attempt in range(attempts_remaining + 1):
            try:
                response = self._inner.handle_request(request)
            except (httpx.ConnectError, httpx.ReadError, httpx.RemoteProtocolError) as exc:
                last_exc = exc
                # Connection errors only retry for idempotent methods (or POST
                # when retry_post is on) — otherwise the client cannot tell
                # whether the side effect already happened.
                if method not in _IDEMPOTENT_METHODS and not self._config.retry_post:
                    raise
                if attempt >= attempts_remaining:
                    raise
                self._sleep(attempt, retry_after=None)
                continue

            if response.status_code not in self._config.retry_status:
                return response
            if attempt >= attempts_remaining:
                return response

            # Drain the body so the connection returns to the pool. Without
            # this httpx will leak the connection across retries.
            response.read()
            response.close()

            retry_after = self._extract_retry_after(response) if self._config.respect_retry_after else None
            self._sleep(attempt, retry_after=retry_after)

        # Should be unreachable — the loop either returns or raises.
        # Guard for type-checker.
        if last_exc is not None:  # pragma: no cover
            raise last_exc
        raise RuntimeError("retry loop exited without a response")  # pragma: no cover

    def _sleep(self, attempt: int, retry_after: Optional[float]) -> None:
        if retry_after is not None:
            wait = min(retry_after, self._config.backoff_max)
        else:
            base = self._config.backoff_factor * (2**attempt)
            wait = min(base, self._config.backoff_max)
        # 0–25% jitter so concurrent clients don't synchronize.
        wait = wait * (1.0 + random.random() * 0.25)
        if wait > 0:
            time.sleep(wait)

    @staticmethod
    def _extract_retry_after(response: httpx.Response) -> Optional[float]:
        v = response.headers.get("Retry-After")
        if not v:
            return None
        try:
            return max(0.0, float(v))
        except ValueError:
            # HTTP date format (RFC 7231) — unsupported here; the SDK falls
            # back to exponential backoff. Real clients rarely send dates.
            return None

    def close(self) -> None:
        self._inner.close()


class _AsyncRetryTransport(httpx.AsyncBaseTransport):
    """Async counterpart of :class:`_RetryTransport`. Same semantics."""

    def __init__(self, inner: httpx.AsyncBaseTransport, config: RetryConfig) -> None:
        self._inner = inner
        self._config = config

    async def handle_async_request(self, request: httpx.Request) -> httpx.Response:
        import asyncio

        if request.headers.get("Accept", "").startswith("text/event-stream"):
            return await self._inner.handle_async_request(request)

        method = request.method.upper()
        attempts_remaining = self._config.max_retries
        last_exc: Optional[Exception] = None

        for attempt in range(attempts_remaining + 1):
            try:
                response = await self._inner.handle_async_request(request)
            except (httpx.ConnectError, httpx.ReadError, httpx.RemoteProtocolError) as exc:
                last_exc = exc
                if method not in _IDEMPOTENT_METHODS and not self._config.retry_post:
                    raise
                if attempt >= attempts_remaining:
                    raise
                await asyncio.sleep(self._compute_sleep(attempt, None))
                continue

            if response.status_code not in self._config.retry_status:
                return response
            if attempt >= attempts_remaining:
                return response

            await response.aread()
            await response.aclose()

            retry_after = (
                _RetryTransport._extract_retry_after(response)
                if self._config.respect_retry_after
                else None
            )
            await asyncio.sleep(self._compute_sleep(attempt, retry_after))

        if last_exc is not None:  # pragma: no cover
            raise last_exc
        raise RuntimeError("retry loop exited without a response")  # pragma: no cover

    def _compute_sleep(self, attempt: int, retry_after: Optional[float]) -> float:
        if retry_after is not None:
            wait = min(retry_after, self._config.backoff_max)
        else:
            base = self._config.backoff_factor * (2**attempt)
            wait = min(base, self._config.backoff_max)
        return wait * (1.0 + random.random() * 0.25)

    async def aclose(self) -> None:
        await self._inner.aclose()


def wrap_transport(
    transport: httpx.BaseTransport, config: Optional[RetryConfig]
) -> httpx.BaseTransport:
    """Return ``transport`` wrapped in a retry layer (or unchanged when ``config`` is None)."""
    if config is None or config.max_retries <= 0:
        return transport
    return _RetryTransport(transport, config)


def wrap_async_transport(
    transport: httpx.AsyncBaseTransport, config: Optional[RetryConfig]
) -> httpx.AsyncBaseTransport:
    """Async version of :func:`wrap_transport`."""
    if config is None or config.max_retries <= 0:
        return transport
    return _AsyncRetryTransport(transport, config)
