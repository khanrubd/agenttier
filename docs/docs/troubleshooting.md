# Troubleshooting

## Terminal drops every 20-60 minutes (older sandboxes)

If your sandbox uses the legacy SPDY-exec terminal path (any sandbox created
on a template without `harness.useHTTPExec: true`, or on a pod that doesn't
have the in-pod runtime baked in), the browser terminal will reconnect every
20-60 minutes regardless of LB idle settings. The cause is the EKS apiserver
recycling long-lived streaming connections, which is unavoidable on the SPDY
path.

What you'll see in Router logs: nothing alarming — the WebSocket reconnect is
fast and the tmux wrap means your shell + running processes survive the drop.
What you'll see in the browser: a brief "Reconnecting…" banner and your
prompt re-appears. If you're running a long task (gdownload, builds), it
keeps going inside tmux even though the terminal blinked.

To eliminate the drop entirely, opt the template into the in-pod HTTP-PTY
path:

```yaml
harness:
  useHTTPExec: true   # also routes /exec, /files, /invoke through the pod runtime
```

The runtime listens on port 9000 inside the pod; the Router dials it directly
TCP-to-TCP, so the apiserver isn't in the request path and there's nothing to
recycle. All sandbox reference images (`general-coding`, `claude-code`,
`openclaw`, `strands-bedrock`, `minimal`, `langgraph`, `rl`) ship with the
runtime baked in. Custom images that don't have the runtime fall back to
SPDY transparently — same behavior as before. To verify which transport a
session used, look in Router logs for:

- `terminal session via HTTP-PTY` — success on the new path.
- `HTTP-PTY fallback to SPDY` with a structured `reason` field — fallback
  (e.g. `runtime healthz failed`, `pod IP not yet assigned`).

## Terminal disconnects after a long idle period

Every released Router sends RFC 6455 WebSocket ping control frames **and**
application-level heartbeat messages every 30 seconds, so any load balancer
with an idle timeout ≥ 60s will see traffic in both directions and keep the
connection open. If you still see drops:

- Verify the LB idle timeout is at least 120s. AWS Classic Load Balancer
  defaults to **60s** — set
  `service.beta.kubernetes.io/aws-load-balancer-connection-idle-timeout: "3600"`
  on the web-ui Service if you're sticking with Classic ELB.
- AWS ALB defaults to 60s as well. Set
  `alb.ingress.kubernetes.io/load-balancer-attributes: idle_timeout.timeout_seconds=4000`
  (the chart sets this by default under `optional.ingress.annotations`).
- With multi-replica Routers, enable sticky sessions on the target group so a
  reconnecting browser lands on the same pod. The chart's default ALB
  annotations include `stickiness.enabled=true`.
- The browser Terminal tracks the server heartbeat and automatically
  reconnects after ~90s of silence, so transient network blips surface as a
  brief "stale" banner rather than a dead shell.

## Terminal shows garbled text / `stty size` returns `0 0`

The Router's SPDY exec call must set `StreamOptions.Tty: true`. Upgrade to the
latest release.

## Sandbox stuck in `Creating` with `ImagePullBackOff`

1. `kubectl get clustersandboxtemplate <name> -o yaml` and verify the image reference.
2. Check the node's network path to the registry. ECR images need
   `AmazonEC2ContainerRegistryReadOnly` on the node role.
3. For private registries, set `spec.image.pullSecret` in the sandbox spec.

## `helm install` hangs or times out

The most common cause is waiting for a PVC that never binds. `kubectl get
pvc -A` will show it. Fix the storage class:

- If there isn't one, install an EBS / PD CSI driver first.
- If there is, make sure `volumeBindingMode: WaitForFirstConsumer` is set
  (default) — `Immediate` binding is also supported and is what the warm pool
  uses for sub-second starts.

## Port-forward preview returns 502

The preview proxy returns `502 Bad Gateway` when the upstream Service has no
endpoints. Check:

1. Is the sandbox actually running? (`kubectl get sandbox`)
2. Is the target port actually listening inside the sandbox?
   (`sandbox.commands.run("ss -tlnp")` from the SDK or via the terminal)
3. Is there a NetworkPolicy blocking traffic from the Router namespace?

## `./deploy.sh --target=local` hangs or fails on `go mod download`

If a controller, router, or sandbox image build times out or fails with
`lookup proxy.golang.org: ... i/o timeout` (or similar DNS/connect failures),
`proxy.golang.org` is unreachable from your network — common on corporate
VPNs and some captive networks. Set `GOPROXY=direct` before running
`deploy.sh` to fetch Go modules straight from their VCS origins instead:

```bash
GOPROXY=direct ./deploy.sh --target=local
```

or add `GOPROXY=direct` to `config/config.env`. `deploy.sh` passes this
through as `--build-arg GOPROXY=...` to every Go-based image build
(`docker/Dockerfile.controller`, `docker/Dockerfile.router`, and all 6 sandbox
Dockerfiles) on both the `--target=local` path and the `--target=eks`
local-buildx path. The `--target=eks` CodeBuild path does not pass this
build-arg (it runs in AWS where `proxy.golang.org` is reachable) and always
uses each Dockerfile's default. The default (`https://proxy.golang.org,direct`)
is unchanged if you don't set it, so this is opt-in and doesn't affect normal
networks.

## Docker Hub rate limits on EKS nodes

All first-party Dockerfiles use `public.ecr.aws/docker/library/*` base images
to avoid anonymous Docker Hub pulls. If you see `TOOMANYREQUESTS`, verify a
custom template or sidecar didn't introduce a `FROM alpine:latest` against
`docker.io`.

## Exposing a service publicly with 0.0.0.0/0

Don't. Use `loadBalancerSourceRanges` to restrict by IP, or put the web-ui
behind an Ingress with OIDC authentication. Exposing the Router or Web UI to
the open internet without auth gives anyone with the address the ability to
create sandboxes and execute arbitrary code in your cluster.
