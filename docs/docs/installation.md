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
    image: "ghcr.io/agenttier/sandbox-general:v0.5.0"
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
  ingress:
    enabled: true
    className: alb
    annotations:
      alb.ingress.kubernetes.io/scheme: internet-facing
      alb.ingress.kubernetes.io/target-type: ip
      alb.ingress.kubernetes.io/listen-ports: '[{"HTTP":80},{"HTTPS":443}]'
      alb.ingress.kubernetes.io/ssl-redirect: "443"
      alb.ingress.kubernetes.io/certificate-arn: arn:aws:acm:us-east-1:111122223333:certificate/xxxx
      alb.ingress.kubernetes.io/load-balancer-attributes: idle_timeout.timeout_seconds=4000
      alb.ingress.kubernetes.io/target-group-attributes: stickiness.enabled=true,stickiness.type=lb_cookie,stickiness.lb_cookie.duration_seconds=3600
    hosts:
      - host: agenttier.example.com
        paths:
          - path: /
            pathType: Prefix
    tls:
      - hosts: [agenttier.example.com]
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
| `auth.devAuth` | DANGER. When `true`, bypasses all auth and treats every request as admin. Local dev only. Default `false` — a missing OIDC issuer fails closed (401), it does NOT grant anonymous admin. |
| `auth.oidc.issuerUrl` | OIDC issuer URL. Empty + `devAuth: false` = every API request is rejected with 401. |
| `auth.oidc.clientId` | OIDC client ID. |
| `auth.oidc.adminGroup` | Group name that receives the `isAdmin` claim. |
| `auth.oidc.groupClaim` | JWT claim that carries the user's groups (default `groups`). |
| `auth.apiKeys.enabled` | Enable X-API-Key authentication. Keys are minted via `POST /user/api-keys`, stored as SHA-256 hashes in Secrets, returned in plaintext exactly once. |

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
| `defaults.sandboxNamespace` | Namespace where Sandboxes (and therefore warm pool Pods + PVCs) are created. Defaults to `default`. Must match where the Router creates Sandboxes — a claimed pool Pod is reused in place and can't move namespaces. |

The Settings page in the Web UI mutates the same values via the `agenttier-warmpool-config` ConfigMap, so admins can retune without redeploying the chart. The pool's configuration lives in the install namespace, but the idle Pods themselves are provisioned in `defaults.sandboxNamespace` so a claimed Pod can be handed directly to a new Sandbox.

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

Helm upgrades are in-place. Chart versions track the app version.

```bash
helm repo update
helm upgrade agenttier agenttier/agenttier \
  --namespace agenttier -f values.prod.yaml
```

**CRDs upgrade automatically.** Helm installs CRDs only on first install and never updates them on
`helm upgrade`, which historically left newly added CRD fields unusable until you ran
`kubectl apply -f config/crd/` by hand. The controller now applies its bundled CRDs on startup
(create-or-update), so a `helm upgrade` that rolls the controller image also brings the CRDs up to
the running version — no manual step. If you manage CRDs out-of-band (GitOps/Argo CD), set
`controller.manageCRDs=false` and apply CRD updates yourself; the controller's ServiceAccount then
no longer needs `customresourcedefinitions` write access.

See the [CHANGELOG](https://github.com/agenttier/agenttier/blob/main/CHANGELOG.md) for per-version upgrade notes.

## Uninstall

```bash
helm uninstall agenttier --namespace agenttier
kubectl delete namespace agenttier

# CRDs are kept by default so your sandboxes survive a re-install.
# Remove them explicitly if you want a clean slate:
kubectl delete crd \
  sandboxes.agenttier.io \
  sandboxtemplates.agenttier.io \
  clustersandboxtemplates.agenttier.io

# If you're upgrading from the pre-rename `agentloft.io` CRDs (rare), also
# remove those — Helm won't touch them:
kubectl delete crd \
  sandboxes.agentloft.io \
  sandboxtemplates.agentloft.io \
  clustersandboxtemplates.agentloft.io 2>/dev/null || true
```

## Exposing the Web UI on AWS with ALB

For production on EKS, use the [AWS Load Balancer
Controller](https://kubernetes-sigs.github.io/aws-load-balancer-controller/)
and enable the chart's Ingress. ALB has native WebSocket support, better idle
timeout controls, TLS termination at the edge, and cleaner integration with
WAF, ACM, and Route 53 than the legacy Classic ELB.

Prerequisites (one-time per cluster):

```bash
# 1. Download the latest IAM policy from upstream. The version pinned below
#    works with AWS Load Balancer Controller v2.13+ (it includes the
#    `elasticloadbalancing:DescribeListenerAttributes` permission that newer
#    controllers require; older policy snapshots lack it and cause the
#    controller to fail with "AccessDenied" when creating listener rules).
curl -sSL -o alb-iam-policy.json \
  https://raw.githubusercontent.com/kubernetes-sigs/aws-load-balancer-controller/main/docs/install/iam_policy.json

aws iam create-policy --policy-name AWSLoadBalancerControllerIAMPolicy \
  --policy-document file://alb-iam-policy.json

# 2. Associate the cluster's OIDC provider with IAM (safe to re-run).
aws eks describe-cluster --name <cluster> --query 'cluster.identity.oidc.issuer'

# 3. Create an IRSA role for the controller's ServiceAccount.
eksctl create iamserviceaccount \
  --cluster <cluster> --namespace kube-system \
  --name aws-load-balancer-controller \
  --role-name AmazonEKSLoadBalancerControllerRole \
  --attach-policy-arn=arn:aws:iam::<account>:policy/AWSLoadBalancerControllerIAMPolicy \
  --override-existing-serviceaccounts --approve

# 4. Install the controller.
helm repo add eks https://aws.github.io/eks-charts
helm repo update
helm install aws-load-balancer-controller eks/aws-load-balancer-controller \
  --namespace kube-system --set clusterName=<cluster> \
  --set serviceAccount.create=false \
  --set serviceAccount.name=aws-load-balancer-controller
```

If you don't use `eksctl`, do step 3 manually: create an IAM role whose trust
policy federates to the cluster OIDC provider with `sub` =
`system:serviceaccount:kube-system:aws-load-balancer-controller`, attach the
policy, then annotate the ServiceAccount with
`eks.amazonaws.com/role-arn=<role-arn>`.

Then enable the chart's Ingress. The chart ships sensible defaults under
`optional.ingress.annotations` for `idle_timeout.timeout_seconds=4000` and
sticky sessions, so long-running terminal sessions stay alive without
disconnects. Override `host` and optionally point `certificate-arn` at an ACM
certificate to terminate TLS at the ALB:

```bash
helm upgrade --install agenttier agenttier/agenttier \
  --namespace agenttier --create-namespace \
  --set optional.ingress.enabled=true \
  --set optional.ingress.hosts[0].host=agenttier.example.com \
  --set optional.ingress.hosts[0].paths[0].path=/ \
  --set optional.ingress.hosts[0].paths[0].pathType=Prefix
```

The Router additionally sends WebSocket control-frame pings and application
heartbeats every 30 seconds, so even with the 60s ALB default the browser
terminal survives long idle periods.

## Verifying released images

Every image published on a `v*` tag is keyless-signed and ships with SPDX + CycloneDX SBOMs. See [Verifying images](verifying-images.md) for `cosign verify` and `cosign verify-attestation` flows. For hardened clusters, enforce with Kyverno / sigstore policy-controller rather than relying on manual verification.
