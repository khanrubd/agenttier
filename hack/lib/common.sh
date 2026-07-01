#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# hack/lib/common.sh — shared logging, prereq checks, and env helpers.
#
# Source this file; do not execute directly.
#   source hack/lib/common.sh
set -euo pipefail

# --- Logging helpers ---

# at::log <msg>  — info level
at::log() {
  echo "[agenttier] $*" >&2
}

# at::warn <msg>  — warning level
at::warn() {
  echo "[agenttier] WARN: $*" >&2
}

# at::err <msg>  — error level; does NOT exit (caller decides)
at::err() {
  echo "[agenttier] ERROR: $*" >&2
}

# at::fatal <msg>  — print error and exit 1
at::fatal() {
  at::err "$*"
  exit 1
}

# at::step <msg>  — prominent section header
at::step() {
  echo "" >&2
  echo "==> $*" >&2
}

# --- Config loading ---

# at::load_config  — source config/config.env if it exists; fall back to
# config/config.env.example defaults (which are inert — values are only
# meaningful when the user copies and edits the .env file).
at::load_config() {
  local root
  root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

  if [[ -f "${root}/config/config.env" ]]; then
    at::log "Loading config from config/config.env"
    # shellcheck source=/dev/null
    source "${root}/config/config.env"
  fi

  # Apply defaults for any variable not yet set.
  AGENTTIER_REGISTRY="${AGENTTIER_REGISTRY:-ghcr.io/agenttier}"
  AGENTTIER_IMAGE_TAG="${AGENTTIER_IMAGE_TAG:-}"
  AGENTTIER_CHART_REPO_URL="${AGENTTIER_CHART_REPO_URL:-https://agenttier.github.io/agenttier/charts}"
  AGENTTIER_DOCS_BASE_URL="${AGENTTIER_DOCS_BASE_URL:-https://agenttier.github.io/agenttier}"
  AGENTTIER_TARGET="${AGENTTIER_TARGET:-local}"
  AGENTTIER_EKS_PLATFORM="${AGENTTIER_EKS_PLATFORM:-linux/amd64}"
  AGENTTIER_AWS_REGION="${AGENTTIER_AWS_REGION:-us-east-1}"
  AGENTTIER_TERRAFORM_DIR="${AGENTTIER_TERRAFORM_DIR:-terraform/aws-eks}"
  AGENTTIER_KIND_CLUSTER="${AGENTTIER_KIND_CLUSTER:-agenttier-local}"
  AGENTTIER_HELM_RELEASE="${AGENTTIER_HELM_RELEASE:-agenttier}"
  # "agenttier" matches the Terraform helm_release namespace (agenttier.tf:35)
  # and the Helm chart default. Use this value consistently end-to-end.
  AGENTTIER_NAMESPACE="${AGENTTIER_NAMESPACE:-agenttier}"

  export AGENTTIER_REGISTRY AGENTTIER_IMAGE_TAG AGENTTIER_CHART_REPO_URL
  export AGENTTIER_DOCS_BASE_URL AGENTTIER_TARGET AGENTTIER_EKS_PLATFORM
  export AGENTTIER_AWS_REGION AGENTTIER_TERRAFORM_DIR
  export AGENTTIER_KIND_CLUSTER AGENTTIER_HELM_RELEASE AGENTTIER_NAMESPACE
}

# --- Prereq checks ---

# at::require_cmd <cmd> [<hint>]  — fatal if command not in PATH
at::require_cmd() {
  local cmd="$1"
  local hint="${2:-}"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    if [[ -n "${hint}" ]]; then
      at::fatal "Required command '${cmd}' not found. ${hint}"
    else
      at::fatal "Required command '${cmd}' not found. Install it and re-run."
    fi
  fi
}

# at::check_local_prereqs  — all tools needed for --target=local
at::check_local_prereqs() {
  at::step "Checking local prerequisites"
  at::require_cmd docker  "Install Docker: https://docs.docker.com/get-docker/"
  at::require_cmd kubectl "Install kubectl: https://kubernetes.io/docs/tasks/tools/"
  at::require_cmd helm    "Install Helm: https://helm.sh/docs/intro/install/"
  at::require_cmd go      "Install Go 1.25+: https://go.dev/dl/"

  # Need at least one local cluster tool.
  if ! command -v kind >/dev/null 2>&1 && ! command -v minikube >/dev/null 2>&1; then
    at::fatal "Neither 'kind' nor 'minikube' found. Install one:
  kind:     https://kind.sigs.k8s.io/docs/user/quick-start/#installation
  minikube: https://minikube.sigs.k8s.io/docs/start/"
  fi

  at::log "Local prerequisites OK."
}

# at::check_eks_prereqs  — all tools needed for --target=eks
#
# Docker / docker buildx are required ONLY for the default local-build path.
# When the CodeBuild path is selected (AGENTTIER_USE_CODEBUILD=true — set by
# deploy.sh either via explicit opt-in or no-Docker auto-detect), images are
# built in the cloud, so a local Docker daemon is NOT required. In that case we
# skip the docker/buildx checks (D1a). All other tools (aws, terraform, kubectl,
# helm, jq, zip) and the AWS-credential check remain unconditional.
at::check_eks_prereqs() {
  at::step "Checking EKS prerequisites"
  at::require_cmd aws        "Install AWS CLI v2: https://docs.aws.amazon.com/cli/latest/userguide/install-cliv2.html"
  at::require_cmd terraform  "Install Terraform >=1.5: https://developer.hashicorp.com/terraform/install"
  at::require_cmd kubectl    "Install kubectl: https://kubernetes.io/docs/tasks/tools/"
  at::require_cmd helm       "Install Helm: https://helm.sh/docs/intro/install/"
  # jq: used by the teardown path to enumerate LoadBalancer services.
  at::require_cmd jq         "Install jq: https://jqlang.github.io/jq/download/"
  # zip: used by the CodeBuild opt-in path to package source into an S3 zip.
  at::require_cmd zip        "Install zip: apt-get install zip  OR  brew install zip"

  if [[ "${AGENTTIER_USE_CODEBUILD:-false}" == "true" ]]; then
    # Cloud build path — images are built in AWS CodeBuild. No local Docker needed.
    at::log "CodeBuild path selected — skipping local Docker/buildx checks."
  else
    # Local build path — require Docker + buildx (for --platform cross-arch push).
    at::require_cmd docker   "Install Docker: https://docs.docker.com/get-docker/  (or set AGENTTIER_USE_CODEBUILD=true to build in AWS CodeBuild)"
    if ! docker buildx version >/dev/null 2>&1; then
      at::fatal "Docker buildx not available. Upgrade Docker Desktop or install the buildx plugin, or set AGENTTIER_USE_CODEBUILD=true to build in AWS CodeBuild."
    fi
  fi

  # Verify AWS credentials.
  at::log "Verifying AWS credentials..."
  if ! aws sts get-caller-identity --no-cli-pager --output json >/dev/null 2>&1; then
    at::fatal "AWS credentials not configured or not valid. Run 'aws configure' or set AWS_PROFILE."
  fi
  local identity
  identity=$(aws sts get-caller-identity --no-cli-pager --output text --query 'Arn')
  at::log "AWS identity: ${identity}"

  at::log "EKS prerequisites OK."
}

# at::check_shellcheck  — advisory only; not required for deploy
at::check_shellcheck() {
  if command -v shellcheck >/dev/null 2>&1; then
    at::log "shellcheck available."
  fi
}
