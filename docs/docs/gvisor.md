# gVisor sandboxing (runsc)

gVisor (`runsc`) is a userspace kernel: it intercepts a container's syscalls and
re-implements the Linux ABI in Go, so sandbox code never talks to the host
kernel directly. That shrinks the blast radius of a Linux kernel CVE (Dirty
Pipe, OverlayFS, io_uring, netfilter) from "node compromise" to "single-pod
compromise" and sanitizes syscall arguments more aggressively than seccomp.

AgentTier wires gVisor through a `RuntimeClass`; sandboxes and templates opt in
per workload.

## Trade-offs

- **Cost:** ~10–30% slowdown on syscall-heavy workloads (compilation, large
  file IO). Negligible for typical agent inference and small builds.
- **Limits:** no easy GPU passthrough; some syscalls are unimplemented (parts of
  io_uring, raw sockets, some ptrace/FUSE paths).
- **Posture:** keep it **optional** for single-tenant trusted users; prefer it
  for agent-mode workloads where tenant code is closer to untrusted.

## Enable

```yaml
security:
  gvisor:
    enabled: true                 # renders the `gvisor` RuntimeClass
    runtimeClassName: gvisor
    nodeSelector:
      agenttier.io/runtime: gvisor   # gVisor pods pin to nodes labelled this way
    installer:
      enabled: false              # see "Installing runsc on nodes" below
```

This renders a `gvisor` RuntimeClass whose `scheduling.nodeSelector` keeps
runsc pods on your gVisor node group while the rest of the fleet stays on runc.

## Opt a template (or sandbox) in

```yaml
apiVersion: agenttier.io/v1alpha1
kind: ClusterSandboxTemplate
metadata:
  name: claude-code-bedrock
spec:
  runtimeClass: gvisor      # this template's sandboxes run under runsc
  # ...
```

A single sandbox can also set `spec.runtimeClass: gvisor` directly. The pod
builder sets `Pod.spec.runtimeClassName`, and the scheduler places it on a
gVisor-labelled node.

## Installing runsc on nodes

The `gvisor` RuntimeClass needs `runsc` present on the node. Three options:

1. **Node image / Bottlerocket variant (recommended for production).** Bake
   runsc into the AMI or use a Bottlerocket variant with gVisor. The
   [Terraform module](https://github.com/agenttier/agenttier/tree/main/terraform)
   provisions a dedicated gVisor node group labelled `agenttier.io/runtime=gvisor`.
2. **Installer DaemonSet (opt-in).** Set `security.gvisor.installer.enabled=true`
   to run a privileged DaemonSet that downloads runsc + the containerd shim onto
   labelled nodes and registers the runsc runtime in containerd. **Standard
   containerd nodes only** (AL2 / AL2023 / Ubuntu with systemd); not for
   Bottlerocket or read-only-root images. It needs host mounts + privileged —
   review it before enabling.
3. **Manual.** Follow the upstream gVisor containerd quick-start on each node.

## Verify

```bash
kubectl get runtimeclass gvisor
kubectl run gv --rm -it --restart=Never \
  --overrides='{"spec":{"runtimeClassName":"gvisor"}}' \
  --image=public.ecr.aws/docker/library/alpine:3.20 -- dmesg | head
```

Under runsc, `dmesg` shows the gVisor kernel banner rather than the host kernel
ring buffer — a quick confirmation the pod is sandboxed.
