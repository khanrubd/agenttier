# =============================================================================
# Cluster
# =============================================================================

output "region" {
  description = "AWS region the cluster is deployed in."
  value       = var.region
}

output "cluster_name" {
  description = "Name of the EKS cluster."
  value       = module.eks.cluster_name
}

output "cluster_endpoint" {
  description = "Endpoint for the EKS Kubernetes API server."
  value       = module.eks.cluster_endpoint
}

output "cluster_oidc_issuer_url" {
  description = "The OIDC issuer URL of the cluster, used for IRSA."
  value       = module.eks.cluster_oidc_issuer_url
}

output "oidc_provider_arn" {
  description = "ARN of the IAM OIDC provider backing IRSA."
  value       = module.eks.oidc_provider_arn
}

output "cluster_certificate_authority_data" {
  description = "Base64-encoded certificate authority data for the cluster."
  value       = module.eks.cluster_certificate_authority_data
  sensitive   = true
}

output "cluster_security_group_id" {
  description = "Security group ID attached to the EKS control plane."
  value       = module.eks.cluster_security_group_id
}

output "node_security_group_id" {
  description = "Security group ID attached to the managed node groups."
  value       = module.eks.node_security_group_id
}

output "kubeconfig_command" {
  description = "Run this to configure kubectl for the cluster."
  value       = "aws eks update-kubeconfig --region ${var.region} --name ${module.eks.cluster_name}"
}

output "account_id" {
  description = "AWS account ID the cluster was created in."
  value       = data.aws_caller_identity.current.account_id
}

# =============================================================================
# Networking
# =============================================================================

output "vpc_id" {
  description = "ID of the VPC created for the cluster."
  value       = module.vpc.vpc_id
}

output "private_subnet_ids" {
  description = "IDs of the private subnets (where nodes run)."
  value       = module.vpc.private_subnets
}

output "public_subnet_ids" {
  description = "IDs of the public subnets (where load balancers run)."
  value       = module.vpc.public_subnets
}

# =============================================================================
# IRSA roles
# =============================================================================

output "aws_load_balancer_controller_role_arn" {
  description = "IAM role ARN assumed by the AWS Load Balancer Controller service account."
  value       = module.lb_controller_irsa.iam_role_arn
}

output "ebs_csi_driver_role_arn" {
  description = "IAM role ARN assumed by the EBS CSI driver service account."
  value       = module.ebs_csi_irsa.iam_role_arn
}

# =============================================================================
# AgentTier
# =============================================================================

output "agenttier_installed" {
  description = "Whether the AgentTier Helm release was installed by this module."
  value       = var.install_agenttier
}

# =============================================================================
# Cognito
# =============================================================================

output "cognito_user_pool_id" {
  description = "ID of the Cognito user pool."
  value       = aws_cognito_user_pool.agenttier.id
}

output "cognito_client_id" {
  description = "ID of the Cognito app client (AgentTier web UI)."
  value       = aws_cognito_user_pool_client.agenttier.id
}

output "cognito_issuer_url" {
  description = "OIDC issuer URL of the Cognito user pool (for auth.oidc.issuerUrl)."
  value       = local.cognito_issuer_url
}

output "cognito_domain" {
  description = "Cognito hosted-UI domain URL."
  value       = local.cognito_domain_url
}

output "cognito_admin_group" {
  description = "Cognito group whose members get AgentTier admin access."
  value       = aws_cognito_user_group.admins.name
}

output "agenttier_helm_auth_values" {
  description = "Helm values snippet wiring AgentTier auth to this Cognito user pool."
  value       = <<-EOT
    auth:
      oidc:
        issuerUrl: "${local.cognito_issuer_url}"
        clientId: "${aws_cognito_user_pool_client.agenttier.id}"
        adminGroup: "${aws_cognito_user_group.admins.name}"
        groupClaim: "cognito:groups"
  EOT
}
