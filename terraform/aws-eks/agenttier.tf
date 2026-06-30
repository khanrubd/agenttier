# AgentTier Helm release.
#
# Installs the published AgentTier chart from the project's GitHub Pages Helm
# repo. Toggle with `install_agenttier` (default true) so the module can also
# be used to stand up just the cluster + add-ons and install AgentTier by hand.
#
# When agenttier_oidc_auth is true (default), the release is wired to the
# Cognito user pool created by this module (see cognito.tf). For a quick
# evaluation without configuring OIDC you can set agenttier_oidc_auth = false
# and pass `auth: { devAuth: true }` via agenttier_extra_values.

locals {
  # OIDC auth values rendered as a YAML fragment merged into the release.
  agenttier_oidc_values = var.agenttier_oidc_auth ? yamlencode({
    auth = {
      oidc = {
        issuerUrl  = local.cognito_issuer_url
        clientId   = aws_cognito_user_pool_client.agenttier.id
        adminGroup = aws_cognito_user_group.admins.name
        groupClaim = "cognito:groups"
      }
    }
  }) : ""
}

resource "helm_release" "agenttier" {
  count = var.install_agenttier ? 1 : 0

  name       = "agenttier"
  repository = "https://agenttier.github.io/agenttier/charts"
  chart      = "agenttier"
  # Empty string => install the latest published chart version.
  version = var.agenttier_chart_version != "" ? var.agenttier_chart_version : null

  namespace        = "agenttier"
  create_namespace = true

  # OIDC auth fragment first, then any user-supplied overrides (later values
  # win). compact() drops the OIDC fragment when agenttier_oidc_auth = false.
  values = compact(concat([local.agenttier_oidc_values], var.agenttier_extra_values))

  # AgentTier needs storage (EBS CSI) for sandbox PVCs and, for the default
  # ALB-backed ingress, the load balancer controller.
  depends_on = [
    aws_eks_addon.ebs_csi,
    helm_release.aws_load_balancer_controller,
  ]
}
