# Governance

AgentTier enforces governance policies at sandbox creation time. Policies are
stored in the `agenttier-governance` ConfigMap and edited through the Web UI
Settings page (admin-only) or the REST API.

## Scopes and merging

Two scopes:

- **Cluster default** — applies everywhere.
- **Per-namespace** — overrides the cluster default field-by-field. Empty
  fields fall through to the cluster default.

Resolution is cluster → namespace. Admin-gated `PUT /api/v1/governance/policies`
sets the cluster default; `PUT /api/v1/governance/policies/{namespace}` sets a
namespace override; `DELETE /api/v1/governance/policies/{namespace}` removes it
and restores the cluster default.

## What you can restrict

| Field | Example | Effect |
| --- | --- | --- |
| `maxSandboxesPerUser` | `5` | Cap per user in this namespace |
| `maxSandboxesTotal` | `50` | Cap total in this namespace |
| `maxCpu` | `"4"` | Rejects sandboxes whose CPU limit exceeds this |
| `maxMemory` | `"8Gi"` | Same, for memory |
| `maxStorage` | `"50Gi"` | Same, for PVC size |
| `maxTimeout` | `"24h"` | Caps `spec.timeout` (including the "infinite" 0) |
| `maxIdleTimeout` | `"1h"` | Caps `spec.idleTimeout` |
| `allowedTemplates` | `["general-coding"]` | Only these template names are permitted |
| `approvedRegistries` | `["ghcr.io/agenttier"]` | Image overrides must start with one of these prefixes |
| `maxAgentSandboxes` | `10` | Per-namespace cap on `mode: agent` sandboxes; doesn't affect code-mode |
| `allowedAgentImages` | `["ghcr.io/agenttier/sandbox-langgraph"]` | Tighter image allowlist applied only to agent-mode sandboxes that override the template image |
| `maxConcurrentInvokesPerSandbox` | `4` | Cluster ceiling clamping the per-template `agent.maxConcurrentInvokes` |

## Agent-mode policies

The last three rows above only apply to `mode: agent` sandboxes. They were added in v0.3.0 as part of [agent mode](agent-mode.md). All three default unset for zero behavior change on existing deployments.

- `maxAgentSandboxes` runs alongside `maxSandboxesTotal`. A namespace with both set rejects new agent sandboxes when either cap is reached. Useful when you want generous code-mode quota but tight agent-mode rationing.
- `allowedAgentImages` is checked only when an agent-mode sandbox overrides the template image. The template's own image is trusted (it was vetted at template-creation time). Distinct from `approvedRegistries` because agent code typically warrants stricter supply-chain controls than interactive dev environments.
- `maxConcurrentInvokesPerSandbox` clamps at admission time. A sandbox spec asking for more is silently lowered to the ceiling; the resolved value lands on `status.agentConfigure.maxConcurrentInvokes` so `/invoke` reads the already-clamped number.

## Re-checked at agent /configure

Three of the policy fields are also evaluated when an agent-mode sandbox calls `POST /api/v1/sandboxes/{id}/configure`. The sandbox already exists (a create-time policy passed), but `/configure` is the first time user-supplied code lands on the PVC, so a re-check guards against policies that tightened after creation:

- **`allowedTemplates`** — re-checked against `status.resolvedTemplate`. If the template fell out of the allowlist after the sandbox was created, the configure is denied (403) before any files are written.
- **`allowedAgentImages`** — re-checked against the sandbox's `spec.image.repository` (only when the sandbox overrides the template image). Same prefix-match semantics as the create-time check.
- **`maxConcurrentInvokesPerSandbox`** — clamped via `governance.ClampConcurrency` at configure time, with the resolved value persisted on `status.agentConfigure.maxConcurrentInvokes` so `/invoke` enforces it without re-resolving the policy on every request.

Independent of the policy, `/configure` enforces server-side correctness limits that protect the Router from a misbehaving caller:

- Per-file size cap of 32 MiB (`configureFileLimitBytes`).
- Aggregate size cap of 128 MiB across all files in one request (`configureFileTotalLimitBytes`).
- Maximum 200 files per request (`configureFileMaxCount`).

A request that violates any of these returns HTTP 403 with the same `policy_violation` shape and a `ConfigureDenied` Kubernetes event on the sandbox CR. The audit trail makes it easy to see who attempted what and when.

## Violations

When a create request is rejected the response is HTTP 403 with a structured body:

```json
{
  "error": "policy_violation",
  "violations": [
    {
      "code": "user_quota_exceeded",
      "message": "user already owns 5 sandboxes in this namespace (max 5)"
    }
  ]
}
```

Stable violation codes:

| Code | Meaning |
| --- | --- |
| `template_not_allowed` | Template is not in the `allowedTemplates` list |
| `image_registry_not_approved` | Image override not in `approvedRegistries` |
| `namespace_quota_exceeded` | Namespace has hit `maxSandboxesTotal` |
| `user_quota_exceeded` | User has hit `maxSandboxesPerUser` |
| `cpu_limit_exceeded` | CPU limit exceeds `maxCpu` |
| `memory_limit_exceeded` | Memory limit exceeds `maxMemory` |
| `storage_limit_exceeded` | Storage size exceeds `maxStorage` |
| `timeout_exceeded` | `spec.timeout` exceeds `maxTimeout` |
| `idle_timeout_exceeded` | `spec.idleTimeout` exceeds `maxIdleTimeout` |

The Web UI uses these codes to highlight the specific form field that triggered
the rejection.

## Admin access

In production, the `PUT`/`DELETE` governance endpoints require the `isAdmin`
claim, derived from OIDC group membership (`auth.oidc.adminGroup` in Helm
values). For local development, set `auth.devAuth: true` to auto-grant admin
so the full editing flow is exercised without an OIDC provider. Without
either an OIDC issuer or `devAuth`, the endpoints reject all requests with
401 (fail-closed) — a missing issuer no longer silently grants admin.
