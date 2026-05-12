# CLI

The `agenttier` CLI is a single Go binary for managing sandboxes and
templates from the terminal. Distributed as GitHub Release assets for
Linux, macOS, and Windows on amd64 and arm64.

Source: [`cmd/cli/`](https://github.com/agenttier/agenttier/tree/main/cmd/cli).
Downloads: [releases/latest](https://github.com/agenttier/agenttier/releases/latest).

## Install

Pick the binary for your OS/arch and drop it on your `PATH`.

### macOS / Linux

```bash
VERSION=v0.1.1
OS=$(uname -s | tr A-Z a-z)
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
curl -L -o agenttier \
  "https://github.com/agenttier/agenttier/releases/download/${VERSION}/agenttier-${VERSION}-${OS}-${ARCH}"

# Verify the SHA256 (recommended)
curl -L -o agenttier.sha256 \
  "https://github.com/agenttier/agenttier/releases/download/${VERSION}/agenttier-${VERSION}-${OS}-${ARCH}.sha256"
sha256sum -c agenttier.sha256 || shasum -a 256 -c agenttier.sha256

chmod +x agenttier
sudo mv agenttier /usr/local/bin/
agenttier --version
```

### Windows

Download `agenttier-vX.Y.Z-windows-amd64.exe` from the release page and place
it somewhere on your `PATH`. PowerShell one-liner:

```powershell
$version = "v0.1.1"
Invoke-WebRequest `
  -Uri "https://github.com/agenttier/agenttier/releases/download/$version/agenttier-$version-windows-amd64.exe" `
  -OutFile "$env:USERPROFILE\bin\agenttier.exe"
```

### Homebrew

A tap is planned; in the meantime use the curl flow above.

## Configuration

The CLI talks to the Router's REST API. Configure the endpoint and credentials
with environment variables (same as the SDK):

| Variable | Effect |
| --- | --- |
| `AGENTTIER_API_URL` | Router base URL (`https://agenttier.company.com`). |
| `AGENTTIER_API_KEY` | Preferred auth; sent as `X-API-Key`. |
| `AGENTTIER_TOKEN` | OIDC bearer token; used if no API key. |
| `KUBECONFIG` | Falls back to in-cluster ServiceAccount token when on a kubeconfig'd node. |

Example:

```bash
export AGENTTIER_API_URL=https://agenttier.company.com
export AGENTTIER_API_KEY=sk_live_...
agenttier sandbox list
```

## Usage

```bash
agenttier --help
agenttier --version
```

The CLI is still lean in v0.1.1 — the core sandbox and template commands are
there; port forwarding and governance editing will follow the server-side
maturity. For features the CLI doesn't cover yet, fall back to `kubectl` on
CRDs directly (sandboxes, templates) or the Python SDK.

## Why use the CLI vs `kubectl` or the SDK?

| Tool | Best for |
| --- | --- |
| `agenttier` CLI | Quick one-off commands, shell scripting, CI jobs. |
| `kubectl` on CRDs | GitOps, bulk operations, combining with other Kubernetes tooling. |
| Python SDK | Building tools, orchestrating multiple sandboxes, embedding into an agent framework. |
| Web UI | Humans exploring and debugging sandboxes. |

All four drive the same Router API and CRDs, so they are interchangeable at
any point — pick the right shape for the task.
