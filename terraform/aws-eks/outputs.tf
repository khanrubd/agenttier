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

output "endpoint_access_mode" {
  description = "Effective EKS API endpoint exposure ('public-restricted' or 'private'). See var.endpoint_access_mode."
  value       = var.endpoint_access_mode
}

output "cluster_endpoint_private_host" {
  description = "Private DNS host of the EKS API server (cluster_endpoint with the scheme stripped), for the SSM Session Manager port-forward runbook (docs/docs/port-forwarding.md). Resolves in-VPC because the VPC has enable_dns_hostnames/support = true."
  value       = replace(module.eks.cluster_endpoint, "https://", "")
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

# =============================================================================
# ECR
# =============================================================================

output "ecr_registry" {
  description = "ECR registry hostname (<account>.dkr.ecr.<region>.amazonaws.com). Pass to deploy.sh as REGISTRY or --set *.image.registry."
  value       = local.ecr_registry
}

output "ecr_controller_url" {
  description = "Full ECR repository URL for the controller image."
  value       = aws_ecr_repository.controller.repository_url
}

output "ecr_router_url" {
  description = "Full ECR repository URL for the router image."
  value       = aws_ecr_repository.router.repository_url
}

output "ecr_webui_url" {
  description = "Full ECR repository URL for the web-ui image."
  value       = aws_ecr_repository.webui.repository_url
}

output "ecr_sandbox_general_url" {
  description = "Full ECR repository URL for the sandbox-general image."
  value       = aws_ecr_repository.sandbox_general.repository_url
}

output "ecr_sandbox_claude_code_url" {
  description = "Full ECR repository URL for the sandbox-claude-code image."
  value       = aws_ecr_repository.sandbox_claude_code.repository_url
}

output "ecr_sandbox_openclaw_url" {
  description = "Full ECR repository URL for the sandbox-openclaw image."
  value       = aws_ecr_repository.sandbox_openclaw.repository_url
}

output "ecr_sandbox_langgraph_url" {
  description = "Full ECR repository URL for the sandbox-langgraph image."
  value       = aws_ecr_repository.sandbox_langgraph.repository_url
}

output "ecr_sandbox_rl_url" {
  description = "Full ECR repository URL for the sandbox-rl image."
  value       = aws_ecr_repository.sandbox_rl.repository_url
}

output "ecr_sandbox_strands_bedrock_url" {
  description = "Full ECR repository URL for the sandbox-strands-bedrock image."
  value       = aws_ecr_repository.sandbox_strands_bedrock.repository_url
}

# =============================================================================
# CodeBuild (only populated when enable_codebuild = true)
# =============================================================================

output "codebuild_project" {
  description = "CodeBuild project name. Empty string when enable_codebuild = false."
  value       = var.enable_codebuild ? aws_codebuild_project.agenttier[0].name : ""
}

output "codebuild_s3_bucket" {
  description = "S3 bucket name for CodeBuild source artifacts. Empty string when enable_codebuild = false."
  value       = var.enable_codebuild ? aws_s3_bucket.codebuild_source[0].bucket : ""
}

output "codebuild_timeout_minutes" {
  description = "Maximum wall-clock minutes per CodeBuild run (used by deploy.sh polling loop)."
  value       = var.codebuild_timeout_minutes
}

# =============================================================================
# Cognito
# =============================================================================

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
