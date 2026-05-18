# CLI

The `agenttier` CLI manages sandboxes and templates from the terminal. Two distributions, identical command surface:

- **`pip install agenttier`** — pure-Python CLI installed alongside the SDK. Works on any platform with Python 3.10+. Recommended for Python-first users.
- **GitHub Releases** — native Go binaries for Linux, macOS, and Windows on amd64 and arm64. No Python runtime required.

Sources: [`cmd/cli/`](https://github.com/agenttier/agenttier/tree/main/cmd/cli) (Go), [`python-sdk/src/agenttier/cli.py`](https://github.com/agenttier/agenttier/tree/main/python-sdk/src/agenttier/cli.py) (Python).
Full command reference: [CLI command reference](cli-reference.md).

## Install

### Via `pip`

```bash
pip install agenttier
agenttier --version
```

This installs the SDK and the CLI together. The Python CLI exposes the full SDK surface (lifecycle, exec, files, port forwards, templates, agent-mode configure/invoke).

### Via GitHub Releases (Go binary)

Pick the binary for your OS/arch and drop it on your `PATH`.

### macOS / Linux

```bash
VERSION=v0.4.0
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
$version = "v0.4.0"
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

The CLI is still lean in v0.4.0 — the core sandbox and template commands are
there; port forwarding and governance editing will follow the server-side
maturity. For features the CLI doesn't cover yet, fall back to `kubectl` on
CRDs directly (sandboxes, templates) or the Python SDK.

## Agent mode (Phase 10)

Two commands drive `mode: agent` sandboxes from the shell:

### `agenttier configure <sandbox-id>`

Uploads files into the sandbox PVC, runs an install command, and records
the entrypoint. Idempotent: re-running with the same files + install command
short-circuits.

```bash
agenttier configure my-agent \
  --file /workspace/agent.py=./agent.py \
  --file /workspace/requirements.txt=./requirements.txt \
  --install "pip install -r /workspace/requirements.txt" \
  --entrypoint "python /workspace/agent.py"
```

| Flag | Meaning |
| --- | --- |
| `--file path=local-path` | Upload a file. Repeatable. Binary files auto-base64. |
| `--install "..."` | Argv string for the install command. Whitespace-split. |
| `--entrypoint "..."` | Argv string for the agent entrypoint. Whitespace-split. |

Install logs stream live to stdout / stderr. Exits 0 on success, 1 if the
install exited non-zero.

### `agenttier invoke <sandbox-id>`

Runs the configured entrypoint and streams its output. The CLI exits with
the same exit code as the entrypoint, so you can compose it in shell
pipelines.

```bash
# Inline prompt
agenttier invoke my-agent --prompt "what's the weather?"

# JSON body fed to the entrypoint on stdin
agenttier invoke my-agent --input '{"messages":[{"role":"user","content":"hi"}]}'

# Body from a file
agenttier invoke my-agent --input @./request.json

# Body from this shell's stdin
echo "from a pipe" | agenttier invoke my-agent --input -

# Cap the per-invoke timeout (overrides the template's defaultInvokeTimeout)
agenttier invoke my-agent --prompt "..." --timeout 5m

# Cancel an in-flight invoke (use the invokeId printed at the top of the stream)
agenttier invoke my-agent --cancel inv-abc123
```

| Flag | Meaning |
| --- | --- |
| `--prompt "..."` | Convenience: appended as `--prompt=<value>` to argv and fed to stdin if `--input` is empty. |
| `--input STR` | Body forwarded to the entrypoint on stdin. Inline string, `@/path` for a file, `-` for the CLI's own stdin. |
| `--timeout DURATION` | Server-side per-invoke timeout (Go duration string like `5m`). |
| `--cancel ID` | Cancel an in-flight invoke instead of starting a new one. |

Stdout from the entrypoint goes to your stdout, stderr to your stderr, and a
small status line ("`invoke started: inv-...`", "`invoke completed: exit 0`")
to stderr — so `agenttier invoke ... > out.txt 2>/dev/null` cleanly captures
just the agent's output.

## Why use the CLI vs `kubectl` or the SDK?

| Tool | Best for |
| --- | --- |
| `agenttier` CLI | Quick one-off commands, shell scripting, CI jobs. |
| `kubectl` on CRDs | GitOps, bulk operations, combining with other Kubernetes tooling. |
| Python SDK | Building tools, orchestrating multiple sandboxes, embedding into an agent framework. |
| Web UI | Humans exploring and debugging sandboxes. |

All four drive the same Router API and CRDs, so they are interchangeable at
any point — pick the right shape for the task.
