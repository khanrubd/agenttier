# Port forwarding

Any sandbox container port can be exposed to authenticated users. AgentTier
creates a ClusterIP Service targeting the sandbox Pod and, when a preview
domain is configured, an Ingress resource pointing at it.

## Expose a port

```bash
# Web UI: click "Forward" on the running sandbox card
# CLI: (coming soon)
# REST:
curl -X POST https://agenttier.company.com/api/v1/sandboxes/my-sbx/ports \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"port": 8080, "protocol": "http"}'
```

Response:

```json
{
  "port": 8080,
  "protocol": "http",
  "internalUrl": "http://pf-my-sbx-8080.default.svc.cluster.local:8080",
  "previewUrl": "https://sandbox-my-sbx-8080.preview.agenttier.company.com/"
}
```

## Access the forwarded port

Two paths:

### 1. Router-proxied preview (works without DNS setup)

```
GET https://agenttier.company.com/api/v1/sandboxes/{id}/preview/{port}/
```

The Router authenticates, checks the sandbox is running, and reverse-proxies
into the in-cluster Service. This is what the "preview" link in the Web UI
opens. No Ingress required — useful for dev clusters, `kind`, and E2E tests.

### 2. Public preview URL (requires Ingress + DNS)

If `networking.previewDomain` is set in Helm values, AgentTier also creates
an Ingress at `https://sandbox-{sandbox}-{port}.{previewDomain}/`. You need:

- An ingress controller (NGINX, ALB, Traefik, …). Set
  `networking.portForwardIngressClass` to its class name.
- Wildcard DNS `*.{previewDomain}` pointing at the ingress controller.
- TLS for the wildcard (cert-manager + a DNS-01 issuer works well).

## Remove a port

```bash
curl -X DELETE https://agenttier.company.com/api/v1/sandboxes/my-sbx/ports/8080 \
  -H "Authorization: Bearer $TOKEN"
```

This deletes the Service and Ingress and clears the entry from the sandbox's
`status.forwardedPorts`.

## Authorization

All port-forward endpoints go through the same owner check as the rest of the
Sandbox API: the caller must own the sandbox or be an admin. The Web UI and CLI
use the same REST endpoints.
