#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# scripts/lib/common.sh — shared logging, prereq checks, and env helpers.
#
# Source this file; do not execute directly.
#   source scripts/lib/common.sh
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
  # Mirrors terraform's var.endpoint_access_mode (terraform/aws-eks/variables.tf):
  # "public-restricted" (default, laptop-friendly) or "private" (no public API
  # path — on-cluster deploy steps delegate to CodeBuild-in-VPC; see design.md#4
  # and scripts/lib/common.sh#at::check_eks_prereqs below for the SSM ops note).
  AGENTTIER_ENDPOINT_MODE="${AGENTTIER_ENDPOINT_MODE:-public-restricted}"

  export AGENTTIER_REGISTRY AGENTTIER_IMAGE_TAG AGENTTIER_CHART_REPO_URL
  export AGENTTIER_DOCS_BASE_URL AGENTTIER_TARGET AGENTTIER_EKS_PLATFORM
  export AGENTTIER_AWS_REGION AGENTTIER_TERRAFORM_DIR
  export AGENTTIER_KIND_CLUSTER AGENTTIER_HELM_RELEASE AGENTTIER_NAMESPACE
  export AGENTTIER_ENDPOINT_MODE
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

# at::check_terraform_version  — fatal if terraform is below the required
# minimum. The `use_lockfile` S3-backend setting (terraform/aws-eks/backend.tf,
# design.md#10 FR-7) requires Terraform >= 1.10 — older releases (e.g. the
# widely-installed 1.5.x) don't recognize the `use_lockfile` backend argument
# and fail `terraform init` with a confusing "unsupported argument" error
# instead of a clear version message. Fail loudly here instead (D-A10).
at::check_terraform_version() {
  local min_version="1.10.0"
  local raw_version
  raw_version=$(terraform version -json 2>/dev/null | grep -o '"terraform_version": *"[^"]*"' | grep -o '[0-9][0-9.]*' || true)
  if [[ -z "${raw_version}" ]]; then
    # -json unavailable on very old terraform (<0.13) — fall back to the
    # human-readable first line: "Terraform v1.10.5".
    raw_version=$(terraform version 2>/dev/null | head -n1 | grep -o '[0-9][0-9.]*' || true)
  fi
  if [[ -z "${raw_version}" ]]; then
    at::fatal "Could not determine terraform version. Install Terraform >=${min_version}: https://developer.hashicorp.com/terraform/install"
  fi
  # sort -V performs a true version-aware comparison (1.10.0 > 1.5.7), unlike
  # a plain string/lexicographic sort.
  if [[ "$(printf '%s\n%s\n' "${min_version}" "${raw_version}" | sort -V | head -n1)" != "${min_version}" ]]; then
    at::fatal "Terraform ${raw_version} found, but >=${min_version} is required (the S3 backend's use_lockfile setting needs it — design.md#10). Install a newer terraform: https://developer.hashicorp.com/terraform/install (install it alongside any pinned system terraform per execution-hygiene; do not assume a system default is new enough)."
  fi
  at::log "terraform ${raw_version} OK (>=${min_version} required)."
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
  at::require_cmd terraform  "Install Terraform >=1.10: https://developer.hashicorp.com/terraform/install"
  at::check_terraform_version
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

  # endpoint_access_mode=private (AGENTTIER_ENDPOINT_MODE) means the cluster's
  # Kubernetes API has no public path at all: on-cluster deploy steps delegate
  # to CodeBuild-in-VPC (deploy.sh, ci/buildspec-deploy.yml) and human kubectl/UI
  # access goes through an SSM Session Manager port-forward instead of a
  # directly reachable endpoint — see docs/docs/port-forwarding.md for the
  # runbook (tls-server-name caveat included). Note it here rather than fail;
  # deploy.sh's own precondition (mirroring main.tf's private⇒enable_codebuild
  # check) is what actually enforces the CodeBuild requirement.
  if [[ "${AGENTTIER_ENDPOINT_MODE:-public-restricted}" == "private" ]]; then
    at::log "AGENTTIER_ENDPOINT_MODE=private — no public API path; on-cluster steps run via CodeBuild-in-VPC, human access via SSM port-forward (see docs/docs/port-forwarding.md)."
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
