# API versioning and deprecation

AgentTier's REST API is served under a version path prefix: today that is
`/api/v1`. This page documents how the API evolves and how clients are warned
before anything breaks.

## Policy

- **Breaking changes bump the path component.** Renaming or removing a request
  field, removing an endpoint, or changing a response shape incompatibly ships
  under `/api/v2` — never as an in-place change to `/api/v1`.
- **Overlap window.** When `/api/v2` reaches GA, `/api/v1` remains supported for
  **at least two minor releases**. Deprecated `/api/v1` endpoints continue to
  work during that window.
- **Deprecation signalling.** Deprecated endpoints emit standard HTTP headers
  clients can detect generically:
    - `Deprecation: true` — the endpoint is deprecated.
    - `Sunset: <HTTP-date>` — when it will be removed
      ([RFC 9745](https://www.rfc-editor.org/rfc/rfc9745.html)).
    - `Link: <successor>; rel="successor-version"` — the replacement endpoint.
- **Additive changes are not breaking.** New optional fields, new endpoints, and
  new enum values can land within the current version. SDK and CLI consumers
  should ignore unknown fields.
- **1.0 contract.** While AgentTier is pre-1.0 (`v0.x`) we accept that breaking
  changes happen between minor releases, but the **shape of `/api/v1` is stable
  from the 1.0 release forward** — consumers can rely on it from then on.

This mirrors the CRD-evolution rules (`v1alpha1` → `v1alpha2` → … → `v1`) the
project applies to Kubernetes resources, extended to the REST surface.

## What clients do with the headers

- **Python SDK** raises a one-time `DeprecationWarning` the first time a process
  hits a deprecated endpoint, naming the endpoint and its sunset date. Silence
  it with `AGENTTIER_DEPRECATION_WARNINGS=off` (or Python's standard
  `warnings` filters).
- **CLI** prints a one-time `stderr` notice per deprecated endpoint per run.
  Silence with `AGENTTIER_DEPRECATION_WARNINGS=off`.

Both are best-effort and never change exit codes or program flow — they only
inform.

## Current state

Nothing is deprecated yet — `/api/v1` is the only version and the deprecation
middleware is a no-op. The mechanism is in place so the day a breaking change is
needed, flagging an endpoint is a one-line change (a `deprecatedRoutes` entry in
the Router) and every shipped SDK/CLI already reacts to it.
