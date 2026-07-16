# Tutorial: Web UI walkthrough

Take a tour of the AgentTier Web UI: create a sandbox, open the terminal, transfer files, forward a port, and stop / resume / delete. By the end you should be comfortable doing day-to-day work entirely in the browser.

**Time:** ~15 minutes
**Prerequisites:** AgentTier installed (see [Tutorials → Prerequisites](index.md#prerequisites)).

## 1. Open the Web UI

The simplest path is `kubectl port-forward`:

```bash
kubectl port-forward -n agenttier svc/agenttier-webui 8080:80
# open http://localhost:8080
```

For a real deployment behind an Ingress, use the URL your cluster operator gave you (commonly an ALB, NLB, or your own reverse proxy). The Quickstart documents the development bypass; OIDC is covered in [Installation](../installation.md).

You should land on the **Dashboard**. It's empty if this is a fresh install.

## 2. Browse templates

Click **Templates** in the left nav. Out of the box you see:

- `general-coding` — Ubuntu base with Python, Node, and Go preinstalled. The default code-mode sandbox.
- `claude-code-bedrock` — Anthropic Claude Code CLI wired to AWS Bedrock via IRSA.
- `langgraph-agent` — Agent-mode template that runs a LangGraph agent under `/invoke`.

Click any template to see its full YAML. Templates are editable: change the image, add env vars, set `network.allowedDomains`, save. Existing sandboxes are not affected — only new sandboxes pick up the change.

> **Tip:** click **New template** to create a custom blueprint. Templates support inheritance via `parentRef` so you can share a base and override per-team.

## 3. Create your first sandbox

Click **+ New sandbox** on the Dashboard. The dialog needs only:

- **Name** — must be DNS-friendly: lowercase alphanumeric and hyphens.
- **Template** — pick `general-coding` for this tutorial.

Click **Create**. The card appears with status `Creating` and flips to `Running` in about ten seconds. With the warm pool turned on, it's about one second.

Watch what happens behind the scenes:

```bash
kubectl get sandbox -A
# NAMESPACE   NAME              STATUS    TEMPLATE         AGE
# default     tutorial-sbx      Running   general-coding   12s

kubectl get pods -A -l agenttier.io/sandbox=tutorial-sbx
# default   tutorial-sbx-xxxxx   1/1   Running
```

The controller created a Pod, a PVC mounted at `/workspace`, a default-deny NetworkPolicy with DNS allow, and a per-sandbox ServiceAccount.

## 4. Open the terminal

Click **Open terminal** on the sandbox card. A new tab opens with a full PTY: arrow keys, tab completion, ANSI colors, `clear`, `vim`, `htop` — everything works.

Try a few things:

```bash
echo $HOME           # /home/sandbox
whoami               # sandbox
df -h /workspace     # the PVC
python3 -c 'print(2+2)'
node -e 'console.log("hi")'
```

The terminal reconnects automatically on a dropped websocket; status indicator at the top right turns yellow → green when it reattaches.

## 5. Persistent workspace

Drop a file in `/workspace`:

```bash
echo "Hello tutorial" > /workspace/notes.md
ls -la /workspace
```

Now stop the sandbox from the card menu (**⋯** → **Stop**). Status flips to `Stopped`. The Pod is gone:

```bash
kubectl get pods -A -l agenttier.io/sandbox=tutorial-sbx
# (empty)
```

But the PVC is preserved. Click **Resume**. Status flips through `Creating` back to `Running`. Click **Open terminal** again:

```bash
cat /workspace/notes.md
# Hello tutorial
```

The file is still there. This is the core "disposable Pod, durable PVC" model.

## 6. Use the Files panel

Expand the **Advanced** section on the sandbox card and click **Files**.

You can:

- Browse `/workspace` (default) and `/tmp`.
- Click a file to download it to your laptop.
- Click **Upload file** and pick something locally — it's pushed via the REST API into the sandbox's PVC.

A 32 MiB ceiling per file applies (configurable in the Router; mostly there to prevent OOM). For big trees, `tar` the tree and upload the archive with the SDK's `sandbox.files.upload(...)`, or use `kubectl cp`.

Verify in the terminal:

```bash
ls -la /workspace/<file you uploaded>
```

## 7. Forward a port

Run a local web server inside the sandbox:

```bash
cd /workspace
python3 -m http.server 8000
```

Back in the Web UI, on the sandbox card → **Advanced** → **Port forwards**, click **Forward a port**. Enter `8000` and a name like `web`. The card now shows a **Preview** link.

Click it. The link goes through the AgentTier Router with your auth context — no public exposure, no separate Ingress required. You should see the Python server's directory listing.

Stop the forward by clicking the trash icon next to it. The Service goes away; the in-cluster proxy stops accepting requests for that port.

If the Helm chart is configured with `networking.previewDomain`, AgentTier also creates an Ingress so the preview gets its own DNS name. See [Port forwarding](../port-forwarding.md).

## 8. Inspect activity

Click **Activity** in the left nav. You see a paginated list of every state transition: created, started, stopped, command exec, file uploads, port forwards. Filter by sandbox, action, user, and time range. This is your audit trail — also available via `GET /api/v1/audit/events` for shipping into Splunk / Datadog / CloudWatch.

## 9. Settings page

Click **Settings**. Two important controls:

- **Warm pool** — enable to keep N pre-warmed Pods per template. Sandbox creation drops from ~10s to ~1s. Pick low values (1–3 per template) to start; cost grows linearly with the count.
- **Governance editor** — admin-only. JSON editor for cluster and per-namespace policies. See the [Governance tutorial section](../governance.md).

## 10. Delete

When you're done, click **⋯** → **Delete** on the card. The CRD is deleted, the controller's finalizer runs, the Pod and PVC are removed. Files are gone permanently. (Keep the sandbox `Stopped` if you want to come back to it later.)

## What you just learned

- Templates are blueprints; sandboxes are instances.
- Stop preserves the PVC, resume re-attaches it. Delete is forever.
- The terminal, files, and port forwards all run through the Router with your auth context — no separate exposure.
- Activity log captures every action for audit.

## What to read next

- [Python SDK walkthrough](python-sdk.md) — drive the same flows from code.
- [Code mode in depth](code-mode.md) — IDE-style workflows, IRSA, allowedDomains.
- [Agent mode in depth](agent-mode-tutorial.md) — `/configure` → `/invoke` for AI agents.
