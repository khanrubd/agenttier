# Security

Security model, trust boundaries, and operational notes for AgentTier operators
and contributors. For the vulnerability disclosure process, see
[SECURITY.md](https://github.com/agenttier/agenttier/blob/main/SECURITY.md).

## CRD source of truth

**The controller manages its own CRDs.** On startup, the controller applies the
CRD manifests bundled in `pkg/crds/` (create-or-update). This means:

- A `helm upgrade` that bumps the controller image **automatically brings CRDs
  up to the running version** — no manual `kubectl apply -f config/crd/` step.
- The CRD definitions in `pkg/crds/` are the authoritative copy. They are
  generated from `api/v1alpha1/` via `make generate manifests` and must never
  be edited by hand.
- If you manage CRDs out-of-band (GitOps / Argo CD), set
  `controller.manageCRDs=false`. The controller's ServiceAccount then no longer
  needs `customresourcedefinitions` write access, and you are responsible for
  applying CRD updates after `helm upgrade`.

**Do not** run `kubectl apply -f config/crd/` directly on a cluster where the
controller has `manageCRDs=true` — the next controller start-up will reconcile
the CRDs back to its bundled version.

## In-cluster exec transport

When `harness.useHTTPExec: true` is set on a template, the Router dials the
in-pod sandbox-runtime directly over plain `http://pod-ip:9000` inside the
cluster. The connection is authenticated with a per-sandbox bearer token
(injected as a Kubernetes Secret), but the traffic is **not TLS-encrypted
between the Router Pod and the sandbox Pod**.

**Trust model:** the security guarantee relies on NetworkPolicy. Each sandbox
has a deny-all ingress policy; only the Router's Pod IP (matched by
`podSelector`) is allowed to reach port 9000. An attacker who can observe
intra-cluster traffic (compromised node, compromised CNI) could read exec
payloads in transit.

**Accepted risk:** full mTLS between the Router and sandbox-runtime is on the
roadmap. For sensitive workloads on multi-tenant nodes, prefer the SPDY exec
fallback (set `useHTTPExec: false`) or use gVisor RuntimeClass to reduce the
blast radius of a node compromise.

## Share-link brute-force

Share-link tokens are 256-bit random values compared in constant time. Brute
force is computationally infeasible at this bit-length. The raw token is
supplied as a `?token=` query parameter (or `Authorization: Bearer` header) when
accessing a shared sandbox; there is no rate limit on validation because the
token space makes brute force equivalent to attacking SHA-256 directly.

Share links are created via `POST /api/v1/sandboxes/{id}/share-links`. There is
no per-share-link revoke endpoint; if your threat model includes a compromised
database that leaks token prefixes, revoke the sandbox's user grants
(`DELETE /api/v1/sandboxes/{id}/share/{userId}`) or let the link expire, then
reissue.

## OIDC validator startup gap

If the OIDC issuer URL is unreachable when the Router starts, the OIDC
validator is `nil`. The router **fails closed** — every authenticated request
returns 401 until the validator initialises (it retries in the background).
This is correct behavior: no auth bypass is possible.

The observable symptom is that the Router Pod logs `OIDC validator not
initialised` and returns 401 to all requests. Fix by ensuring the OIDC issuer
is reachable from the Router Pod before or shortly after startup (DNS,
firewall, Cognito endpoint). A future release will add a readiness probe that
holds the Pod NotReady until the validator is live.

## gVisor installer image supply chain

The opt-in gVisor installer DaemonSet (`security.gvisor.installer.enabled=true`)
uses the image `gcr.io/gvisor/gvisor-installer:latest` by default
(`helm/agenttier/values.yaml`). The `:latest` tag is a floating reference —
an upstream change silently alters the installer that runs privileged on your
nodes.

**For production clusters:**

1. Pin to a specific digest: `gcr.io/gvisor/gvisor-installer@sha256:<digest>`
2. Override the Helm value: `--set security.gvisor.installer.image=gcr.io/gvisor/gvisor-installer@sha256:<digest>`
3. Use the alternative options (baked node AMI or manual install) which do not
   require a privileged DaemonSet at all — see [gVisor sandboxing](gvisor.md).

## Authentication and authorization

- **OIDC JWTs** are validated against the issuer's JWKS on every request (with
  LRU caching). The `iss` and `aud` claims are checked.
- **API keys** are stored as SHA-256 hashes in Kubernetes Secrets and compared
  in constant time. A key is returned in plaintext exactly once (at creation).
- **Dev auth** (`auth.devAuth=true`) disables all authentication and treats
  every request as admin. It must never be enabled on a publicly-reachable
  Router. The controller logs a loud `AUTHENTICATION DISABLED — LOCAL DEV ONLY`
  warning on startup when this flag is set.
- **Admin routes** (`/admin/sandboxes`, `/admin/sharing`, cluster/namespace
  policy endpoints) are gated by `requireAdmin` middleware. Non-admin
  identities receive 403.

## Pod security defaults

Every sandbox Pod gets:

- Non-root user (`runAsNonRoot: true`)
- Read-only root filesystem (`readOnlyRootFilesystem: true`)
- All Linux capabilities dropped (`capabilities.drop: [ALL]`)
- `seccompProfile: RuntimeDefault`
- Per-sandbox ServiceAccount with zero cluster RBAC permissions
- A NetworkPolicy with deny-all ingress/egress (DNS always allowed; opt-in egress per template)

These defaults are set by the controller and cannot be weakened via the Sandbox
spec. Templates may tighten them further but not loosen them.

## Supply chain

Every image published to `ghcr.io/agenttier/*` on a `v*` tag is:

- Keyless-signed with cosign using GitHub Actions' OIDC identity.
- Shipped with SPDX + CycloneDX SBOMs attached as OCI artifacts.
- Built from a pinned, digest-locked base image (see `Dockerfile.controller`,
  `Dockerfile.router`, `images/*/Dockerfile`).

See [Verifying images](verifying-images.md) for `cosign verify` and
`cosign verify-attestation` commands.
