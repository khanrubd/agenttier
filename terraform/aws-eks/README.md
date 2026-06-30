# AgentTier — EKS reference module

Terraform that provisions a production-shaped Amazon EKS cluster with everything
AgentTier needs, and (optionally) installs AgentTier itself from the published
Helm chart. Use it as a starting point for your own infrastructure-as-code, not
as a turnkey production deployment — review and harden it for your environment
first (see [Production notes](#production-notes)).

It is built on the official upstream modules
([`terraform-aws-modules/vpc`](https://github.com/terraform-aws-modules/terraform-aws-vpc)
and [`terraform-aws-modules/eks`](https://github.com/terraform-aws-modules/terraform-aws-eks))
so it stays close to community best practice and is easy to extend.

## What it creates

| Area | Resource |
|------|----------|
| Networking | VPC across `az_count` AZs (default 3) with public + private subnets and a NAT gateway, tagged for Kubernetes load-balancer subnet auto-discovery |
| Cluster | EKS cluster (Kubernetes `1.30` by default) with the public API endpoint enabled and IRSA/OIDC turned on |
| Compute | Two managed node groups: a general **`default`** group (`t3.large`, 2–4 nodes) and a dedicated **`gvisor`** group labelled `agenttier.io/runtime=gvisor` (optionally tainted) |
| Storage | AWS EBS CSI driver as an EKS add-on, backed by its own IRSA role |
| Ingress | AWS Load Balancer Controller (Helm) with its IRSA role + the AWS-maintained IAM policy |
| Auth | Cognito user pool, SPA app client (PKCE), and an `agenttier-admins` group for OIDC login |
| App | AgentTier Helm release from `https://agenttier.github.io/agenttier/charts` (toggle with `install_agenttier`) |

The `gvisor` node group only provides correctly **labelled** nodes so that pods
using AgentTier's gVisor RuntimeClass (nodeSelector `agenttier.io/runtime=gvisor`)
schedule onto them. Installing the gVisor runtime (`runsc`) + RuntimeClass on
those nodes is a separate step — use the AgentTier chart's gVisor add-on or a
`runsc` installer DaemonSet. Set `gvisor_node_taint = true` to keep non-gVisor
workloads off these nodes.

## Prerequisites

- Terraform >= 1.5
- AWS CLI v2, configured with credentials that can create VPC/EKS/IAM resources
  (`aws configure`). The `kubernetes`/`helm` providers authenticate to the new
  cluster by shelling out to `aws eks get-token`, so the AWS CLI must be on
  `PATH`.
- `kubectl` and `helm` for post-apply verification.

## Usage

```bash
terraform init
terraform plan
terraform apply      # ~15-20 minutes (control plane + nodes + add-ons)

# Point kubectl at the new cluster
$(terraform output -raw kubeconfig_command)

# Verify
kubectl get nodes -L agenttier.io/runtime
kubectl get pods -n agenttier          # if install_agenttier = true (default)
```

To stand up only the cluster + add-ons and install AgentTier yourself:

```bash
terraform apply -var=install_agenttier=false
helm repo add agenttier https://agenttier.github.io/agenttier/charts
helm repo update
helm install agenttier agenttier/agenttier --namespace agenttier --create-namespace
```

### Authentication

By default the AgentTier release is wired to the Cognito user pool this module
creates (`agenttier_oidc_auth = true`). Create a user in the pool, add them to
the `agenttier-admins` group, and log in through the web UI. The exact Helm
values used are also exposed as the `agenttier_helm_auth_values` output.

For a quick evaluation without OIDC, disable the wiring and turn on dev auth:

```bash
terraform apply \
  -var=agenttier_oidc_auth=false \
  -var='agenttier_extra_values=["auth:\n  devAuth: true\n"]'
```

> Dev auth grants blanket admin to every request — never use it on a cluster
> reachable from the public internet.

## Key variables

| Variable | Default | Description |
|----------|---------|-------------|
| `region` | `us-east-1` | AWS region |
| `cluster_name` | `agenttier` | Cluster name + resource prefix |
| `kubernetes_version` | `1.30` | EKS control-plane version |
| `az_count` | `3` | Number of AZs (2–3) |
| `vpc_cidr` | `10.0.0.0/16` | VPC CIDR (subnets carved as /20) |
| `single_nat_gateway` | `true` | One shared NAT gateway; set `false` for HA egress |
| `cluster_endpoint_public_access_cidrs` | `["0.0.0.0/0"]` | CIDRs allowed to reach the K8s API |
| `node_instance_type` / `node_{min,desired,max}_size` | `t3.large` / `2`/`2`/`4` | Default node group |
| `gvisor_node_instance_type` / `gvisor_node_{min,desired,max}_size` | `t3.large` / `1`/`1`/`3` | gVisor node group |
| `gvisor_node_taint` | `false` | Taint gVisor nodes so only gVisor pods land there |
| `install_aws_load_balancer_controller` | `true` | Install the AWS LB Controller |
| `aws_load_balancer_controller_chart_version` | `1.8.1` | LB controller chart version |
| `install_agenttier` | `true` | Install the AgentTier Helm chart |
| `agenttier_chart_version` | `""` (latest) | Pin the AgentTier chart version |
| `agenttier_oidc_auth` | `true` | Wire AgentTier auth to the Cognito pool |
| `agenttier_extra_values` | `[]` | Extra Helm values (raw YAML) for the AgentTier release |
| `create_test_user` | `false` | Seed an admin user in Cognito |

See `variables.tf` for the full list (Cognito domain prefix, tags, seed-user
email/password, etc.).

## Outputs

`cluster_name`, `cluster_endpoint`, `cluster_oidc_issuer_url`, `region`,
`kubeconfig_command`, `vpc_id`, and `aws_load_balancer_controller_role_arn` are
the headline outputs. The module also exports the cluster CA data (sensitive),
security group IDs, subnet IDs, the EBS CSI role ARN, the OIDC provider ARN, and
the Cognito pool/client/issuer/domain plus a ready-to-use auth values snippet.
Run `terraform output` to see them all.

## Cost

A rough order of magnitude for the defaults (`us-east-1`, on-demand):

| Resource | ~Monthly |
|----------|----------|
| EKS control plane | $73 |
| 3× `t3.large` (default + gVisor nodes) | ~$200 |
| NAT gateway (single) | ~$32 |
| EBS / ELB / data transfer | variable |

Run `terraform destroy` when you are done to stop the charges.

## Production notes

This module is a reference, not a hardened production baseline. Before relying
on it:

- Restrict `cluster_endpoint_public_access_cidrs` (and consider enabling private
  endpoint access) instead of leaving the API open to `0.0.0.0/0`.
- Set `single_nat_gateway = false` for per-AZ NAT redundancy.
- Pin `kubernetes_version`, `agenttier_chart_version`, and
  `aws_load_balancer_controller_chart_version` and upgrade them deliberately.
- Configure a remote Terraform backend with state locking (this module ships no
  backend block so it stays drop-in).
- Review the AgentTier chart values for your governance, warm-pool, and
  observability needs — this module installs the chart with defaults plus OIDC.

## Teardown

```bash
# If you installed AgentTier and want it gone first:
terraform apply -var=install_agenttier=false   # or: helm uninstall agenttier -n agenttier

terraform destroy
```

If `destroy` stalls on the VPC, it is usually because the AWS Load Balancer
Controller still has an ALB/NLB (and its ENIs) attached — delete the AgentTier
Ingress/Services first so the load balancers are cleaned up, then re-run
`terraform destroy`.
