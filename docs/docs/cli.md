# CLI

The `agenttier` CLI is a Go binary for managing sandboxes and templates from the
terminal. Distributed as a GitHub Release asset for linux, macOS, and Windows on
amd64 and arm64.

## Install

Pick the binary for your OS/arch and drop it on your `PATH`:

```bash
VERSION=v0.1.0
OS=$(uname -s | tr A-Z a-z)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -L -o agenttier \
  "https://github.com/agenttier/agenttier/releases/download/${VERSION}/agenttier-${VERSION}-${OS}-${ARCH}"
chmod +x agenttier
sudo mv agenttier /usr/local/bin/
```

Checksums (`.sha256`) are attached to every release asset.

## Authentication

The CLI auto-detects credentials in this order:

1. `AGENTTIER_API_KEY` env var
2. `AGENTTIER_TOKEN` env var (OIDC bearer token)
3. Kubeconfig in-cluster ServiceAccount token

## Commands

(A full reference will appear here as the CLI gains surface area. For now the
commands that mirror REST endpoints are the ones worth using.)

```bash
agenttier --help
agenttier --version
```

See [github.com/agenttier/agenttier/tree/main/cmd/cli](https://github.com/agenttier/agenttier/tree/main/cmd/cli)
for the current source.
