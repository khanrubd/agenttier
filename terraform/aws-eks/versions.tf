# Terraform and provider version constraints for the AgentTier EKS reference module.
#
# These constraints are intentionally aligned with the checked-in
# .terraform.lock.hcl so `terraform init` resolves without re-locking.
# The official VPC/EKS modules pull in the tls, cloudinit, time and null
# providers transitively; they are recorded in the lock file and do not need
# to be declared here because this module does not configure them directly.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.14"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.30"
    }
  }
}
