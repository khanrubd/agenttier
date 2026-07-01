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
| **ECR** | Nine ECR repositories (controller, router, web-ui, and six sandbox images) with AES256 encryption, scan-on-push, and lifecycle policies — see [ECR repositories](#ecr-repositories) |
| App | AgentTier Helm release wired to the ECR repos and Cognito pool (toggle with `install_agenttier`) |
| CodeBuild (opt-in) | CodeBuild project + encrypted S3 source bucket for building images without a local Docker daemon — off by default (set `enable_codebuild = true`) |

The `gvisor` node group only provides correctly **labelled** nodes so that pods
using AgentTier's gVisor RuntimeClass (nodeSelector `agenttier.io/runtime=gvisor`)
schedule onto them. Installing the gVisor runtime (`runsc`) + RuntimeClass on
those nodes is a separate step — use the AgentTier chart's gVisor add-on or a
`runsc` installer DaemonSet. Set `gvisor_node_taint = true` to keep non-gVisor
workloads off these nodes.

## Prerequisites

- Terraform >= 1.5
- AWS CLI v2, configured with credentials that can create VPC/EKS/IAM/ECR resources
  (`aws configure`). The `kubernetes`/`helm` providers authenticate to the new
  cluster by shelling out to `aws eks get-token`, so the AWS CLI must be on `PATH`.
- Docker with buildx support (for the default local-build → ECR push path).
- `kubectl` and `helm` for post-apply verification.

## Usage

The recommended deploy path is `./deploy.sh --target=eks` from the repo root,
which calls `terraform apply`, reads ECR outputs, builds and pushes images, and
installs the Helm chart in one shot. See the top-level `README.md` for the full
deploy guide. Direct Terraform usage:

```bash
terraform init
terraform plan
terraform apply      # ~15-20 minutes (control plane + nodes + ECR repos + add-ons)

# Point kubectl at the new cluster
$(terraform output -raw kubeconfig_command)

# Build and push images to ECR (deploy.sh handles this automatically)
ECR_REGISTRY=$(terraform output -raw ecr_registry)
IMAGE_TAG=$(git rev-parse --short HEAD)
aws ecr get-login-password --region us-east-1 \
  | docker login --username AWS --password-stdin "$ECR_REGISTRY"
docker buildx build --platform linux/amd64 \
  -t "$ECR_REGISTRY/agenttier/controller:$IMAGE_TAG" \
  -f Dockerfile.controller --push .

# Verify
kubectl get nodes -L agenttier.io/runtime
kubectl get pods -n agenttier          # if install_agenttier = true (default)
```

### ECR repositories

`terraform apply` creates nine ECR repositories consumed by the build path —
one per AgentTier service image and one per default sandbox image. Every
`ClusterSandboxTemplate` shipped by the Helm chart references one of the six
sandbox repos; without them a sandbox using any non-general template would
enter `ImagePullBackOff` on a from-source EKS deploy. `images/minimal` is not
referenced by any default template and is therefore not provisioned here.

| Output | Repository | Used by |
|--------|-----------|---------|
| `ecr_registry` | `<account>.dkr.ecr.<region>.amazonaws.com` | `docker login`, `--set global.registry=<ecr_registry>/agenttier` |
| `ecr_controller_url` | `…/<prefix>/controller` | controller push + Helm |
| `ecr_router_url` | `…/<prefix>/router` | router push + Helm |
| `ecr_webui_url` | `…/<prefix>/web-ui` | web-ui push + Helm |
| `ecr_sandbox_general_url` | `…/<prefix>/sandbox-general` | `general-coding` template |
| `ecr_sandbox_claude_code_url` | `…/<prefix>/sandbox-claude-code` | `claude-code-bedrock` template |
| `ecr_sandbox_openclaw_url` | `…/<prefix>/sandbox-openclaw` | `openclaw-bedrock` template |
| `ecr_sandbox_langgraph_url` | `…/<prefix>/sandbox-langgraph` | `langgraph-agent` template |
| `ecr_sandbox_rl_url` | `…/<prefix>/sandbox-rl` | `rl-rollout` template |
| `ecr_sandbox_strands_bedrock_url` | `…/<prefix>/sandbox-strands-bedrock` | `strands-bedrock` template |

All repos are encrypted at rest (AES256), have scan-on-push enabled, and apply
lifecycle policies that expire untagged images after 1 day and keep the 10 most
recent tagged images.

### Image tag derivation

Never use `latest`. The canonical tag is derived by `hack/lib/version.sh`:
- Clean tree at a release tag → value from the `VERSION` file (e.g. `0.8.1`)
- Dev / dirty tree → `sha-<7-char-git-sha>[-dirty]`

`deploy.sh` computes the tag once, builds with it, pushes to ECR, and passes the
same value to Helm via `--set *.image.tag=<tag>`.

### CodeBuild opt-in

By default (`enable_codebuild = false`) no CodeBuild resources are created and
the build path is **local Docker buildx → ECR push**. Set `enable_codebuild = true`
only when a local Docker daemon is unavailable:

```bash
terraform apply -var=enable_codebuild=true
```

When enabled, the following additional resources are created:

| Output | Resource |
|--------|----------|
| `codebuild_project` | CodeBuild project name |
| `codebuild_s3_bucket` | Encrypted S3 bucket for source zip uploads |
| `codebuild_timeout_minutes` | Max build duration (default 30 min; deploy.sh respects this) |

The S3 bucket enforces TLS-only access and blocks all public access. The
CodeBuild IAM role has least-privilege permissions (ECR push to the four repos,
S3 read for source, CloudWatch Logs write).

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

> **Warning:** Dev auth grants blanket admin to every request — never use it on
> a cluster reachable from the public internet. It is local-only.

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
| `ecr_repo_prefix` | `""` (uses `cluster_name`) | Override ECR repository name prefix |
| `enable_codebuild` | `false` | Enable the opt-in CodeBuild build path |
| `codebuild_timeout_minutes` | `30` | Max CodeBuild run duration (5–480 min) |

See `variables.tf` for the full list (Cognito domain prefix, tags, seed-user
email/password, etc.).

## Outputs

Headline outputs:

| Output | Description |
|--------|-------------|
| `ecr_registry` | ECR registry hostname — pass to `docker login` and `deploy.sh` |
| `ecr_controller_url` | Full ECR URL for the controller image |
| `ecr_router_url` | Full ECR URL for the router image |
| `ecr_webui_url` | Full ECR URL for the web-ui image |
| `ecr_sandbox_general_url` | Full ECR URL for the sandbox-general image |
| `ecr_sandbox_claude_code_url` | Full ECR URL for the sandbox-claude-code image |
| `ecr_sandbox_openclaw_url` | Full ECR URL for the sandbox-openclaw image |
| `ecr_sandbox_langgraph_url` | Full ECR URL for the sandbox-langgraph image |
| `ecr_sandbox_rl_url` | Full ECR URL for the sandbox-rl image |
| `ecr_sandbox_strands_bedrock_url` | Full ECR URL for the sandbox-strands-bedrock image |
| `codebuild_project` | CodeBuild project name (empty when `enable_codebuild = false`) |
| `codebuild_s3_bucket` | CodeBuild source S3 bucket (empty when `enable_codebuild = false`) |
| `codebuild_timeout_minutes` | Max CodeBuild run duration (used by `deploy.sh` polling loop) |
| `cluster_name` | EKS cluster name |
| `cluster_endpoint` | Kubernetes API endpoint |
| `kubeconfig_command` | Run this to configure `kubectl` |
| `cognito_issuer_url` | Cognito OIDC issuer URL |
| `agenttier_helm_auth_values` | Ready-to-use Helm values wiring auth to Cognito |

Run `terraform output` to see all outputs including cluster CA data (sensitive),
subnet IDs, security group IDs, IRSA role ARNs, and Cognito details.

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
