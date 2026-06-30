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

variable "cluster_endpoint_public_access_cidrs" {
  description = "CIDR blocks allowed to reach the public Kubernetes API endpoint. Restrict this for production clusters."
  type        = list(string)
  default     = ["0.0.0.0/0"]
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
# =============================================================================

variable "install_aws_load_balancer_controller" {
  description = "Install the AWS Load Balancer Controller via Helm (required for ALB-backed Ingress)."
  type        = bool
  default     = true
}

variable "aws_load_balancer_controller_chart_version" {
  description = "Chart version for the aws-load-balancer-controller Helm release."
  type        = string
  default     = "1.8.1"
}

# =============================================================================
# AgentTier Helm release
# =============================================================================

variable "install_agenttier" {
  description = "Install the AgentTier Helm chart from the published chart repo."
  type        = bool
  default     = true
}

variable "agenttier_chart_version" {
  description = "AgentTier chart version to install. Empty string installs the latest published version."
  type        = string
  default     = ""
}

variable "agenttier_oidc_auth" {
  description = "Wire the AgentTier release's OIDC auth to the Cognito user pool created by this module. Set false to manage auth yourself (e.g. devAuth) via agenttier_extra_values."
  type        = bool
  default     = true
}

variable "agenttier_extra_values" {
  description = "Additional Helm values (raw YAML strings) merged into the AgentTier release. Later entries override earlier ones."
  type        = list(string)
  default     = []
}

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
