# Port forwarding

Any sandbox container port can be exposed to authenticated users. AgentTier
creates a ClusterIP Service targeting the sandbox Pod and, when a preview
domain is configured, an Ingress resource pointing at it.

## Expose a port

```bash
# Web UI: click "Forward" on the running sandbox card
# CLI: (coming soon)
# REST:
curl -X POST https://agenttier.company.com/api/v1/sandboxes/my-sbx/ports \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"port": 8080, "protocol": "http"}'
```

Response:

```json
{
  "port": 8080,
  "protocol": "http",
  "internalUrl": "http://pf-my-sbx-8080.default.svc.cluster.local:8080",
  "previewUrl": "https://sandbox-my-sbx-8080.preview.agenttier.company.com/"
}
```

## Access the forwarded port

Two paths:

### 1. Router-proxied preview (works without DNS setup)

```
GET https://agenttier.company.com/api/v1/sandboxes/{id}/preview/{port}/
```

The Router authenticates, checks the sandbox is running, and reverse-proxies
into the in-cluster Service. This is what the "preview" link in the Web UI
opens. No Ingress required — useful for dev clusters, `kind`, and E2E tests.

### 2. Public preview URL (requires Ingress + DNS)

If `networking.previewDomain` is set in Helm values, AgentTier also creates
an Ingress at `https://sandbox-{sandbox}-{port}.{previewDomain}/`. You need:

- An ingress controller (NGINX, ALB, Traefik, …). Set
  `networking.portForwardIngressClass` to its class name.
- Wildcard DNS `*.{previewDomain}` pointing at the ingress controller.
- TLS for the wildcard (cert-manager + a DNS-01 issuer works well).

## Remove a port

```bash
curl -X DELETE https://agenttier.company.com/api/v1/sandboxes/my-sbx/ports/8080 \
  -H "Authorization: Bearer $TOKEN"
```

This deletes the Service and Ingress and clears the entry from the sandbox's
`status.forwardedPorts`.

## Authorization

All port-forward endpoints go through the same owner check as the rest of the
Sandbox API: the caller must own the sandbox or be an admin. The Web UI and CLI
use the same REST endpoints.

## Reaching a private-mode EKS cluster (SSM Session Manager)

The section above covers port-forwarding *into a sandbox* through the Router
API — it works the same regardless of how the cluster's own API server is
exposed. This section covers the separate concern of a human operator
reaching the **Kubernetes API server itself** when the `terraform/aws-eks`
module is deployed with `endpoint_access_mode = "private"` (see
[Security: EKS API endpoint modes](security.md#eks-api-endpoint-modes)), where
there is no public endpoint to point `kubectl` at.

**Approach: SSM Session Manager port-forward through a worker node.** The
module's managed node groups carry `AmazonSSMManagedInstanceCore` on their
instance role specifically for this purpose (no separate bastion host, no
inbound security-group rule, no SSH key — access is IAM-gated and
outbound-initiated only, via the SSM agent already present on EKS-optimized
AL2/AL2023 AMIs).

!!! note "Prerequisite: the Session Manager plugin (operator workstation only)"
    `aws ssm start-session` streams bytes through a separate
    [`session-manager-plugin`](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
    binary that the AWS CLI does **not** bundle — without it the command fails
    immediately with `SessionManagerPlugin is not found`. Install it once on the
    machine you run `kubectl` from. It is **not** needed to deploy: `deploy.sh`
    reaches a `private` cluster via CodeBuild-in-VPC, not SSM — this is purely an
    operator-access tool.

    ```bash
    # macOS
    brew install --cask session-manager-plugin
    # Debian/Ubuntu
    curl -o /tmp/smp.deb "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/ubuntu_64bit/session-manager-plugin.deb" && sudo dpkg -i /tmp/smp.deb
    # RHEL / Amazon Linux
    sudo yum install -y "https://s3.amazonaws.com/session-manager-downloads/plugin/latest/linux_64bit/session-manager-plugin.rpm"

    session-manager-plugin --version   # verify
    ```

```bash
# 1. Find a running managed-node instance in the cluster (cluster_name comes
#    from the terraform output, not hardcoded — a non-default cluster_name
#    would otherwise match zero instances).
cd terraform/aws-eks
CLUSTER_NAME=$(terraform output -raw cluster_name)
INSTANCE=$(aws ec2 describe-instances \
  --filters "Name=tag:eks:cluster-name,Values=${CLUSTER_NAME}" \
            "Name=instance-state-name,Values=running" \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)

# 2. Resolve the private API endpoint host from the Terraform output
#    (cluster_endpoint with the https:// scheme stripped). The VPC has
#    enable_dns_hostnames/enable_dns_support = true, so this hostname
#    resolves correctly from inside the VPC.
APISERVER=$(terraform output -raw cluster_endpoint_private_host)
cd -

# 3. Start the tunnel: local :6443 -> apiserver:443, relayed through the node.
aws ssm start-session --target "$INSTANCE" \
  --document-name AWS-StartPortForwardingSessionToRemoteHost \
  --parameters "{\"host\":[\"$APISERVER\"],\"portNumber\":[\"443\"],\"localPortNumber\":[\"6443\"]}"
```

### TLS/SNI caveat

The API server's TLS certificate is issued for the real endpoint hostname,
not for `localhost` — connecting `kubectl` straight to
`https://localhost:6443` fails certificate verification even though the
tunnel itself is healthy. Two options, in order of preference:

**Preferred: set `tls-server-name` in kubeconfig** so `kubectl` still
validates the certificate correctly through the tunnel:

```bash
kubectl config set-cluster agenttier-private --server="https://localhost:6443"
kubectl config set-cluster agenttier-private --tls-server-name="$APISERVER"
kubectl config set-context agenttier-private --cluster=agenttier-private --user=<your-eks-user>
kubectl --context agenttier-private get nodes
```

**Fallback: `insecure-skip-tls-verify`** on the tunnel-scoped context only, if
your `kubectl`/client-go version doesn't honor `tls-server-name` correctly.
This disables certificate validation for that context — acceptable for a
tunnel that only exists for the duration of the SSM session, but do not reuse
that context configuration anywhere the tunnel isn't active.

### Reaching the web UI through the same tunnel

Once the API tunnel from step 3 is up, a normal `kubectl port-forward` works
through it exactly as it would against a public endpoint:

```bash
kubectl --context agenttier-private port-forward -n agenttier svc/agenttier-webui 8080:80
```

No second SSM session is needed — `kubectl port-forward` itself talks to the
API server (already tunneled) to set up the Pod-level forward.
