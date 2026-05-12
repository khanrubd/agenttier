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
values). Dev mode — no OIDC configured — auto-grants admin, so the full
editing flow is exercised locally without extra setup.
