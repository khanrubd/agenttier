# Templates

A `ClusterSandboxTemplate` (or namespace-scoped `SandboxTemplate`) is the
blueprint a sandbox is created from. It describes the container image, resource
caps, network rules, storage, env vars, init scripts, and the *agent harness* —
which shell, which tools, and which lifecycle hooks should run.

## Minimal template

```yaml
apiVersion: agenttier.io/v1alpha1
kind: ClusterSandboxTemplate
metadata:
  name: minimal-shell
spec:
  description: "Plain Alpine shell"
  image:
    repository: ghcr.io/agenttier/sandbox-minimal:latest
  resources:
    requests:
      cpu: "200m"
      memory: "256Mi"
    limits:
      cpu: "1"
      memory: "2Gi"
```

## Agent harness example

```yaml
apiVersion: agenttier.io/v1alpha1
kind: ClusterSandboxTemplate
metadata:
  name: claude-code-bedrock
spec:
  description: "Claude Code CLI with AWS Bedrock"
  image:
    repository: ghcr.io/agenttier/sandbox-claude-code:latest
  resources:
    requests:
      cpu: "1"
      memory: "2Gi"
    limits:
      cpu: "4"
      memory: "8Gi"
  storage:
    size: "20Gi"
  network:
    allowInternet: true
  env:
    - name: CLAUDE_CODE_USE_BEDROCK
      value: "1"
    - name: AWS_DEFAULT_REGION
      value: us-east-1
  harness:
    shell: /bin/bash
    tools:
      - name: claude
        verifyCommand: "claude --version"
    hooks:
      onStart: "echo 'Claude Code ready'"
  timeout: 24h
  idleTimeout: 2h
```

## Inheritance

Templates can extend other templates via `spec.inheritsFrom`. Field-level merge
order is: sandbox spec → template spec → parent template → controller defaults.
Env vars are additive with sandbox values winning on key conflicts. Inheritance
depth is capped at 10 to prevent loops.

## Agent mode

Templates with `spec.mode: agent` define sandboxes that run a configured entrypoint via [`POST /configure`](agent-mode.md) + [`POST /invoke`](agent-mode.md) instead of the terminal. The agent contract lives under `spec.harness.agent`:

```yaml
apiVersion: agenttier.io/v1alpha1
kind: ClusterSandboxTemplate
metadata:
  name: my-agent-template
spec:
  mode: agent
  image:
    repository: ghcr.io/agenttier/sandbox-langgraph:v0.4.0
  harness:
    workingDir: /workspace
    agent:
      entrypoint: ["python", "/workspace/agent.py"]
      installCommand: ["pip", "install", "-r", "/workspace/requirements.txt"]
      defaultInvokeTimeout: 30m
      maxConcurrentInvokes: 4
```

| Field | Effect |
| --- | --- |
| `entrypoint` | Argv `/invoke` runs on every call. Receives the request body on stdin. |
| `installCommand` | Argv run once at `/configure` time. Idempotent across re-configures. |
| `workingDir` | Working directory for both. Defaults to `/workspace`. |
| `env` | Additional env vars merged on top of the template's harness env. |
| `defaultInvokeTimeout` | Wall-clock cap per invoke. Callers can lower via `?timeout=` but not raise. Defaults to 30 minutes. |
| `maxConcurrentInvokes` | Cap on parallel invokes per sandbox. Over-cap requests get HTTP 429. Governance can clamp this lower; see [governance.md](governance.md#agent-mode-policies). |

The mode and the agent block are both optional at the template level — a template without them defaults to code mode and the existing harness fields apply unchanged.

## Credentials

Inject secrets into the sandbox container via `spec.credentials`:

```yaml
spec:
  credentials:
    - secretName: openai-api-key
      mountAs: env
      envPrefix: OPENAI_
```

`mountAs: env` exposes every key in the Secret as an env var (with the optional `envPrefix` prepended). `mountAs: file` mounts the Secret as a read-only volume at `mountPath`. Combine with IRSA on EKS for AWS-native flows like Bedrock — annotate the sandbox ServiceAccount with the role ARN and skip `credentials` for AWS calls entirely.

Agent-mode sandboxes use the same `credentials` block; nothing special is required for `/invoke` to inherit them. See [agent-mode.md](agent-mode.md#memory-model-providers-secrets) for the canonical patterns.

## Managing templates

- **Web UI** → Templates tab: inline YAML editor with syntax highlighting,
  create / save / delete.
- **CLI**: `agenttier template list | get | apply | delete`.
- **REST**: `GET/POST/PUT/DELETE /api/v1/templates[/name]`.
- **kubectl**: `kubectl get clustersandboxtemplates`; apply YAML directly.

Deletion is rejected if any Running sandbox still references the template.

## Reference templates

Bundled in the default install and ready to use:

- `general-coding` — general-purpose Debian + common dev tools.
- `claude-code-bedrock` — Claude Code CLI pinned to a known-good version, wired
  to AWS Bedrock via IRSA.
- `minimal-shell` — bare Alpine, fast to pull, for troubleshooting.
- `security-scanner` — pre-installed CVE scanners.
- `data-analysis` — Python + pandas + notebooks.
