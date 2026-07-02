# EKS control-plane logging (api, audit, authenticator).
#
# The eks module is configured with create_cloudwatch_log_group = false
# (main.tf) so this resource is the sole owner of the log group. That avoids
# the create-race that would otherwise happen if EKS auto-creates the group
# on first log publish before terraform does (same teardown-cleanup
# discipline as the CodeBuild log group, commit a518ff7): module "eks" in
# main.tf must depend_on this resource so the group exists before logging is
# enabled.

resource "aws_cloudwatch_log_group" "eks_cluster" {
  name              = "/aws/eks/${var.cluster_name}/cluster"
  retention_in_days = var.eks_log_retention_days

  tags = merge(local.tags, {
    service               = "agenttier-eks"
    "data-classification" = "internal"
  })
}
