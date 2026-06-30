# AgentTier EKS reference module.
#
# Provisions a production-shaped EKS cluster sized for AgentTier:
#   - a VPC with public + private subnets across multiple AZs and a NAT gateway
#   - an EKS cluster (Kubernetes 1.30 by default) with IRSA/OIDC enabled
#   - two managed node groups: a general "default" group and a dedicated
#     gVisor group labelled so AgentTier's gVisor RuntimeClass schedules onto it
#   - the EBS CSI driver as an EKS add-on (with its own IRSA role)
#   - the AWS Load Balancer Controller (see load_balancer_controller.tf)
#   - an optional AgentTier Helm release (see agenttier.tf)
#   - a Cognito user pool for OIDC auth (see cognito.tf)
#
# This is a reference module. Read the README before applying; it creates
# billable resources (EKS control plane, EC2 nodes, NAT gateway, EBS).

data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_caller_identity" "current" {}

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

  cluster_endpoint_public_access       = true
  cluster_endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs

  # Grant the identity running `terraform apply` cluster-admin via an EKS
  # access entry, so kubectl works immediately after apply.
  enable_cluster_creator_admin_permissions = true

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
    }
  }

  tags = local.tags
}
