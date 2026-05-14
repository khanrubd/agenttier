# Tutorials

Hands-on walkthroughs that take you through real AgentTier workflows end-to-end. Each tutorial is self-contained and assumes a working cluster.

## Prerequisites

These tutorials assume you have already deployed AgentTier:

- A Kubernetes cluster (EKS, GKE, AKS, or local kind)
- AgentTier installed via Helm — see [Installation](../installation.md)
- The `kubectl`, `helm`, `agenttier` CLI, and `pip` available locally
- Python 3.9+ for SDK tutorials

If you have not deployed yet, run the [Quickstart](../quickstart.md) first. It takes about ten minutes.

A quick sanity check before you start any tutorial:

```bash
kubectl get pods -n agenttier
# agenttier-controller-xxx   1/1   Running
# agenttier-router-xxx       1/1   Running
# agenttier-webui-xxx        1/1   Running

kubectl get clustersandboxtemplates
# general-coding
# claude-code-bedrock
# langgraph-agent
```

## Pick your path

| Tutorial | What you build | Time |
| --- | --- | --- |
| [Web UI walkthrough](web-ui.md) | Create, open, configure, stop/resume, and delete sandboxes from the browser. Use the file panel and port forwarding. | ~15 min |
| [Python SDK walkthrough](python-sdk.md) | Sync and async clients, sandbox lifecycle, exec commands, file transfer, port forwarding, error handling. | ~25 min |
| [Code mode in depth](code-mode.md) | Long-lived dev sandboxes for humans (and Claude Code). Persistent workspaces, IDE-style flows, IRSA credentials. | ~20 min |
| [Agent mode in depth](agent-mode-tutorial.md) | Configure → invoke an agent end-to-end. Stream SSE output, cancel mid-flight, governance caps, optional `mem0` memory. | ~30 min |

Tutorials build on each other, but you can read them in any order. Each one ends with a "What to read next" pointer.

## How AgentTier fits together

Before diving in, the one-paragraph mental model:

> A **Sandbox** is a Kubernetes Pod plus a PVC plus a NetworkPolicy plus a ServiceAccount, all owned by the AgentTier controller. A **SandboxTemplate** (or **ClusterSandboxTemplate**) is the blueprint — image, env, install scripts, network rules, harness config. You create a Sandbox by referencing a template; the controller builds the Pod, the Router gives you a terminal, file API, port forwarding, and (for `mode: agent`) `/configure` and `/invoke` endpoints. Stopping deletes the Pod and keeps the PVC; resuming re-attaches the PVC to a fresh Pod in seconds.

That's it. Everything else is a knob on top of those primitives.
