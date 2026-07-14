# =============================================================================
# General
# =============================================================================

variable "region" {
  description = "AWS region for all resources."
  type        = string
  default     = "us-east-1"
}

variable "cluster_name" {
  description = "Name of the EKS cluster. Also used as a prefix for related resources (VPC, IAM roles, Cognito pool)."
  type        = string
  default     = "agenttier"
}

variable "kubernetes_version" {
  description = "Kubernetes control-plane version for the EKS cluster."
  type        = string
  default     = "1.30"
}

variable "tags" {
  description = "Additional tags applied to all resources that support tagging."
  type        = map(string)
  default     = {}
}

# =============================================================================
# Networking
# =============================================================================

variable "vpc_cidr" {
  description = "CIDR block for the VPC. Subnets are carved as /20 ranges out of this block."
  type        = string
  default     = "10.0.0.0/16"
}

variable "az_count" {
  description = "Number of availability zones to spread subnets across (2-3 recommended)."
  type        = number
  default     = 3

  validation {
    condition     = var.az_count >= 2 && var.az_count <= 3
    error_message = "az_count must be 2 or 3."
  }
}

variable "single_nat_gateway" {
  description = "Use a single shared NAT gateway (cheaper) instead of one per AZ. Set to false for production HA egress."
  type        = bool
  default     = true
}

variable "endpoint_access_mode" {
  description = "EKS API endpoint exposure. 'public-restricted' (default): public endpoint on with a narrow CIDR allowlist + private access on — laptop-friendly. 'private': public endpoint OFF, private access ON — requires CodeBuild-in-VPC for CI and SSM port-forward for humans."
  type        = string
  default     = "public-restricted"

  validation {
    condition     = contains(["public-restricted", "private"], var.endpoint_access_mode)
    error_message = "endpoint_access_mode must be 'public-restricted' or 'private'."
  }
}

# Fail closed: public-restricted must NOT allow the whole internet.
variable "cluster_endpoint_public_access_cidrs" {
  description = "CIDR blocks allowed to reach the public Kubernetes API endpoint in public-restricted mode. Must NOT include 0.0.0.0/0 — supply a narrow allowlist, or use endpoint_access_mode=private."
  type        = list(string)
  default     = [] # was ["0.0.0.0/0"] — BREAKING default change; user must supply their CIDR

  validation {
    condition     = !contains(var.cluster_endpoint_public_access_cidrs, "0.0.0.0/0")
    error_message = "0.0.0.0/0 is not allowed. Supply a narrow CIDR allowlist, or use endpoint_access_mode=private."
  }
}

# =============================================================================
# Default (general-purpose) node group
# =============================================================================

variable "node_instance_type" {
  description = "EC2 instance type for the default general-purpose node group."
  type        = string
  default     = "t3.large"
}

variable "node_min_size" {
  description = "Minimum number of nodes in the default node group."
  type        = number
  default     = 2
}

variable "node_desired_size" {
  description = "Desired number of nodes in the default node group."
  type        = number
  default     = 2
}

variable "node_max_size" {
  description = "Maximum number of nodes in the default node group."
  type        = number
  default     = 4
}

# =============================================================================
# gVisor node group
# =============================================================================

variable "gvisor_node_instance_type" {
  description = "EC2 instance type for the dedicated gVisor node group."
  type        = string
  default     = "t3.large"
}

variable "gvisor_node_min_size" {
  description = "Minimum number of nodes in the gVisor node group."
  type        = number
  default     = 1
}

variable "gvisor_node_desired_size" {
  description = "Desired number of nodes in the gVisor node group."
  type        = number
  default     = 1
}

variable "gvisor_node_max_size" {
  description = "Maximum number of nodes in the gVisor node group."
  type        = number
  default     = 3
}

variable "gvisor_node_taint" {
  description = "If true, taint the gVisor nodes (agenttier.io/runtime=gvisor:NoSchedule) so only gVisor-targeted pods schedule onto them."
  type        = bool
  default     = false
}

# =============================================================================
# Add-ons
#
# The AWS Load Balancer Controller is installed by deploy.sh (Shape A /
# D-U3/D-A1 — terraform apply is pure-AWS; on-cluster helm installs happen
# outside terraform). Its install-toggle and chart-version live as deploy.sh
# env vars (AGENTTIER_INSTALL_LBC, AGENTTIER_LBC_CHART_VERSION — see
# config/config.env.example), not as terraform variables: the two
# corresponding terraform vars this module used to declare had no remaining
# reader anywhere in the module (a tflint terraform_unused_declarations
# finding) and have been removed. The IRSA role the controller's
# ServiceAccount assumes is still owned by this module (module.lb_controller_
# irsa in irsa.tf, aws_load_balancer_controller_role_arn output) — only the
# Helm install itself moved to deploy.sh.
# =============================================================================

# =============================================================================
# Cognito (OIDC identity provider)
# =============================================================================

variable "cognito_domain_prefix" {
  description = "Cognito hosted-UI domain prefix. Must be globally unique per region. Empty uses \"<cluster_name>-auth\"."
  type        = string
  default     = ""
}

variable "agenttier_url" {
  description = "Public URL of the AgentTier web UI, added to the Cognito client's callback/logout URLs once known."
  type        = string
  default     = ""
}

variable "create_test_user" {
  description = "Create a seed admin user in the Cognito pool (handy for evaluation clusters)."
  type        = bool
  default     = false
}

variable "test_user_email" {
  description = "Email/username for the optional seed admin user."
  type        = string
  default     = "admin@example.com"
}

variable "test_user_password" {
  description = "Temporary password for the optional seed admin user. Required only when create_test_user = true."
  type        = string
  default     = ""
  sensitive   = true
}

# =============================================================================
# ECR
# =============================================================================

variable "ecr_repo_prefix" {
  description = "Name prefix for ECR repositories. Defaults to cluster_name. Override to share a registry namespace across clusters."
  type        = string
  default     = ""
}

# =============================================================================
# CodeBuild (opt-in — off by default per decision D6)
# =============================================================================

variable "enable_codebuild" {
  description = "Enable the optional CodeBuild path for building and pushing images. Off by default — the primary build path is local Docker buildx pushed to ECR. Enable only when a local Docker daemon is unavailable."
  type        = bool
  default     = false
}

variable "codebuild_timeout_minutes" {
  description = "Maximum wall-clock minutes for a single CodeBuild run. The deploy.sh CodeBuild polling loop respects this limit and exits non-zero if exceeded (fixes audit M6)."
  type        = number
  default     = 30

  validation {
    condition     = var.codebuild_timeout_minutes >= 5 && var.codebuild_timeout_minutes <= 480
    error_message = "codebuild_timeout_minutes must be between 5 and 480."
  }
}

# =============================================================================
# EKS hardening (control-plane logging, GuardDuty)
# =============================================================================

variable "eks_log_retention_days" {
  description = "CloudWatch Logs retention (days) for the EKS control-plane log group (/aws/eks/<cluster_name>/cluster). Bounds audit-log cost."
  type        = number
  default     = 14
}

variable "enable_guardduty_eks_protection" {
  description = "Enable GuardDuty EKS Protection (audit-log + runtime monitoring datasources). Off by default — GuardDuty is frequently managed at the AWS Organizations level, and enabling a second detector here can conflict with an org-wide detector."
  type        = bool
  default     = false
}
