# AgentTier

**Kubernetes-native platform for isolated, persistent sandboxes — for humans and AI agents.**

AgentTier gives humans and AI agents disposable, on-demand dev environments
managed as Kubernetes CRDs. Each sandbox is a Pod + PVC + NetworkPolicy with
its own persistent workspace, a full PTY terminal in the browser, and
optional per-session credentials. The warm pod pool makes creation feel
instant (~800 ms in our measurements vs ~10 s cold).

## When to use it

- Give each AI agent (Claude Code, Cursor, Aider, a custom agent) a private,
  isolated environment with its own storage and credentials.
- Run untrusted AI-generated code safely with gVisor kernel isolation.
- Provide on-demand developer environments for your team without pets.
- Orchestrate multi-agent workflows where agents need to collaborate over
  in-cluster networking.

## Get started

- [Quickstart](quickstart.md) — from zero to a running sandbox.
- [Installation](installation.md) — Helm chart, values, and production knobs.
- [Templates](templates.md) — the YAML blueprints that describe each agent.
- [Governance](governance.md) — cluster-wide and per-namespace policy enforcement.
- [Port forwarding](port-forwarding.md) — exposing sandbox ports via Services and Ingresses.

## Install in three commands

```bash
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update
helm install agenttier agenttier/agenttier --namespace agenttier --create-namespace
```

Then create a sandbox from the bundled `general-coding` template and open it in
your browser. See the [Quickstart](quickstart.md).

## License

Apache-2.0. Source at
[github.com/agenttier/agenttier](https://github.com/agenttier/agenttier).
