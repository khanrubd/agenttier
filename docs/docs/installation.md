# Installation

AgentTier installs as a single Helm chart. CRDs, RBAC, and reference templates are bundled.

## Requirements

- Kubernetes **1.27+**
- CNI that supports NetworkPolicy (Calico, Cilium, AWS VPC CNI with NetworkPolicy enabled)
- A CSI storage driver (EBS CSI, PD CSI, Azure Disk CSI, or any RWO-capable CSI)
- Helm **3.x**

Optional but recommended:

- An ingress controller (ingress-nginx, AWS ALB Controller, Traefik) for the Web UI and port-forward preview URLs
- An OIDC identity provider (Cognito, Okta, Azure AD, Auth0) for multi-user auth
- gVisor `RuntimeClass` (for running untrusted agent workloads with kernel-level isolation)

## Quick install

```bash
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update
helm install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace
```

Images are pulled anonymously from `ghcr.io/agenttier/*`. Every released image is keyless-signed with cosign — see [Verifying images](verifying-images.md) before using on production-sensitive clusters.

## Production install

A realistic values file for an EKS cluster with Cognito OIDC, warm pool, and ALB ingress:

```yaml
# values.prod.yaml
auth:
  oidc:
    issuerUrl: "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_XXXXXXXXX"
    clientId: "your-client-id"
    adminGroup: "agenttier-admins"
    groupClaim: "cognito:groups"

networking:
  defaultPolicy: deny-all
  previewDomain: "preview.agenttier.example.com"
  portForwardIngressClass: "alb"

security:
  gvisor:
    enabled: true

defaults:
  sandbox:
    image: "ghcr.io/agenttier/sandbox-general:v0.1.1"
    resources:
      requests:
        cpu: "500m"
        memory: "1Gi"
      limits:
        cpu: "2"
        memory: "4Gi"

warmPool:
  enabled: true
  desiredCount: 2
  template: "general-coding"

controller:
  replicas: 2
  resources:
    requests: { cpu: "100m", memory: "128Mi" }
    limits: { cpu: "500m", memory: "512Mi" }

router:
  replicas: 2
  service:
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-connection-idle-timeout: "3600"

optional:
  imagePrepull:
    enabled: true
  serviceMonitor:
    enabled: true   # requires Prometheus Operator
  podDisruptionBudget:
    enabled: true

observability:
  otlp:
    endpoint: "otel-collector.observability.svc.cluster.local:4317"
```

Install with this values file:

```bash
helm install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace \
  -f values.prod.yaml
```

## Helm values reference

All values are documented inline in [`helm/agenttier/values.yaml`](https://github.com/agenttier/agenttier/blob/main/helm/agenttier/values.yaml). The knobs you will most often change:

### Auth

| Value | Purpose |
| --- | --- |
| `auth.oidc.issuerUrl` | OIDC issuer URL. Empty = dev mode (every request is anonymous admin). |
| `auth.oidc.clientId` | OIDC client ID. |
| `auth.oidc.adminGroup` | Group name that receives the `isAdmin` claim. |
| `auth.oidc.groupClaim` | JWT claim that carries the user's groups (default `groups`). |
| `auth.apiKeys` | List of accepted API keys (SHA-256 hashed on disk). |

### Networking

| Value | Purpose |
| --- | --- |
| `networking.defaultPolicy` | `deny-all` (default) or `allow-internet`. |
| `networking.previewDomain` | Wildcard domain for port-forward preview URLs. Leave empty to use only the Router-proxied preview. |
| `networking.portForwardIngressClass` | Ingress class name (`alb`, `nginx`, `traefik`). |

### Sandbox defaults

| Value | Purpose |
| --- | --- |
| `defaults.sandbox.image` | Default sandbox image for templates that don't override. |
| `defaults.sandbox.resources` | Default CPU/memory requests and limits. |
| `defaults.sandbox.storage.size` | Default PVC size. |
| `defaults.sandbox.timeout` | Default max runtime. |
| `defaults.sandbox.idleTimeout` | Default idle auto-stop. |

### Security

| Value | Purpose |
| --- | --- |
| `security.gvisor.enabled` | Create a `gvisor` RuntimeClass and mark it available to templates. |
| `security.podSecurityContext` | Overrides the restrictive default (non-root, RO rootfs, drop ALL caps). |

### Warm pool

| Value | Purpose |
| --- | --- |
| `warmPool.enabled` | Leader-elected reconciler that pre-creates idle Pods. |
| `warmPool.desiredCount` | Number of hot spares to keep. |
| `warmPool.template` | Template the warm Pods use. |

The Settings page in the Web UI mutates the same values via the `agenttier-warmpool-config` ConfigMap, so admins can retune without redeploying the chart.

### Optional add-ons

| Value | Purpose |
| --- | --- |
| `optional.imagePrepull.enabled` | DaemonSet that pre-caches sandbox images on every node. |
| `optional.serviceMonitor.enabled` | Prometheus Operator ServiceMonitor (requires the Operator). |
| `optional.podDisruptionBudget.enabled` | PDB for controller + router. |
| `optional.otelCollector.enabled` | Sidecar OTel Collector. |

### Observability

| Value | Purpose |
| --- | --- |
| `observability.otlp.endpoint` | OTLP endpoint for traces + metrics + logs. |
| `observability.logLevel` | Controller + Router log verbosity (`info`, `debug`). |

## Upgrading

Helm upgrades are in-place and CRD-aware. Chart versions track the app version.

```bash
helm repo update
helm upgrade agenttier agenttier/agenttier \
  --namespace agenttier -f values.prod.yaml
```

See the [CHANGELOG](https://github.com/agenttier/agenttier/blob/main/CHANGELOG.md) for per-version upgrade notes.

## Uninstall

```bash
helm uninstall agenttier --namespace agenttier
kubectl delete namespace agenttier
# CRDs are kept by default so your sandboxes survive a re-install.
# Remove them explicitly if you want a clean slate:
kubectl delete crd sandboxes.agenttier.io \
  sandboxtemplates.agenttier.io \
  clustersandboxtemplates.agenttier.io
```

## Verifying released images

Every image published on a `v*` tag is keyless-signed and ships with SPDX + CycloneDX SBOMs. See [Verifying images](verifying-images.md) for `cosign verify` and `cosign verify-attestation` flows. For hardened clusters, enforce with Kyverno / sigstore policy-controller rather than relying on manual verification.
