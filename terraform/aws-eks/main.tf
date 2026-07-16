# AgentTier EKS reference module.
#
# Provisions a production-shaped EKS cluster sized for AgentTier:
#   - a VPC with public + private subnets across multiple AZs and a NAT gateway
#   - an EKS cluster (Kubernetes 1.30 by default) with IRSA/OIDC enabled
#   - two managed node groups: a general "default" group and a dedicated
#     gVisor group labelled so AgentTier's gVisor RuntimeClass schedules onto it
#   - the EBS CSI driver as an EKS add-on (with its own IRSA role)
#   - an IRSA role for the AWS Load Balancer Controller (module.lb_controller_irsa
#     below) — the controller's Helm install itself is owned by deploy.sh, not
#     terraform (D-U3/D-A1: this module is pure-AWS, no helm/kubernetes provider)
#   - a Cognito user pool for OIDC auth (see cognito.tf)
#
# This is a reference module. Read the README before applying; it creates
# billable resources (EKS control plane, EC2 nodes, NAT gateway, EBS).

data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_caller_identity" "current" {}

# Normalizes the calling identity to a principal ARN that EKS access entries
# accept. When terraform is run via an assumed role (the common case — SSO,
# CI, `aws sts assume-role`), data.aws_caller_identity.current.arn is an STS
# *session* ARN (arn:aws:sts::<acct>:assumed-role/<role>/<session>), which is
# NOT a valid access-entry principal. aws_iam_session_context resolves that
# back to the underlying role/user ARN (issuer_arn) that EKS expects.
data "aws_iam_session_context" "current" {
  arn = data.aws_caller_identity.current.arn
}

locals {
  # Pick the first N available AZs for the cluster.
  azs = slice(data.aws_availability_zones.available.names, 0, var.az_count)

  tags = merge(
    {
      Project   = "agenttier"
      ManagedBy = "terraform"
      Module    = "aws-eks"
    },
    var.tags,
  )

  # ECR registry hostname — <account>.dkr.ecr.<region>.amazonaws.com.
  # Consumed by deploy.sh and the optional CodeBuild project.
  ecr_registry = "${data.aws_caller_identity.current.account_id}.dkr.ecr.${var.region}.amazonaws.com"

  # ECR repository name prefix. When var.ecr_repo_prefix is non-empty it is
  # used as-is; otherwise falls back to cluster_name so repos are namespaced
  # by cluster (e.g. "agenttier-prod/controller"). Override to share a registry
  # namespace across multiple clusters (D2).
  ecr_prefix = var.ecr_repo_prefix != "" ? var.ecr_repo_prefix : var.cluster_name

  # =============================================================================
  # EKS API endpoint access mode (FR-1/FR-2, design.md#3)
  # =============================================================================

  # public-restricted (default): public endpoint on with a narrow CIDR
  # allowlist. private: public endpoint off entirely. Private access is
  # always on so in-VPC principals (nodes, CodeBuild, SSM-tunneled kubectl)
  # can always reach the API regardless of mode.
  endpoint_public  = var.endpoint_access_mode == "public-restricted"
  endpoint_private = true

  # =============================================================================
  # Scoped EKS access entries (FR-8, design.md#5)
  #
  # Replaces enable_cluster_creator_admin_permissions=true (blanket creator
  # admin) with an explicit, enumerated map: the normalized identity running
  # `terraform apply` always gets an entry first (so a private cluster is
  # never orphaned — risk #3), and the CodeBuild deploy principal gets one
  # too, but only when it exists (enable_codebuild=true).
  # =============================================================================

  deployer_principal_arn = data.aws_iam_session_context.current.issuer_arn

  cluster_admin_policy_association = {
    admin = {
      policy_arn   = "arn:aws:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
      access_scope = { type = "cluster" }
    }
  }

  access_entries = merge(
    {
      deployer = {
        principal_arn       = local.deployer_principal_arn
        kubernetes_groups   = []
        policy_associations = local.cluster_admin_policy_association
      }
    },
    var.enable_codebuild ? {
      codebuild = {
        principal_arn       = aws_iam_role.codebuild[0].arn
        kubernetes_groups   = []
        policy_associations = local.cluster_admin_policy_association
      }
    } : {}
  )
}

# =============================================================================
# VPC
# =============================================================================

module "vpc" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 5.0"

  name = "${var.cluster_name}-vpc"
  cidr = var.vpc_cidr

  azs = local.azs
  # Carve evenly sized /20 subnets out of the VPC CIDR. Public subnets are
  # offset so they never overlap the private ranges.
  private_subnets = [for i, az in local.azs : cidrsubnet(var.vpc_cidr, 4, i)]
  public_subnets  = [for i, az in local.azs : cidrsubnet(var.vpc_cidr, 4, i + 8)]

  enable_nat_gateway   = true
  single_nat_gateway   = var.single_nat_gateway
  enable_dns_hostnames = true
  enable_dns_support   = true

  # Tags the AWS Load Balancer Controller and Kubernetes use for subnet
  # auto-discovery when provisioning internet-facing / internal load balancers.
  public_subnet_tags = {
    "kubernetes.io/role/elb"                    = "1"
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
  }
  private_subnet_tags = {
    "kubernetes.io/role/internal-elb"           = "1"
    "kubernetes.io/cluster/${var.cluster_name}" = "shared"
  }

  tags = local.tags
}

# =============================================================================
# EKS cluster + managed node groups
# =============================================================================

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.0"

  cluster_name    = var.cluster_name
  cluster_version = var.kubernetes_version

  # IRSA / OIDC provider. Required for the EBS CSI and Load Balancer Controller
  # IRSA roles (see irsa.tf) and for AgentTier per-sandbox cloud identities.
  enable_irsa = true

  cluster_endpoint_public_access       = local.endpoint_public
  cluster_endpoint_private_access      = local.endpoint_private
  cluster_endpoint_public_access_cidrs = local.endpoint_public ? var.cluster_endpoint_public_access_cidrs : []

  # Control-plane logging (FR-9, design.md#6). The log group is owned by
  # logging.tf (create_cloudwatch_log_group=false here) so that
  # `terraform destroy` cleans it up instead of leaving an EKS-auto-created
  # group behind — same discipline as the CodeBuild log group (commit
  # a518ff7). depends_on ensures the group exists before the cluster enables
  # logging against it, avoiding a create-race.
  cluster_enabled_log_types              = ["api", "audit", "authenticator"]
  create_cloudwatch_log_group            = false
  cloudwatch_log_group_retention_in_days = var.eks_log_retention_days

  # Scoped EKS access entries (FR-8, design.md#5) replace blanket creator-admin.
  enable_cluster_creator_admin_permissions = false
  access_entries                           = local.access_entries

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnets

  # Core add-ons. The EBS CSI driver is managed separately (irsa.tf) because it
  # needs an IRSA role whose trust policy depends on this module's OIDC output.
  cluster_addons = {
    coredns    = {}
    kube-proxy = {}
    vpc-cni    = {}
  }

  eks_managed_node_groups = {
    # General-purpose node group for AgentTier's control-plane components
    # (controller, router, web-ui) and standard sandboxes.
    default = {
      instance_types = [var.node_instance_type]
      capacity_type  = "ON_DEMAND"

      min_size     = var.node_min_size
      max_size     = var.node_max_size
      desired_size = var.node_desired_size

      labels = {
        "agenttier.io/nodegroup" = "default"
      }

      # Human-ops SSM Session Manager port-forward path (FR-12, design.md#9):
      # reusing worker nodes as SSM targets (no separate bastion) needs the
      # SSM agent (present on EKS-optimized AL2/AL2023 AMIs) to be able to
      # register with the SSM service, which requires this managed policy on
      # the node instance role. No inbound SG rule needed — SSM is
      # outbound-initiated via NAT.
      iam_role_additional_policies = {
        ssm = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
      }
    }

    # Dedicated node group for gVisor-isolated sandboxes. AgentTier's gVisor
    # RuntimeClass uses a nodeSelector of `agenttier.io/runtime=gvisor`, so
    # pods that request the gVisor runtime land here. Set var.gvisor_node_taint
    # to also taint these nodes so only gVisor workloads schedule onto them.
    #
    # NOTE: this group only provides correctly *labelled* nodes. The gVisor
    # runtime (runsc) + RuntimeClass are installed separately (the AgentTier
    # chart's gVisor add-on or a runsc installer DaemonSet) — see the README.
    gvisor = {
      instance_types = [var.gvisor_node_instance_type]
      capacity_type  = "ON_DEMAND"

      min_size     = var.gvisor_node_min_size
      max_size     = var.gvisor_node_max_size
      desired_size = var.gvisor_node_desired_size

      labels = {
        "agenttier.io/runtime" = "gvisor"
      }

      taints = var.gvisor_node_taint ? {
        gvisor = {
          key    = "agenttier.io/runtime"
          value  = "gvisor"
          effect = "NO_SCHEDULE"
        }
      } : {}

      # Same SSM rationale as the default node group above.
      iam_role_additional_policies = {
        ssm = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
      }
    }
  }

  tags = local.tags

  depends_on = [aws_cloudwatch_log_group.eks_cluster]
}

# =============================================================================
# Fail-closed preconditions (FR-1/FR-8, design.md#3/#5)
# =============================================================================

# public-restricted mode with an empty CIDR allowlist would leave the public
# endpoint enabled but unreachable by anyone — belt-and-suspenders alongside
# the variable-level 0.0.0.0/0 rejection in variables.tf.
resource "terraform_data" "endpoint_mode_preconditions" {
  lifecycle {
    precondition {
      condition     = var.endpoint_access_mode != "public-restricted" || length(var.cluster_endpoint_public_access_cidrs) > 0
      error_message = "endpoint_access_mode=public-restricted requires at least one entry in cluster_endpoint_public_access_cidrs (0.0.0.0/0 is rejected — supply a narrow allowlist, or use endpoint_access_mode=private)."
    }

    # private mode has no public path to the API; only CodeBuild-in-VPC can
    # run the on-cluster deploy steps (design.md#4). Without enable_codebuild
    # a private cluster would have no deploy path at all.
    precondition {
      condition     = var.endpoint_access_mode != "private" || var.enable_codebuild
      error_message = "endpoint_access_mode=private requires enable_codebuild=true (CodeBuild-in-VPC is the only deploy path when the public endpoint is off)."
    }
  }
}
