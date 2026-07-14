# Terraform and provider version constraints for the AgentTier EKS reference module.
#
# These constraints are intentionally aligned with the checked-in
# .terraform.lock.hcl so `terraform init` resolves without re-locking.
# The official VPC/EKS modules pull in the tls, cloudinit, time and null
# providers transitively; they are recorded in the lock file and do not need
# to be declared here because this module does not configure them directly.
#
# The helm AND kubernetes providers are both removed: the AWS Load Balancer
# Controller and AgentTier Helm releases relocated to deploy.sh (D-U3/D-A1),
# so `terraform apply` no longer talks to Kubernetes at all — it is pure-AWS
# (D-A3, confirmed by `terraform init -backend=false && terraform validate`
# with no kubernetes provider declared: the eks module uses EKS Access
# Entries, not the aws-auth ConfigMap sub-module that would otherwise need
# it). required_version is bumped to >= 1.10.0 because the S3 backend
# (backend.tf) uses the native S3 lockfile (`use_lockfile = true`), which
# requires Terraform >= 1.10 (D-A10). Install an isolated terraform >= 1.10
# for validation/apply — do not rely on an older system-wide terraform.

terraform {
  required_version = ">= 1.10.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}
