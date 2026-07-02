# Provider configuration.
#
# Only the aws provider remains. The helm provider is gone: the AWS Load
# Balancer Controller and AgentTier Helm releases that used to be applied
# here now run from deploy.sh (locally in public-restricted mode, or inside
# CodeBuild-in-VPC in private mode) — see terraform/aws-eks/README.md. That
# removes the `aws eks get-token`-during-apply reachability dependency that
# used to couple `terraform apply` to a reachable cluster endpoint, so
# `terraform apply` is now pure-AWS and also works against a fully private
# cluster from a laptop.
#
# The kubernetes provider is also gone (D-A3, verify-don't-assume gate /
# Group 2 / T7): the eks module (terraform-aws-modules/eks/aws v20.x) uses
# EKS Access Entries for cluster auth (main.tf) rather than the aws-auth
# ConfigMap sub-module, and no root-module resource here is a
# `kubernetes_*` type — `terraform init -backend=false && terraform
# validate` confirmed nothing in the resolved module graph requires it.

provider "aws" {
  region = var.region
}
