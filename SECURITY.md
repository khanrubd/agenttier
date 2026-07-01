# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in AgentTier, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

Instead, please email: security@agenttier.io

### What to Include

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

### Response Timeline

- **Acknowledgment**: Within 48 hours
- **Initial Assessment**: Within 5 business days
- **Fix Timeline**: Depends on severity (Critical: 7 days, High: 14 days, Medium: 30 days)

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |
| < latest| :x:                |

## Security Best Practices for Operators

- Always run the latest version
- Enable gVisor for sandboxes running untrusted code
- Use network policies (default deny-all is enforced)
- Rotate credentials regularly
- Enable audit logging
- Review governance policies periodically
- Use approved image registries

## Verifying released images

All container images published to `ghcr.io/agenttier/*` on a `v*` tag are:

1. **Keyless-signed with cosign** using GitHub Actions' OIDC identity.
2. **Shipped with an SBOM** (SPDX + CycloneDX) attached as OCI artifacts.

### Verify the signature

Requires [cosign](https://docs.sigstore.dev/cosign/installation/) v2+.

```bash
cosign verify \
  --certificate-identity-regexp 'https://github.com/agenttier/agenttier/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  ghcr.io/agenttier/controller:v0.8.1
```

The command prints the certificate chain on success and exits non-zero if the
signature is missing or the identity does not match.

### Pull the SBOM

```bash
# SPDX
cosign download sbom ghcr.io/agenttier/controller:v0.8.1 > controller.spdx.json

# Or via the attest predicate (signed attestation, stronger guarantee)
cosign verify-attestation \
  --certificate-identity-regexp 'https://github.com/agenttier/agenttier/.*' \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  --type spdx \
  ghcr.io/agenttier/controller:v0.8.1
```

Signatures and attestations are present from **v0.2.0** onwards. Earlier
releases (v0.1.0 and below) shipped without them.
