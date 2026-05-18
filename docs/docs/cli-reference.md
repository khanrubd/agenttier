# CLI reference

Full command reference for the `agenttier` CLI. The CLI is distributed two ways:

- **`pip install agenttier`** — pure-Python CLI installed alongside the SDK. Works on any platform with Python 3.10+.
- **GitHub Releases** — native Go binaries (`linux/darwin/windows × amd64/arm64`) for users who do not want a Python runtime.

Both share the same command surface and flag names. This reference matches the v0.4.1 release.

For tutorials, see [Web UI walkthrough](tutorials/web-ui.md), [Python SDK walkthrough](tutorials/python-sdk.md), and [Agent mode in depth](tutorials/agent-mode-tutorial.md).

## Synopsis

```
agenttier <command> [<subcommand>] [flags] [arguments]
```

## Global flags

Available on every command:

| Flag | Env var | Description |
| --- | --- | --- |
| `--api-url <URL>` | `AGENTTIER_API_URL` | Router base URL. Required unless saved by `agenttier login`. |
| `--api-key <KEY>` | `AGENTTIER_API_KEY` | Static API key. Sent as `X-API-Key`. |
| `--token <JWT>` | `AGENTTIER_TOKEN` | Bearer token / OIDC JWT. Sent as `Authorization: Bearer ...`. |
| `--output text\|json` | — | Output format. `text` is human-friendly tables; `json` is machine-readable. Default: `text`. |
| `-h`, `--help` | — | Show help for the current command. |
| `--version` | — | Print CLI version. |

Configuration precedence: CLI flag → environment variable → `~/.config/agenttier/config.json` (set by `agenttier login`).

## Commands

### `agenttier version`

Print the CLI version string.

```
agenttier version
```

### `agenttier login`

Save endpoint and credentials to `~/.config/agenttier/config.json` (mode 0600). Verifies the configuration by calling `/user/me`.

```
agenttier login --api-url https://agenttier.example.com [--api-key <KEY> | --token <JWT>]
```

Flags: global only. The path is overridable via `AGENTTIER_CONFIG`.

### `agenttier whoami`

Print the server's view of the authenticated identity.

```
agenttier whoami [--output text|json]
```

Output: `email (admin)` for admin users, `email` otherwise. JSON mode returns the full `CurrentUser` model.

### `agenttier sandbox`

Manage sandboxes. Most operations work on the sandbox ID (DNS-friendly name).

#### `agenttier sandbox list`

List sandboxes visible to the caller.

```
agenttier sandbox list [--namespace <NS>] [--status Running|Stopped|Error|...]
```

Output columns: ID, NAME, STATUS, TEMPLATE, NAMESPACE.

#### `agenttier sandbox get`

Show one sandbox.

```
agenttier sandbox get <sandbox-id>
```

#### `agenttier sandbox create`

Create a sandbox from a `ClusterSandboxTemplate`.

```
agenttier sandbox create <name> --template <name> \
  [--namespace <NS>] \
  [--timeout <DUR>] [--idle-timeout <DUR>] \
  [--storage-size <SIZE>] \
  [--wait] [--wait-timeout <SECONDS>]
```

| Flag | Description |
| --- | --- |
| `<name>` | Sandbox name (DNS-friendly: lowercase alphanumeric and hyphens). |
| `--template` | Required. Cluster-scoped template name (e.g. `general-coding`). |
| `--namespace` | Default: `default`. |
| `--timeout` | Max-runtime duration (Go format, e.g. `8h`, `24h`). |
| `--idle-timeout` | Auto-stop after this much inactivity (e.g. `30m`). |
| `--storage-size` | PVC size (e.g. `10Gi`, `50Gi`). |
| `--wait` | Block until sandbox reaches `Running`. |
| `--wait-timeout` | Wait timeout in seconds. Default: `180`. |

#### `agenttier sandbox stop`

Stop a sandbox. The Pod is deleted; the PVC is preserved.

```
agenttier sandbox stop <sandbox-id>
```

#### `agenttier sandbox resume`

Resume a stopped sandbox. The PVC is re-attached to a fresh Pod.

```
agenttier sandbox resume <sandbox-id>
```

#### `agenttier sandbox delete`

Permanently delete a sandbox and its PVC.

```
agenttier sandbox delete <sandbox-id>
```

#### `agenttier sandbox exec`

Run a one-shot command and stream output. The CLI exits with the command's exit code.

```
agenttier sandbox exec <sandbox-id> [--timeout <SECONDS>] -- <command> [args...]
```

Examples:

```bash
agenttier sandbox exec demo -- bash -lc 'echo $USER'
agenttier sandbox exec demo --timeout 60 -- pytest tests/
```

#### `agenttier sandbox wait`

Block until the sandbox reaches `Running`.

```
agenttier sandbox wait <sandbox-id> [--timeout <SECONDS>]
```

#### `agenttier sandbox files`

File operations. Each operation goes through the Router with a 32 MiB per-call cap.

| Subcommand | Synopsis |
| --- | --- |
| `ls` | `agenttier sandbox files ls <id> [--path /workspace]` |
| `cat` | `agenttier sandbox files cat <id> <path>` |
| `upload` | `agenttier sandbox files upload <id> <local> <remote>` |
| `download` | `agenttier sandbox files download <id> <remote> <local>` |
| `write` | `agenttier sandbox files write <id> <path> --data "..."` or `--file <local>` (use `-` for stdin) |

Examples:

```bash
agenttier sandbox files ls demo --path /workspace
agenttier sandbox files upload demo ./script.py /workspace/script.py
agenttier sandbox files cat demo /workspace/output.txt > local.txt
echo "hello" | agenttier sandbox files write demo /workspace/note.txt --file -
```

#### `agenttier sandbox ports`

Manage port forwards. The Router proxies the in-pod port at `/api/v1/sandboxes/<id>/preview/<port>/`. When `networking.previewDomain` is set in Helm, an Ingress preview URL is also returned.

| Subcommand | Synopsis |
| --- | --- |
| `list` | `agenttier sandbox ports list <id>` |
| `forward` | `agenttier sandbox ports forward <id> --port <N> [--protocol http\|tcp]` |
| `remove` | `agenttier sandbox ports remove <id> --port <N>` |

### `agenttier template`

Inspect cluster-scoped templates.

| Subcommand | Synopsis |
| --- | --- |
| `list` | `agenttier template list` — outputs NAME, IMAGE, DESCRIPTION |
| `get` | `agenttier template get <name>` — full template spec as JSON |

### `agenttier configure`

Configure an agent-mode sandbox. Uploads files, runs an install command, and registers the entrypoint. Streams install logs as Server-Sent Events while the command runs.

```
agenttier configure <sandbox-id> \
  [--file <remote>=<local>]... \
  [--install "<command>"] \
  [--entrypoint "<command>"]
```

| Flag | Description |
| --- | --- |
| `--file` | Upload one local file. Repeatable. Format: `remote-path=local-path`. |
| `--install` | Install command, run once. Idempotent — re-runs with the same files + command short-circuit. |
| `--entrypoint` | Command run on every `/invoke`. Persisted in `Sandbox.status.agentConfigure`. |

At least one of `--file`, `--install`, `--entrypoint` is required. The sandbox must be `mode: agent` (rejected with HTTP 400 otherwise).

Example:

```bash
agenttier configure my-agent \
  --file /workspace/agent.py=./agent.py \
  --file /workspace/requirements.txt=./requirements.txt \
  --install "pip install -r /workspace/requirements.txt" \
  --entrypoint "python /workspace/agent.py"
```

### `agenttier invoke`

Run the configured entrypoint and stream stdout / stderr / exit as Server-Sent Events. The CLI exits with the entrypoint's exit code.

```
agenttier invoke <sandbox-id> \
  [--prompt "<text>"] \
  [--input "<inline string>" | "@/path/to/file" | "-"] \
  [--timeout <DUR>] \
  [--cancel <invoke-id>]
```

| Flag | Description |
| --- | --- |
| `--prompt` | Convenience flag — sent as `?prompt=<value>` query param. The entrypoint can read this from argv as `--prompt=<value>`. |
| `--input` | Body forwarded to the entrypoint on stdin. Inline string, `@/path/to/file` to read from disk, or `-` for stdin. |
| `--timeout` | Server-side per-invoke wall-clock cap (Go duration format, e.g. `5m`, `30s`). Defaults to the template's `defaultInvokeTimeout` (30 min if unset). |
| `--cancel <id>` | Cancel an in-flight invoke instead of starting a new one. The invoke ID is printed at the start of every invoke. |

Stdin is accepted in two ways: `--input "literal"` or piping into the CLI with `--input -`. JSON bodies are passed through unchanged so frameworks that expect typed dicts (LangGraph, Strands) can decode directly.

Examples:

```bash
# Plain prompt
agenttier invoke my-agent --prompt "summarize this PR"

# JSON body via inline string
agenttier invoke my-agent --input '{"prompt":"hello","temperature":0.7}'

# JSON body from file
agenttier invoke my-agent --input @./body.json

# Pipe stdin
echo '{"prompt":"hello"}' | agenttier invoke my-agent --input -

# Cancel an in-flight invoke
agenttier invoke my-agent --cancel inv-7ca109acefc16b71e8c99c03

# Cap server-side runtime to 5 minutes
agenttier invoke my-agent --prompt "long task" --timeout 5m
```

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | Success. |
| `1` | Operational error (HTTP 4xx / 5xx, sandbox not found, file I/O, etc.). |
| `2` | Usage error (bad flag, missing required argument). |
| `130` | Interrupted by Ctrl+C. |
| _other_ | For `sandbox exec` and `invoke`: the underlying command's exit code. |

## Output formats

Every command supports `--output json` for scriptable use:

```bash
agenttier sandbox list --output json | jq '.[] | select(.status == "Running") | .name'
agenttier template get general-coding --output json | jq .spec.image
```

In `json` mode, fields are serialized in snake_case from the SDK's pydantic models. The Router responds in camelCase; the SDK normalizes both directions.

## Configuration file

`agenttier login` writes `~/.config/agenttier/config.json` (mode 0600):

```json
{
  "api_url": "https://agenttier.example.com",
  "api_key": "ak_...",
  "token": "..."
}
```

Override the path with `AGENTTIER_CONFIG=/path/to/config.json`.

## Troubleshooting

- **`agenttier: no API URL configured`** — set `--api-url`, `AGENTTIER_API_URL`, or run `agenttier login`.
- **`HTTP 401`** — pass `--api-key` or `--token` (or run `agenttier login` to save credentials).
- **`HTTP 400: sandbox is in mode "code"`** — `/configure` and `/invoke` are agent-mode only. Create with `--template <agent-template>` or use `sandbox exec` for code-mode.
- **`HTTP 429`** — `maxConcurrentInvokes` cap hit. The error body includes the `limit` and `Retry-After` seconds.
