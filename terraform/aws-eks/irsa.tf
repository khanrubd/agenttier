# IRSA (IAM Roles for Service Accounts) roles for cluster add-ons.
#
# Both roles trust the cluster's OIDC provider (created by the eks module when
# enable_irsa = true) and are scoped to a single Kubernetes service account.
# They use the official iam-role-for-service-accounts-eks submodule, which
# attaches the correct AWS-maintained managed policy for each add-on.

# -----------------------------------------------------------------------------
# EBS CSI driver
# -----------------------------------------------------------------------------

module "ebs_csi_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name             = "${var.cluster_name}-ebs-csi"
  attach_ebs_csi_policy = true

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["kube-system:ebs-csi-controller-sa"]
    }
  }

  tags = local.tags
}

# The EBS CSI driver ships as an EKS managed add-on. It is declared here rather
# than inside the eks module's `cluster_addons` to avoid a dependency cycle:
# the add-on needs the IRSA role ARN, and the IRSA role needs the cluster's
# OIDC provider ARN that the eks module produces.
resource "aws_eks_addon" "ebs_csi" {
  cluster_name = module.eks.cluster_name
  addon_name   = "aws-ebs-csi-driver"

  service_account_role_arn    = module.ebs_csi_irsa.iam_role_arn
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  tags = local.tags

  # Wait for at least one node group so the driver's pods can schedule.
  depends_on = [module.eks]
}

# -----------------------------------------------------------------------------
# AWS Load Balancer Controller
# -----------------------------------------------------------------------------

module "lb_controller_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.0"

  role_name                              = "${var.cluster_name}-aws-lbc"
  attach_load_balancer_controller_policy = true

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["kube-system:aws-load-balancer-controller"]
    }
  }

  tags = local.tags
}
