# Verifying released images

From **v0.2.0** onwards, every container image published to `ghcr.io/agenttier/*`
is keyless-signed with [cosign](https://docs.sigstore.dev/cosign/) using GitHub
Actions' OIDC identity, and ships with SPDX + CycloneDX SBOMs attached as OCI
artifacts.

## Verify a signature

Requires cosign v2+.

```bash
cosign verify \
  --certificate-identity-regexp 'https://github.com/agenttier/agenttier/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  ghcr.io/agenttier/controller:v0.3.0
```

The command prints the certificate chain on success and exits non-zero if the
signature is missing or the identity does not match the expected issuer and
workflow.

## Pull an SBOM

```bash
# Unsigned download (convenient, not tamper-proof):
cosign download sbom ghcr.io/agenttier/controller:v0.3.0 > controller.spdx.json

# Signed attestation (recommended):
cosign verify-attestation \
  --certificate-identity-regexp 'https://github.com/agenttier/agenttier/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --type spdx \
  ghcr.io/agenttier/controller:v0.3.0
```

## Policy engines

The signature format is the standard Sigstore bundle, so any admission
controller that speaks cosign / Sigstore policies (Kyverno, OPA Gatekeeper,
sigstore policy-controller) can enforce "only run AgentTier images signed by
the official GitHub Actions workflow" with a few lines of policy. Example
fragment for sigstore policy-controller:

```yaml
apiVersion: policy.sigstore.dev/v1beta1
kind: ClusterImagePolicy
metadata:
  name: agenttier-signed
spec:
  images:
    - glob: "ghcr.io/agenttier/*"
  authorities:
    - keyless:
        identities:
          - issuer: https://token.actions.githubusercontent.com
            subjectRegExp: ^https://github.com/agenttier/agenttier/.*$
```
