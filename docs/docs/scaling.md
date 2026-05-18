# Scaling

AgentTier ships two complementary autoscaling layers in the Helm chart. Both
are off by default; flip them on when you outgrow a fixed-size node group.

## The two layers

### 1. Cluster Autoscaler (CAS) — reactive node scaling

The upstream `kubernetes/autoscaler` Cluster Autoscaler watches for `Pending`
pods and adjusts the size of cloud node groups (EKS managed node groups, GKE
MIGs, AKS VM scale sets, Cluster API MachineDeployments, etc.) to fit them.
When a node sits idle past `scaleDownUnneededTime`, CAS removes it.

Cloud-neutral. Works on EKS, GKE, AKS, OpenStack, vSphere, Cluster API, and
anything else with a CAS provider. AgentTier's chart installs it as a
Deployment in the AgentTier namespace; the cloud-provider IAM (IRSA on EKS,
Workload Identity on GKE) is provisioned out-of-band and supplied via a
Helm value.

```yaml
optional:
  clusterAutoscaler:
    enabled: true
    cloudProvider: aws            # or gce, azure, clusterapi, externalgrpc
    clusterName: my-cluster       # matches the ASG/MIG auto-discovery tag
    serviceAccountRoleArn: arn:aws:iam::ACCOUNT:role/my-cluster-autoscaler
    region: us-east-1
```

Scale-up latency: 60-90 s on EKS (ASG round-trip). Scale-down: a node sits
idle for `scaleDownUnneededTime` (default 10 m), then is drained and
terminated.

### 2. Headroom Deployment — proactive N+1 spare capacity

CAS alone is *reactive*: a sandbox waits on a Pending pod until CAS sees it
and provisions a node, which is a visible delay for interactive workloads.
The headroom Deployment fixes that. It runs N pause Pods at a deeply-negative
PriorityClass, sized to roughly one node's worth of capacity. They squat on a
spare node. When a real sandbox arrives:

1. The scheduler **preempts** a pause Pod (instant — `preemptionPolicy:
   Never` ensures the pause Pod itself never preempts anything).
2. The real sandbox lands on the now-free slot.
3. The evicted pause Pod goes Pending.
4. CAS sees the Pending pause Pod and provisions a fresh spare node.
5. Equilibrium restored: there is always one extra node beyond what's full.

Net effect: real sandboxes always land on already-warm nodes. New nodes come
up in the background without anyone waiting on them.

```yaml
optional:
  headroom:
    enabled: true
    replicas: 4         # how many pause Pods to run
    cpu: 500m           # per replica
    memory: 1Gi         # per replica
    priorityValue: -1000
```

The pause Pods use `registry.k8s.io/pause:3.9` (700 KiB binary, ~50 µCPU
overhead each). The Deployment uses `Recreate` strategy and soft pod anti-
affinity so replicas spread across nodes when possible.

## Sizing the headroom

Two questions to answer:

**How fast do sandboxes typically arrive?** If your peak burst is 5 sandboxes
per minute and a typical sandbox uses 500m CPU + 1 Gi memory, you need
headroom that covers the gap between burst arrival and CAS catching up to
provision a new node. CAS scale-up on EKS is ~90 s, so you want at least 90s
× burst-rate worth of capacity = 7-8 sandboxes worth. With 500m/1Gi each
that's 4 vCPU + 8 Gi.

**What instance type underpins your node group?** Headroom should be sized
to roughly one node's worth of allocatable so each headroom-evict triggers
exactly one CAS scale-up. On `t3.large` (2 vCPU, 8 Gi) with kubelet reserves
giving ~1.9 vCPU + 7.2 Gi allocatable, set `replicas: 3, cpu: 500m, memory:
2Gi` for ~1.5 vCPU + 6 Gi reserved (one node minus daemonset overhead).

When in doubt, start with `replicas: 4, cpu: 500m, memory: 1Gi` and watch
the `cluster_autoscaler_unneeded_count` metric — if it stays high, headroom
is too aggressive and you're paying for cold nodes that never get used.

## Cost trade-offs

| Configuration | Always-on cost | Sandbox-create latency |
| --- | --- | --- |
| Neither (fixed `desired_size`) | Lowest | None below capacity, hard fail above |
| CAS only | One node always cold-starts during a burst | 60-90 s on the first burst sandbox |
| CAS + headroom (`replicas: 4`) | One extra node continuously held warm | ~1 s for the first 4 burst sandboxes |
| CAS + larger headroom | More extra nodes held warm | ~1 s for more burst sandboxes |

For a development cluster: skip both. For a production cluster handling
interactive sandboxes: CAS + headroom. The continuous cost is one always-on
spare node, which on AWS `t3.large` is ~$60/month — usually trivial next to
the latency win. Spot instances cut that further if your tolerance allows.

## Cloud-specific node group setup

CAS auto-discovers node groups via tags. Tag your existing ASG / MIG /
scale-set with both:

```
k8s.io/cluster-autoscaler/enabled = true
k8s.io/cluster-autoscaler/<cluster-name> = owned
```

### EKS

```hcl
resource "aws_eks_node_group" "main" {
  scaling_config {
    desired_size = 2
    min_size     = 1
    max_size     = 10           # raise from the chart's typical 3-4
  }
  tags = {
    "k8s.io/cluster-autoscaler/enabled"        = "true"
    "k8s.io/cluster-autoscaler/${cluster_name}" = "owned"
  }
}
```

CAS needs IRSA. The Helm chart's `serviceAccountRoleArn` value adds the
`eks.amazonaws.com/role-arn` annotation; provision the IAM role with the
standard CAS policy (autoscaling:Describe* + autoscaling:SetDesiredCapacity
+ ec2:Describe* + the `aws:ResourceTag/k8s.io/cluster-autoscaler/<cluster>:
"owned"` condition).

### GKE / AKS

GKE: prefer GKE's native cluster autoscaler instead of CAS — it's tighter
integration with the cloud control plane. Set `optional.clusterAutoscaler.
enabled: false` and enable autoscaling on your GKE node pool.

AKS: AKS has a built-in cluster autoscaler. Same recommendation — use it,
not CAS. Headroom still works because it's autoscaler-agnostic.

### Karpenter (AWS-only)

If you're on EKS and want sub-30 s scale-up with mixed instance types and
automatic Spot/On-Demand fallback, Karpenter is a strict upgrade over CAS.
The Helm chart doesn't ship Karpenter today (tracked as a P2 follow-up);
install it separately, then enable AgentTier's `optional.headroom.enabled:
true`. The headroom pattern works identically — Karpenter respects the same
negative `PriorityClass` and provisions a fresh node when the evicted pause
pods go Pending.

## Verifying it works

After enabling both flags and running `helm upgrade`:

```bash
# CAS should be Running and discovering your node group:
kubectl logs -n agenttier deployment/agenttier-cluster-autoscaler --tail=50

# Headroom Pods should be Running:
kubectl get pods -n agenttier -l app.kubernetes.io/component=headroom

# Force a scale-up by creating sandboxes that exceed current capacity:
for i in $(seq 1 10); do
  kubectl apply -f - <<EOF
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata: {name: scale-test-$i}
spec: {templateRef: {name: general-coding, kind: ClusterSandboxTemplate}}
EOF
done

# Watch nodes scale up:
kubectl get nodes -w
```

You should see CAS log `Scale-up: setting group X size to Y` within a few
seconds, and the new node Ready in 60-90 seconds. Delete the sandboxes and
nodes scale back down after `scaleDownUnneededTime`.
