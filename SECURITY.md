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
