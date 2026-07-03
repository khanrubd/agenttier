# AgentTier — EKS reference module

Terraform that provisions a production-shaped Amazon EKS cluster with everything
AgentTier needs (VPC, cluster, node groups, ECR, Cognito, IRSA roles). It is
pure AWS infrastructure — installing AgentTier itself onto the cluster is a
separate step handled by `./deploy.sh --target=eks` (see
[Usage](#usage)). Use this module as a starting point for your own
infrastructure-as-code, not as a turnkey production deployment — review and
harden it for your environment first (see [Production notes](#production-notes)).

It is built on the official upstream modules
([`terraform-aws-modules/vpc`](https://github.com/terraform-aws-modules/terraform-aws-vpc)
and [`terraform-aws-modules/eks`](https://github.com/terraform-aws-modules/terraform-aws-eks))
so it stays close to community best practice and is easy to extend.

## What it creates

| Area | Resource |
|------|----------|
| Networking | VPC across `az_count` AZs (default 3) with public + private subnets and a NAT gateway, tagged for Kubernetes load-balancer subnet auto-discovery |
| Cluster | EKS cluster (Kubernetes `1.30` by default) with IRSA/OIDC turned on. API endpoint exposure is controlled by `endpoint_access_mode` — see [EKS API endpoint modes](#eks-api-endpoint-modes) |
| Compute | Two managed node groups: a general **`default`** group (`t3.large`, 2–4 nodes) and a dedicated **`gvisor`** group labelled `agenttier.io/runtime=gvisor` (optionally tainted). Both node roles get `AmazonSSMManagedInstanceCore` so nodes double as SSM Session Manager targets — see [Human ops: SSM port-forward](#human-ops-ssm-port-forward) |
| Storage | AWS EBS CSI driver as an EKS add-on, backed by its own IRSA role |
| Ingress | AWS Load Balancer Controller IRSA role + the AWS-maintained IAM policy (terraform only provisions the role; the Helm install itself runs from `deploy.sh`/CodeBuild — see [Apply is pure-AWS](#apply-is-pure-aws)) |
| Auth | Cognito user pool, SPA app client (PKCE), and an `agenttier-admins` group for OIDC login |
| Access control | Scoped **EKS Access Entries** for the deploying principal and (when `enable_codebuild = true`) the CodeBuild role — replaces blanket creator-admin. See [Cluster access](#cluster-access) |
| Observability | Control-plane logging (`api`, `audit`, `authenticator`) to a managed, retention-bounded CloudWatch log group; optional GuardDuty EKS Protection. See [Control-plane logging](#control-plane-logging-and-guardduty) |
| **ECR** | Nine ECR repositories (controller, router, web-ui, and six sandbox images) with AES256 encryption, scan-on-push, and lifecycle policies — see [ECR repositories](#ecr-repositories) |
| CodeBuild (opt-in) | CodeBuild project + encrypted S3 source bucket for building images without a local Docker daemon — off by default (set `enable_codebuild = true`). Also the required in-VPC deploy actor when `endpoint_access_mode = "private"` |
| State | S3 backend with the native S3 lockfile (`use_lockfile`, no DynamoDB table) — see [State backend](#state-backend) |

AgentTier itself is **not** installed by this module — the canonical install path is `./deploy.sh --target=eks` from the repo root (it runs the Load Balancer Controller and AgentTier Helm installs after `terraform apply`). The old `install_agenttier` terraform variable and its `helm_release` are gone — see [Breaking changes](#breaking-changes-in-this-hardening-pass).

The `gvisor` node group only provides correctly **labelled** nodes so that pods
using AgentTier's gVisor RuntimeClass (nodeSelector `agenttier.io/runtime=gvisor`)
schedule onto them. Installing the gVisor runtime (`runsc`) + RuntimeClass on
those nodes is a separate step — use the AgentTier chart's gVisor add-on or a
`runsc` installer DaemonSet. Set `gvisor_node_taint = true` to keep non-gVisor
workloads off these nodes.

## Prerequisites

- Terraform **>= 1.10** (bumped from >= 1.5 — the S3 backend's native lockfile,
  `use_lockfile = true`, requires it; see [State backend](#state-backend)).
  `scripts/lib/common.sh`'s `at::check_eks_prereqs` enforces this and fails
  loudly with an install hint if your `terraform` is older.
- AWS CLI v2, configured with credentials that can create VPC/EKS/IAM/ECR
  resources (`aws configure`). Terraform's own provider is AWS-only now (see
  [Apply is pure-AWS](#apply-is-pure-aws)) — the AWS CLI is still needed by
  `deploy.sh`'s post-apply steps (`aws eks update-kubeconfig`, ECR login).
- Docker with buildx support — required **only** for the default local-build →
  ECR push path. Not required when building in-cloud via CodeBuild
  (`enable_codebuild = true`); `deploy.sh` also auto-selects that path when no
  local Docker daemon is present. See [CodeBuild opt-in](#codebuild-opt-in).
- `kubectl` and `helm` for post-apply verification (and for `deploy.sh`'s
  on-cluster install steps).

## EKS API endpoint modes

`endpoint_access_mode` (default `"public-restricted"`) controls whether the
Kubernetes API server has any public path at all:

| Mode | Public endpoint | Private endpoint | CI/deploy actor | Human ops |
|------|-----------------|-------------------|------------------|-----------|
| `public-restricted` (default) | On, restricted to `cluster_endpoint_public_access_cidrs` | On | laptop `deploy.sh` (or CodeBuild) | laptop `kubectl` from an allowlisted IP |
| `private` | **Off** | On | CodeBuild-in-VPC (required — `enable_codebuild = true`) | SSM Session Manager port-forward (see [Human ops: SSM port-forward](#human-ops-ssm-port-forward)) |

**Breaking default change:** `cluster_endpoint_public_access_cidrs` now
defaults to `[]` (was `["0.0.0.0/0"]`) and **rejects `0.0.0.0/0`** outright —
you must supply your own narrow CIDR allowlist in `public-restricted` mode, or
switch to `endpoint_access_mode = "private"`. This is a fail-closed design
choice: an internet-open control plane was the top hardening gap in this
module. A `precondition` in `main.tf` also asserts
`public-restricted ⇒ len(cidrs) > 0` and `private ⇒ enable_codebuild`, so
either misconfiguration fails at `terraform plan`, not at apply time or later.

```bash
# public-restricted with your office/VPN CIDR
terraform apply -var=endpoint_access_mode=public-restricted \
  -var='cluster_endpoint_public_access_cidrs=["203.0.113.0/24"]'

# fully private (no public path — requires CodeBuild)
terraform apply -var=endpoint_access_mode=private -var=enable_codebuild=true
```

### Apply is pure-AWS

`terraform apply` never talks to the Kubernetes API — only the `aws` provider
is configured (no `kubernetes`/`helm` providers). The AWS Load Balancer
Controller and the AgentTier Helm chart are installed by `deploy.sh` **after**
apply, either from your laptop (`public-restricted`, endpoint reachable) or
inside a CodeBuild-in-VPC deploy run (`private`, delegated automatically by
`deploy.sh`). This means creating (or updating) a cluster works from a laptop
in either mode, with no "endpoint flip" step and no window where the endpoint
must be temporarily public just to apply.

### Cluster access

`enable_cluster_creator_admin_permissions` is `false`; instead the module
grants explicit, enumerated **EKS Access Entries**:

- `deployer` — the identity running `terraform apply`, normalized from an
  assumed-role session ARN to the underlying role ARN via
  `data.aws_iam_session_context` (an STS session ARN is not a valid access-entry
  principal). Always granted, so a private cluster is never left with no
  admin path in.
- `codebuild` — the CodeBuild deploy role, only when `enable_codebuild = true`.
  The single scoped principal used by CI; no long-lived human key ever touches
  the cluster in the CodeBuild path.

Both currently use the `AmazonEKSClusterAdminPolicy` cluster-scoped policy
association (v1 keeps this broad because the Load Balancer Controller install
and namespace creation need it); namespace-scoping the `codebuild` entry is a
known tightening opportunity for a future pass.

### Control-plane logging and GuardDuty

Control-plane logging (`api`, `audit`, `authenticator`) is always on, writing
to a terraform-owned CloudWatch log group
(`/aws/eks/<cluster_name>/cluster`, retention `eks_log_retention_days` = 14
days by default) so `terraform destroy` cleans it up instead of leaving an
EKS-auto-created group behind. **Cost flag:** audit logs are the expensive
tier on a busy cluster — raise or lower `eks_log_retention_days` to match your
budget and compliance needs.

GuardDuty EKS Protection (audit-log + runtime-monitoring datasources) is
opt-in via `enable_guardduty_eks_protection` (default `false`). It is off by
default because GuardDuty is frequently managed at the AWS Organizations
level via a delegated administrator account with a single org-wide detector —
enabling a second standalone detector here can conflict with that setup. Only
turn it on if this account has no org-managed GuardDuty detector.

### Human ops: SSM port-forward

Both node groups' instance roles carry `AmazonSSMManagedInstanceCore`, so the
worker nodes double as SSM Session Manager targets — no separate bastion, no
inbound security-group rule (SSM is outbound-initiated over the existing NAT
egress). This is the primary way to reach a `private`-mode cluster's API
server (and the AgentTier web UI) from outside the VPC:

> **Prerequisite:** these commands need the
> [`session-manager-plugin`](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)
> on your workstation — the AWS CLI does not bundle it (without it,
> `aws ssm start-session` fails with `SessionManagerPlugin is not found`).
> macOS: `brew install --cask session-manager-plugin`; Linux: see the AWS docs.
> Operator-only — not needed by `deploy.sh`, which reaches a private cluster via
> CodeBuild-in-VPC.

```bash
# 1. Find a running managed node instance
INSTANCE=$(aws ec2 describe-instances \
  --filters "Name=tag:eks:cluster-name,Values=$(terraform output -raw cluster_name)" \
            "Name=instance-state-name,Values=running" \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)

# 2. Resolve the API server host (terraform output, scheme stripped)
APISERVER=$(terraform output -raw cluster_endpoint_private_host)

# 3. Port-forward local :6443 -> apiserver:443 through the node via SSM
aws ssm start-session --target "$INSTANCE" \
  --document-name AWS-StartPortForwardingSessionToRemoteHost \
  --parameters "{\"host\":[\"$APISERVER\"],\"portNumber\":[\"443\"],\"localPortNumber\":[\"6443\"]}"

# 4. Point kubectl at the tunnel — the API server cert is issued for
#    $APISERVER, not "localhost", so set tls-server-name (or use
#    --insecure-skip-tls-verify for the tunnel only):
kubectl config set-cluster agenttier-tunnel --server=https://localhost:6443 \
  --tls-server-name="$APISERVER" \
  --certificate-authority=<(terraform output -raw cluster_certificate_authority_data | base64 -d)
```

Full runbook (including the web-UI tunnel variant): `docs/docs/port-forwarding.md`.
An EC2 Instance Connect Endpoint (EICE) is an accepted alternative if you'd
rather not put SSM on the worker nodes.

### MFA

The module does not mint any human-assumable IAM role, so there is nothing
here for terraform to attach an MFA condition to — human access relies on
your own IAM/SSO MFA policy. If you later add a convenience
"cluster-admin-assume" role, give its trust policy an
`aws:MultiFactorAuthPresent` condition.

## Usage

The recommended deploy path is `./deploy.sh --target=eks` from the repo root,
which calls `terraform apply`, reads ECR/cluster outputs, builds and pushes
images, and installs the Load Balancer Controller + AgentTier Helm chart in
one shot (locally in `public-restricted` mode, or via a CodeBuild-in-VPC
deploy run in `private` mode). See the top-level `README.md` for the full
deploy guide. Direct Terraform usage:

```bash
terraform init                       # -backend=false for a local-state-only eval
terraform plan
terraform apply      # ~15-20 minutes (control plane + nodes + ECR repos + add-ons)

# Point kubectl at the new cluster (public-restricted mode; private mode
# needs the SSM tunnel above first)
$(terraform output -raw kubeconfig_command)

# Build and push images to ECR (deploy.sh handles this automatically)
# Use the actual Terraform output values — the prefix follows cluster_name /
# ecr_repo_prefix and is NOT always "agenttier". Always derive from outputs:
ECR_REGISTRY=$(terraform output -raw ecr_registry)
ECR_CONTROLLER_URL=$(terraform output -raw ecr_controller_url)
IMAGE_TAG=$(git rev-parse --short HEAD)
aws ecr get-login-password --region us-east-1 \
  | docker login --username AWS --password-stdin "$ECR_REGISTRY"
docker buildx build --platform linux/amd64 \
  -t "${ECR_CONTROLLER_URL}:${IMAGE_TAG}" \
  -f docker/Dockerfile.controller --push .
# Similarly, use terraform output -raw ecr_router_url, ecr_webui_url,
# ecr_sandbox_*_url for the remaining images. deploy.sh does all of this
# automatically — the manual commands above are illustrative only.
#
# No local Docker? Apply with -var=enable_codebuild=true and let deploy.sh
# handle the source-zip upload to S3 and the CodeBuild run (it does this
# automatically when it can't find a local Docker daemon). Building directly
# against CodeBuild from raw terraform is not wired — use ./deploy.sh --target=eks.
# See "CodeBuild opt-in" below.

# Verify
kubectl get nodes -L agenttier.io/runtime
kubectl get pods -n agenttier          # after deploy.sh's Helm install step
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

Never use `latest`. The canonical tag is derived by `scripts/lib/version.sh`:
- Clean tree at a release tag → value from the `VERSION` file (e.g. `0.8.1`)
- Dev / dirty tree → `sha-<7-char-git-sha>[-dirty]`

`deploy.sh` computes the tag once, builds with it, pushes to ECR, and passes the
same value to Helm via `--set *.image.tag=<tag>`. The same value is also stamped
into the controller and router binaries as their reported version (via the
`VERSION` build-arg / ldflags) across every build path — local `docker build`,
local buildx, and CodeBuild — so a component's `/version` endpoint matches the
image tag it was deployed under. `deploy.sh` additionally stamps the short git
commit (`GIT_COMMIT`); the CodeBuild path leaves it `unknown` because its S3
source zip excludes `.git`.

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
CodeBuild IAM role has least-privilege permissions (ECR push to all nine repos
— scoped to exact ARNs, no wildcard — S3 read for source, CloudWatch Logs write).

### Authentication

This module always provisions a Cognito user pool + SPA app client;
`deploy.sh` wires the AgentTier Helm release to it by default (`--set
auth.devAuth=false` plus the `auth.oidc.*` values from the
`agenttier_helm_auth_values` output — see `deploy.sh` Step 6 /
`ci/buildspec-deploy.yml`, D8). Create a user in the pool, add them to the
`agenttier-admins` group, and log in through the web UI. There is no longer a
terraform-side toggle for this — the Helm values live entirely in
`deploy.sh`/`ci/buildspec-deploy.yml` now (see
[Breaking changes](#breaking-changes-in-this-hardening-pass)).

For a quick evaluation without OIDC, run `terraform apply` + `aws eks
update-kubeconfig` yourself, then install the chart directly with dev auth
instead of using `deploy.sh`:

```bash
helm upgrade --install agenttier ./helm/agenttier/ -n agenttier --create-namespace \
  --set auth.devAuth=true \
  --set controller.image.repository="$(terraform output -raw ecr_controller_url)" \
  # ... remaining --set flags mirror deploy.sh Step 6/7
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
| `endpoint_access_mode` | `"public-restricted"` | `"public-restricted"` or `"private"` — see [EKS API endpoint modes](#eks-api-endpoint-modes) |
| `cluster_endpoint_public_access_cidrs` | `[]` | CIDRs allowed to reach the K8s API in `public-restricted` mode. **`0.0.0.0/0` is rejected** — breaking default change, was `["0.0.0.0/0"]` |
| `node_instance_type` / `node_{min,desired,max}_size` | `t3.large` / `2`/`2`/`4` | Default node group |
| `gvisor_node_instance_type` / `gvisor_node_{min,desired,max}_size` | `t3.large` / `1`/`1`/`3` | gVisor node group |
| `gvisor_node_taint` | `false` | Taint gVisor nodes so only gVisor pods land there |
| `create_test_user` | `false` | Seed an admin user in Cognito |
| `ecr_repo_prefix` | `""` (uses `cluster_name`) | Override ECR repository name prefix |
| `enable_codebuild` | `false` | Enable the opt-in CodeBuild build path. **Required** when `endpoint_access_mode = "private"` |
| `codebuild_timeout_minutes` | `30` | Max CodeBuild run duration (5–480 min) |
| `eks_log_retention_days` | `14` | CloudWatch retention for the control-plane log group — see [Control-plane logging and GuardDuty](#control-plane-logging-and-guardduty) |
| `enable_guardduty_eks_protection` | `false` | Opt-in GuardDuty EKS Protection — see [Control-plane logging and GuardDuty](#control-plane-logging-and-guardduty) |

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
| `endpoint_access_mode` | Effective endpoint mode (`"public-restricted"` or `"private"`) |
| `cluster_endpoint_private_host` | Private DNS host of the API server (scheme stripped) — used by the [SSM port-forward runbook](#human-ops-ssm-port-forward) |
| `kubeconfig_command` | Run this to configure `kubectl` (public-restricted mode; private mode needs the SSM tunnel first) |
| `aws_load_balancer_controller_role_arn` | IRSA role ARN for `deploy.sh`'s LBC helm step |
| `cognito_issuer_url` | Cognito OIDC issuer URL |
| `agenttier_helm_auth_values` | Ready-to-use Helm values wiring auth to Cognito |

Run `terraform output` to see all outputs including cluster CA data (sensitive),
subnet IDs, security group IDs, IRSA role ARNs, and Cognito details.

## State backend

This module ships a committed `backend.tf` (S3 backend, `use_lockfile = true`
— a native S3 lock object, **no DynamoDB table**) — a deliberate departure from
its previous "no backend block, always local state" stance. A shared backend
is now required whenever the CodeBuild-in-VPC deploy actor and a human laptop
need to read/write the *same* state (i.e. whenever `endpoint_access_mode =
"private"`, and recommended even in `public-restricted` mode for team use).

```bash
# 1. Create a hardened bucket (versioned, SSE-KMS, Block Public Access, TLS-only
#    policy, tagged data-classification=confidential):
../../scripts/bootstrap-tfstate.sh agenttier-tfstate-<account-id> us-east-1

# 2. Point terraform at it (the script above prints the kms_key_id to use):
cp backend.hcl.example backend.hcl   # git-ignored — fill in your bucket name + kms_key_id
terraform init -backend-config=backend.hcl
```

`kms_key_id` in backend.hcl is what makes `terraform.tfstate` itself land
under SSE-KMS with the CMK the bootstrap script created — Terraform's S3
backend otherwise writes state with its own explicit SSE-S3 (AES256)
encryption regardless of the bucket's default encryption setting. Omitting
`kms_key_id` still encrypts state (AES256), just not under the CMK.

For a pure laptop eval with no shared state, `terraform init -backend=false`
still works (falls back to local state) — the module stays usable without ever
creating the bucket. Note the native lockfile requires **Terraform >= 1.10**
(see [Prerequisites](#prerequisites)).

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

- Use `endpoint_access_mode = "private"` if you can support the
  CodeBuild-in-VPC + SSM-port-forward workflow it requires; otherwise keep
  `public-restricted` with the **narrowest CIDR allowlist you can** — never
  reintroduce `0.0.0.0/0` (the module now rejects it outright).
- Set `single_nat_gateway = false` for per-AZ NAT redundancy.
- Pin `kubernetes_version` and `deploy.sh`'s `AGENTTIER_LBC_CHART_VERSION`
  (see `config/config.env.example`) and upgrade them deliberately.
- Namespace-scope the `codebuild` EKS Access Entry instead of cluster-admin
  once your LBC/namespace bootstrapping no longer needs broad rights (see
  [Cluster access](#cluster-access)) — v1 keeps it cluster-admin.
- Consider EKS secrets envelope-encryption (`cluster_encryption_config` with a
  customer-managed KMS key) — not enabled by this module in v1.
- Raise or lower `eks_log_retention_days` to match your compliance/cost needs;
  audit logs are the most expensive control-plane log type on a busy cluster.
- Review the AgentTier chart values for your governance, warm-pool, and
  observability needs — `deploy.sh` installs the chart with defaults plus
  Cognito OIDC auth.

## Breaking changes in this hardening pass

If you're upgrading from a pre-hardening version of this module:

- `cluster_endpoint_public_access_cidrs` now defaults to `[]` (was
  `["0.0.0.0/0"]`) and rejects `0.0.0.0/0` — you must supply a CIDR allowlist
  or switch to `endpoint_access_mode = "private"`.
- `enable_cluster_creator_admin_permissions` is gone; access is now via
  explicit EKS Access Entries (see [Cluster access](#cluster-access)). If you
  relied on blanket creator-admin for some other principal, add it as an
  access entry yourself.
- The module ships a `backend.tf` (S3 + native lockfile) — it is no longer a
  no-backend drop-in. Run `scripts/bootstrap-tfstate.sh` (or `terraform init
  -backend=false` for local-only state) — see [State backend](#state-backend).
- `install_agenttier`, `agenttier_chart_version`, `agenttier_oidc_auth`,
  `agenttier_extra_values`, and the `agenttier_installed` output are **removed**.
  The AgentTier Helm release is no longer installable from terraform at all —
  use `./deploy.sh --target=eks` (the canonical path already, per D1/D20).
- The `helm_release.aws_load_balancer_controller` resource is removed, and so
  are its former toggle/version variables — the module no longer declares any
  LBC install-toggle or chart-version variable. Use `deploy.sh`'s
  `AGENTTIER_INSTALL_LBC` / `AGENTTIER_LBC_CHART_VERSION` env vars instead
  (see `config/config.env.example`); the IRSA role the controller assumes is
  still created by this module (`aws_load_balancer_controller_role_arn`
  output).
- Terraform's `required_version` bumped to `>= 1.10.0` (was `>= 1.5.0`) —
  required by the S3 backend's native lockfile.

## Teardown

```bash
# If you installed AgentTier via deploy.sh and want it gone first:
helm uninstall agenttier -n agenttier
helm uninstall aws-load-balancer-controller -n kube-system   # if installed

terraform destroy
```

`./deploy.sh --target=eks --teardown` automates the Helm uninstall + `terraform
destroy` sequence (including LoadBalancer Service/PVC cleanup) — see the
top-level `README.md`.

If `destroy` stalls on the VPC, it is usually because the AWS Load Balancer
Controller still has an ALB/NLB (and its ENIs) attached — delete the AgentTier
Ingress/Services first so the load balancers are cleaned up, then re-run
`terraform destroy`.
