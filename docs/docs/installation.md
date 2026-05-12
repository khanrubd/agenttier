# Installation

AgentTier installs as a single Helm chart. CRDs and RBAC are bundled.

## Requirements

- Kubernetes **1.27+**
- A CNI that supports NetworkPolicy (Calico, Cilium, or AWS VPC CNI)
- A CSI storage driver (EBS CSI, PD CSI, or any CSI-compliant driver)
- Helm **3.x**

## Install from the public chart repo

```bash
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update
helm install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace
```

That's it. Images pull anonymously from `ghcr.io/agenttier/*`.

## Useful values

All values are documented inline in
[`helm/agenttier/values.yaml`](https://github.com/agenttier/agenttier/blob/main/helm/agenttier/values.yaml).
The knobs you are most likely to change:

| Value | Purpose |
| --- | --- |
| `auth.oidc.issuerUrl` | OIDC issuer (Cognito, Okta, Azure AD). Leave empty for dev mode. |
| `auth.oidc.adminGroup` | Group that receives the `isAdmin` claim |
| `networking.defaultPolicy` | `deny-all` (default) or `allow-internet` |
| `networking.previewDomain` | Wildcard domain for port-forward preview URLs |
| `networking.portForwardIngressClass` | Ingress class for forwarded ports (`nginx`, `alb`, `traefik`, …) |
| `security.gvisor.enabled` | Enable the gVisor RuntimeClass for untrusted workloads |
| `defaults.sandbox.image` | Default sandbox image when a template doesn't override it |
| `defaults.sandbox.resources` | Default CPU/memory requests and limits |
| `warmPool.enabled` | Pre-create idle pods for near-instant sandbox startup |
| `optional.imagePrepull.enabled` | DaemonSet that pre-caches sandbox images on every node |

## Upgrading

Helm upgrades are in-place and CRD-aware. Chart versions follow the app version
(e.g. chart `0.2.0` bundles app `v0.2.0`).

```bash
helm repo update
helm upgrade agenttier agenttier/agenttier --namespace agenttier
```

See the [CHANGELOG](https://github.com/agenttier/agenttier/blob/main/CHANGELOG.md)
for upgrade notes on each release.

## Verifying images (recommended)

Release images are keyless-signed with cosign and ship with SPDX + CycloneDX
SBOMs. See [Verifying images](verifying-images.md) for the full flow.
