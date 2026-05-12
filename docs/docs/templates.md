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
