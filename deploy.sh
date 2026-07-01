#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# deploy.sh — single entrypoint for building and deploying AgentTier.
#
# Usage:
#   ./deploy.sh --target=local              Build + deploy to a local kind/minikube cluster
#   ./deploy.sh --target=eks                Build + deploy to AWS EKS via Terraform
#   ./deploy.sh --target=local --teardown   Delete the local cluster
#   ./deploy.sh --target=eks   --teardown   Uninstall Helm + destroy Terraform infra
#   ./deploy.sh --help                      Show this help
#
# Configuration (env or config/config.env):
#   See config/config.env.example for all variables and their defaults.
#   Copy it to config/config.env and edit before running.
#
# Pre-requisites by target:
#   local: docker, kubectl, kind OR minikube, helm, go
#   eks:   aws cli, terraform >=1.5, docker (with buildx), kubectl, helm
#
# All operations are non-interactive (-auto-approve, --yes, etc.).
set -euo pipefail

# ---------------------------------------------------------------------------
# Resolve repo root regardless of CWD.
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export REPO_ROOT="${SCRIPT_DIR}"

# ---------------------------------------------------------------------------
# Source shared library (logging, prereq checks, config loading).
# ---------------------------------------------------------------------------
# shellcheck source=hack/lib/common.sh
# shellcheck disable=SC1091
source "${REPO_ROOT}/hack/lib/common.sh"
# shellcheck source=hack/lib/version.sh
# shellcheck disable=SC1091
source "${REPO_ROOT}/hack/lib/version.sh"

# ---------------------------------------------------------------------------
# Parse arguments.
# ---------------------------------------------------------------------------
DEPLOY_TARGET=""
TEARDOWN=false

usage() {
  cat >&2 <<'EOF'
Usage: ./deploy.sh --target=<local|eks> [--teardown] [--help]

Targets:
  local     Build images, side-load into kind/minikube, install Helm chart with
            devAuth enabled. Zero cloud cost. Requires: docker, kubectl,
            kind or minikube, helm, go.
  eks       Run terraform apply (VPC+EKS+ECR+IRSA+Cognito), build+push images
            to ECR via docker buildx, install Helm chart with Cognito OIDC auth.
            Requires: aws cli, terraform >=1.5, docker (buildx), kubectl, helm.

Flags:
  --teardown  For local: delete the kind/minikube cluster.
              For eks: helm uninstall + delete PVCs/LB services + terraform destroy.
  --help      Show this help message.

Configuration:
  Copy config/config.env.example to config/config.env and edit.
  All variables have documented defaults (see the example file).
EOF
}

for arg in "$@"; do
  case "${arg}" in
    --target=*) DEPLOY_TARGET="${arg#--target=}" ;;
    --teardown)  TEARDOWN=true ;;
    --help|-h)   usage; exit 0 ;;
    *)
      at::err "Unknown argument: ${arg}"
      usage
      exit 1
      ;;
  esac
done

if [[ -z "${DEPLOY_TARGET}" ]]; then
  at::err "--target is required."
  usage
  exit 1
fi

if [[ "${DEPLOY_TARGET}" != "local" && "${DEPLOY_TARGET}" != "eks" ]]; then
  at::err "Unknown target '${DEPLOY_TARGET}'. Must be 'local' or 'eks'."
  usage
  exit 1
fi

# ---------------------------------------------------------------------------
# Load config + resolve image tag.
# ---------------------------------------------------------------------------
at::load_config

# AGENTTIER_IMAGE_TAG is already set by version.sh (sourced above); at::load_config
# may have overridden it from config/config.env — re-derive if still empty.
if [[ -z "${AGENTTIER_IMAGE_TAG:-}" ]]; then
  # shellcheck source=hack/lib/version.sh
  # shellcheck disable=SC1091
  source "${REPO_ROOT}/hack/lib/version.sh"
fi
IMAGE_TAG="${AGENTTIER_IMAGE_TAG}"

at::log "Deploy target : ${DEPLOY_TARGET}"
at::log "Image tag     : ${IMAGE_TAG}"
at::log "Namespace     : ${AGENTTIER_NAMESPACE}"
at::log "Helm release  : ${AGENTTIER_HELM_RELEASE}"

# ---------------------------------------------------------------------------
# Smoke test helper — called at the end of both deploy paths.
# ---------------------------------------------------------------------------
run_smoke_test() {
  at::step "Running smoke test"
  export AGENTTIER_NAMESPACE
  export AGENTTIER_SMOKE_TIMEOUT="${AGENTTIER_SMOKE_TIMEOUT:-300}"
  bash "${REPO_ROOT}/hack/smoke-test.sh"
}

# ===========================================================================
# LOCAL TEARDOWN
# ===========================================================================
if [[ "${DEPLOY_TARGET}" == "local" && "${TEARDOWN}" == true ]]; then
  at::step "Teardown: local cluster"

  # Detect cluster tool.
  if command -v kind >/dev/null 2>&1; then
    CLUSTER_TOOL=kind
  elif command -v minikube >/dev/null 2>&1; then
    CLUSTER_TOOL=minikube
  else
    at::fatal "Neither 'kind' nor 'minikube' found — cannot tear down."
  fi

  # Uninstall Helm release (fail loudly).
  if helm status "${AGENTTIER_HELM_RELEASE}" -n "${AGENTTIER_NAMESPACE}" >/dev/null 2>&1; then
    at::log "Uninstalling Helm release ${AGENTTIER_HELM_RELEASE}..."
    helm uninstall "${AGENTTIER_HELM_RELEASE}" -n "${AGENTTIER_NAMESPACE}" --wait \
      --timeout 120s
  else
    at::log "Helm release not found (already uninstalled)."
  fi

  # Delete the cluster.
  if [[ "${CLUSTER_TOOL}" == "kind" ]]; then
    if kind get clusters 2>/dev/null | grep -q "^${AGENTTIER_KIND_CLUSTER}$"; then
      at::log "Deleting kind cluster '${AGENTTIER_KIND_CLUSTER}'..."
      kind delete cluster --name "${AGENTTIER_KIND_CLUSTER}"
    else
      at::log "Kind cluster '${AGENTTIER_KIND_CLUSTER}' not found."
    fi
  else
    at::log "Deleting minikube cluster..."
    minikube delete || true
  fi

  at::log "Local teardown complete."
  exit 0
fi

# ===========================================================================
# EKS TEARDOWN
# ===========================================================================
if [[ "${DEPLOY_TARGET}" == "eks" && "${TEARDOWN}" == true ]]; then
  at::step "Teardown: EKS"
  at::check_eks_prereqs

  TF_DIR="${REPO_ROOT}/${AGENTTIER_TERRAFORM_DIR}"

  # Step 1: Uninstall Helm release (fail loudly — orphaned pods/PVCs cause TF destroy issues).
  at::step "Uninstalling Helm release"
  if helm status "${AGENTTIER_HELM_RELEASE}" -n "${AGENTTIER_NAMESPACE}" >/dev/null 2>&1; then
    at::log "Uninstalling ${AGENTTIER_HELM_RELEASE}..."
    helm uninstall "${AGENTTIER_HELM_RELEASE}" -n "${AGENTTIER_NAMESPACE}" --wait \
      --timeout 120s
  else
    at::log "Helm release not found (already uninstalled)."
  fi

  # Step 2: Delete PVCs and LoadBalancer services, wait for LB deprovisioning.
  at::step "Deleting PVCs and LoadBalancer services"
  if kubectl get namespace "${AGENTTIER_NAMESPACE}" >/dev/null 2>&1; then
    # Delete sandboxes first to release PVCs.
    at::log "Deleting all Sandboxes..."
    kubectl delete sandboxes --all --all-namespaces --timeout=60s 2>/dev/null || true

    # Delete PVCs in the install namespace (where sandboxes + helm release live).
    # Do NOT target the 'default' namespace — AgentTier never installs there.
    at::log "Deleting PVCs in namespace ${AGENTTIER_NAMESPACE}..."
    kubectl delete pvc --all -n "${AGENTTIER_NAMESPACE}" --timeout=60s 2>/dev/null || true

    # Delete LoadBalancer services and wait for AWS LB deprovisioning.
    at::log "Deleting LoadBalancer services..."
    kubectl get svc --all-namespaces -o json \
      | jq -r '.items[] | select(.spec.type=="LoadBalancer") | "\(.metadata.namespace) \(.metadata.name)"' \
      | while read -r ns svc; do
          at::log "  Deleting LB service ${ns}/${svc}..."
          kubectl delete svc "${svc}" -n "${ns}" --timeout=60s || true
        done

    at::log "Waiting 30s for AWS load balancers to deprovision..."
    sleep 30
  fi

  # Step 3: Terraform destroy.
  # C2 fix: init with the REAL backend (matching apply) so destroy operates on
  # the actual remote state. -backend=false gives an empty local state and
  # destroys NOTHING, leaving EKS+NAT+ECR running and billing. No || true —
  # fail loudly if init or destroy fails.
  at::step "Destroying Terraform infrastructure"
  cd "${TF_DIR}"
  terraform init -input=false
  terraform destroy -auto-approve \
    -var="region=${AGENTTIER_AWS_REGION}"
  cd "${REPO_ROOT}"

  at::log "EKS teardown complete."
  exit 0
fi

# ===========================================================================
# LOCAL DEPLOY
# ===========================================================================
if [[ "${DEPLOY_TARGET}" == "local" ]]; then
  at::check_local_prereqs

  # Detect cluster tool (prefer kind).
  if command -v kind >/dev/null 2>&1; then
    CLUSTER_TOOL=kind
  else
    CLUSTER_TOOL=minikube
  fi

  # Step 1: Create cluster if absent.
  at::step "Ensuring local cluster exists"
  if [[ "${CLUSTER_TOOL}" == "kind" ]]; then
    if ! kind get clusters 2>/dev/null | grep -q "^${AGENTTIER_KIND_CLUSTER}$"; then
      at::log "Creating kind cluster '${AGENTTIER_KIND_CLUSTER}'..."
      kind create cluster --name "${AGENTTIER_KIND_CLUSTER}" --wait 120s
    else
      at::log "Kind cluster '${AGENTTIER_KIND_CLUSTER}' already exists."
    fi
    # Ensure kubectl context points at this cluster.
    kubectl config use-context "kind-${AGENTTIER_KIND_CLUSTER}"
  else
    if ! minikube status >/dev/null 2>&1; then
      at::log "Starting minikube..."
      minikube start --wait=all
    else
      at::log "Minikube already running."
    fi
  fi

  # Step 2: Build images (local arch, no push).
  # Build ALL images referenced by the Helm chart so the cluster can pull them
  # without contacting a registry (IfNotPresent + side-loaded). This covers:
  # - controller, router, web-ui (system components)
  # - 6 sandbox images backing the default ClusterSandboxTemplates (FR-1/D1):
  #   general-coding, claude-code, openclaw, langgraph, rl, strands-bedrock.
  # images/minimal is NOT referenced by any default template — omitted.
  at::step "Building container images (local arch)"
  REGISTRY="${AGENTTIER_REGISTRY}"
  CONTROLLER_IMG="${REGISTRY}/controller:${IMAGE_TAG}"
  ROUTER_IMG="${REGISTRY}/router:${IMAGE_TAG}"
  WEBUI_IMG="${REGISTRY}/web-ui:${IMAGE_TAG}"

  at::log "Building controller image: ${CONTROLLER_IMG}"
  docker build -t "${CONTROLLER_IMG}" -f "${REPO_ROOT}/Dockerfile.controller" \
    "${REPO_ROOT}"

  at::log "Building router image: ${ROUTER_IMG}"
  docker build -t "${ROUTER_IMG}" -f "${REPO_ROOT}/Dockerfile.router" \
    "${REPO_ROOT}"

  at::log "Building web-ui image: ${WEBUI_IMG}"
  docker build -t "${WEBUI_IMG}" -f "${REPO_ROOT}/web-ui/Dockerfile" \
    "${REPO_ROOT}/web-ui"

  # Build all 6 sandbox images. Each entry is:
  #   <short-name>:<image-dir-relative-to-images/> (same dir unless noted)
  # general-coding lives at images/general-coding and is the default sandbox.
  declare -A SANDBOX_IMAGE_DIRS
  SANDBOX_IMAGE_DIRS=(
    ["sandbox-general"]="general-coding"
    ["sandbox-claude-code"]="claude-code"
    ["sandbox-openclaw"]="openclaw"
    ["sandbox-langgraph"]="langgraph"
    ["sandbox-rl"]="rl"
    ["sandbox-strands-bedrock"]="strands-bedrock"
  )
  declare -A SANDBOX_IMGS
  for sbx_name in sandbox-general sandbox-claude-code sandbox-openclaw \
                  sandbox-langgraph sandbox-rl sandbox-strands-bedrock; do
    sbx_dir="${SANDBOX_IMAGE_DIRS[${sbx_name}]}"
    sbx_img="${REGISTRY}/${sbx_name}:${IMAGE_TAG}"
    SANDBOX_IMGS["${sbx_name}"]="${sbx_img}"
    at::log "Building ${sbx_name}: ${sbx_img}"
    docker build -t "${sbx_img}" \
      -f "${REPO_ROOT}/images/${sbx_dir}/Dockerfile" \
      "${REPO_ROOT}/images/${sbx_dir}"
  done

  # Step 3: Side-load images into local cluster.
  at::step "Loading images into local cluster"
  if [[ "${CLUSTER_TOOL}" == "kind" ]]; then
    at::log "Loading images into kind cluster '${AGENTTIER_KIND_CLUSTER}'..."
    kind load docker-image "${CONTROLLER_IMG}" --name "${AGENTTIER_KIND_CLUSTER}"
    kind load docker-image "${ROUTER_IMG}"     --name "${AGENTTIER_KIND_CLUSTER}"
    kind load docker-image "${WEBUI_IMG}"      --name "${AGENTTIER_KIND_CLUSTER}"
    for sbx_name in sandbox-general sandbox-claude-code sandbox-openclaw \
                    sandbox-langgraph sandbox-rl sandbox-strands-bedrock; do
      kind load docker-image "${SANDBOX_IMGS[${sbx_name}]}" \
        --name "${AGENTTIER_KIND_CLUSTER}"
    done
  else
    at::log "Loading images into minikube..."
    minikube image load "${CONTROLLER_IMG}"
    minikube image load "${ROUTER_IMG}"
    minikube image load "${WEBUI_IMG}"
    for sbx_name in sandbox-general sandbox-claude-code sandbox-openclaw \
                    sandbox-langgraph sandbox-rl sandbox-strands-bedrock; do
      minikube image load "${SANDBOX_IMGS[${sbx_name}]}"
    done
  fi

  # Step 4: Install / upgrade Helm chart.
  # Controller manages CRDs on startup (--manage-crds=true, D15/H3).
  # No `kubectl apply -f config/crd/` — the controller's startup apply is the
  # canonical path; pre-applying config/crd/ risks drift.
  at::step "Installing / upgrading Helm chart"
  at::log "Helm release: ${AGENTTIER_HELM_RELEASE} → namespace: ${AGENTTIER_NAMESPACE}"
  helm upgrade "${AGENTTIER_HELM_RELEASE}" "${REPO_ROOT}/helm/agenttier/" \
    --install \
    --namespace "${AGENTTIER_NAMESPACE}" \
    --create-namespace \
    --values "${REPO_ROOT}/helm/agenttier/values-local.yaml" \
    --set "controller.image.repository=${REGISTRY}/controller" \
    --set "controller.image.tag=${IMAGE_TAG}" \
    --set "router.image.repository=${REGISTRY}/router" \
    --set "router.image.tag=${IMAGE_TAG}" \
    --set "webui.image.repository=${REGISTRY}/web-ui" \
    --set "webui.image.tag=${IMAGE_TAG}" \
    --set "defaults.sandbox.image=${SANDBOX_IMGS[sandbox-general]}" \
    --set "defaults.claudeCode.image=${SANDBOX_IMGS[sandbox-claude-code]}" \
    --set "defaults.openclaw.image=${SANDBOX_IMGS[sandbox-openclaw]}" \
    --set "defaults.langgraph.image=${SANDBOX_IMGS[sandbox-langgraph]}" \
    --set "defaults.rl.image=${SANDBOX_IMGS[sandbox-rl]}" \
    --set "defaults.strandsBedrock.image=${SANDBOX_IMGS[sandbox-strands-bedrock]}" \
    --wait \
    --timeout 180s

  # Step 5: Run smoke test.
  run_smoke_test

  at::log ""
  at::log "Local deploy complete!"
  at::log ""
  at::log "Access the web UI:    kubectl port-forward -n ${AGENTTIER_NAMESPACE} svc/${AGENTTIER_HELM_RELEASE}-webui 8080:80"
  at::log "Access the router:    kubectl port-forward -n ${AGENTTIER_NAMESPACE} svc/${AGENTTIER_HELM_RELEASE}-router 8081:8080"
  at::log "Tear down:            ./deploy.sh --target=local --teardown"
  exit 0
fi

# ===========================================================================
# EKS DEPLOY
# ===========================================================================
if [[ "${DEPLOY_TARGET}" == "eks" ]]; then
  at::check_eks_prereqs

  TF_DIR="${REPO_ROOT}/${AGENTTIER_TERRAFORM_DIR}"

  # Step 1: Terraform init + apply.
  # C3 fix: pass install_agenttier=false so Terraform does NOT install the
  # PUBLISHED chart from agenttier.github.io. deploy.sh installs the SOURCE
  # chart in Step 6 — double-installing would violate D1 and leave a stale
  # published release competing with our source-built one.
  at::step "Provisioning infrastructure via Terraform"
  at::log "Terraform directory: ${TF_DIR}"
  cd "${TF_DIR}"
  terraform init -input=false
  terraform apply -auto-approve \
    -var="region=${AGENTTIER_AWS_REGION}" \
    -var="install_agenttier=false"
  cd "${REPO_ROOT}"

  # Step 2: Read ECR registry and cluster info from Terraform outputs.
  at::step "Reading Terraform outputs"
  cd "${TF_DIR}"
  ECR_REGISTRY="$(terraform output -raw ecr_registry --no-cli-pager 2>/dev/null)"
  ECR_CONTROLLER_URL="$(terraform output -raw ecr_controller_url --no-cli-pager 2>/dev/null)"
  ECR_ROUTER_URL="$(terraform output -raw ecr_router_url --no-cli-pager 2>/dev/null)"
  ECR_WEBUI_URL="$(terraform output -raw ecr_webui_url --no-cli-pager 2>/dev/null)"
  ECR_SANDBOX_URL="$(terraform output -raw ecr_sandbox_general_url --no-cli-pager 2>/dev/null)"
  ECR_SANDBOX_CLAUDE_CODE_URL="$(terraform output -raw ecr_sandbox_claude_code_url --no-cli-pager 2>/dev/null)"
  ECR_SANDBOX_OPENCLAW_URL="$(terraform output -raw ecr_sandbox_openclaw_url --no-cli-pager 2>/dev/null)"
  ECR_SANDBOX_LANGGRAPH_URL="$(terraform output -raw ecr_sandbox_langgraph_url --no-cli-pager 2>/dev/null)"
  ECR_SANDBOX_RL_URL="$(terraform output -raw ecr_sandbox_rl_url --no-cli-pager 2>/dev/null)"
  ECR_SANDBOX_STRANDS_BEDROCK_URL="$(terraform output -raw ecr_sandbox_strands_bedrock_url --no-cli-pager 2>/dev/null)"
  CLUSTER_NAME="$(terraform output -raw cluster_name --no-cli-pager 2>/dev/null)"
  COGNITO_ISSUER="$(terraform output -raw cognito_issuer_url --no-cli-pager 2>/dev/null)"
  COGNITO_CLIENT_ID="$(terraform output -raw cognito_client_id --no-cli-pager 2>/dev/null)"
  COGNITO_ADMIN_GROUP="$(terraform output -raw cognito_admin_group --no-cli-pager 2>/dev/null)"

  # C5: fail loudly if any MANDATORY output is empty. terraform output -raw
  # returns "" (with 2>/dev/null swallowing the error) when the state is stale,
  # empty, or read from the wrong directory — an empty value would otherwise
  # flow silently into the helm --set flags and produce a broken install.
  for _tf_var in ECR_REGISTRY ECR_CONTROLLER_URL ECR_ROUTER_URL ECR_WEBUI_URL \
                 ECR_SANDBOX_URL \
                 ECR_SANDBOX_CLAUDE_CODE_URL ECR_SANDBOX_OPENCLAW_URL \
                 ECR_SANDBOX_LANGGRAPH_URL ECR_SANDBOX_RL_URL \
                 ECR_SANDBOX_STRANDS_BEDROCK_URL \
                 CLUSTER_NAME COGNITO_ISSUER COGNITO_CLIENT_ID \
                 COGNITO_ADMIN_GROUP; do
    if [[ -z "${!_tf_var:-}" ]]; then
      at::fatal "Terraform output for ${_tf_var} is empty — is the cluster applied and is ${TF_DIR} the correct state directory? Re-run: (cd ${TF_DIR} && terraform apply)."
    fi
  done

  # CodeBuild outputs (only meaningful when enable_codebuild=true).
  CODEBUILD_PROJECT="$(terraform output -raw codebuild_project --no-cli-pager 2>/dev/null || true)"
  CODEBUILD_S3_BUCKET="$(terraform output -raw codebuild_s3_bucket --no-cli-pager 2>/dev/null || true)"
  CODEBUILD_TIMEOUT="$(terraform output -raw codebuild_timeout_minutes --no-cli-pager 2>/dev/null || echo "30")"

  cd "${REPO_ROOT}"

  at::log "ECR registry     : ${ECR_REGISTRY}"
  at::log "EKS cluster      : ${CLUSTER_NAME}"
  at::log "Cognito issuer   : ${COGNITO_ISSUER}"

  # Step 3: Authenticate Docker to ECR.
  at::step "Authenticating Docker to ECR"
  aws ecr get-login-password --region "${AGENTTIER_AWS_REGION}" --no-cli-pager \
    | docker login --username AWS --password-stdin "${ECR_REGISTRY}"

  # Step 4: Build + push images to ECR.
  #
  # Default path: local docker buildx → ECR.
  # CodeBuild path (opt-in): upload source zip to S3, start build, poll with
  # bounded timeout (fixes audit M6). Activated when CODEBUILD_PROJECT is set
  # (which only happens when enable_codebuild=true in Terraform).
  #
  # H3/D15: NO `kubectl apply -f config/crd/` — controller manages CRDs.
  PLATFORM="${AGENTTIER_EKS_PLATFORM}"

  if [[ -n "${CODEBUILD_PROJECT:-}" ]]; then
    # CodeBuild opt-in path.
    at::step "Building images via CodeBuild (opt-in)"
    at::log "CodeBuild project  : ${CODEBUILD_PROJECT}"
    at::log "S3 source bucket   : ${CODEBUILD_S3_BUCKET}"
    at::log "Build timeout      : ${CODEBUILD_TIMEOUT} minutes"

    # Upload source zip.
    at::log "Packaging source..."
    SOURCE_ZIP="/tmp/agenttier-source-$$.zip"
    zip -r "${SOURCE_ZIP}" . \
      -x '.git/*' 'terraform/*' 'node_modules/*' '*.DS_Store' '.venv/*' \
      >/dev/null
    at::log "Uploading source to s3://${CODEBUILD_S3_BUCKET}/source.zip..."
    aws s3 cp "${SOURCE_ZIP}" \
      "s3://${CODEBUILD_S3_BUCKET}/source.zip" \
      --region "${AGENTTIER_AWS_REGION}" \
      --no-cli-pager
    rm -f "${SOURCE_ZIP}"

    # Start build.
    BUILD_ID="$(aws codebuild start-build \
      --project-name "${CODEBUILD_PROJECT}" \
      --region "${AGENTTIER_AWS_REGION}" \
      --no-cli-pager \
      --output text \
      --query 'build.id')"
    at::log "CodeBuild build ID : ${BUILD_ID}"

    # Poll with bounded timeout (M6 fix — never loops forever).
    MAX_POLLS=$(( CODEBUILD_TIMEOUT * 60 / 15 ))  # check every 15s
    POLL=0
    while true; do
      STATUS="$(aws codebuild batch-get-builds \
        --ids "${BUILD_ID}" \
        --region "${AGENTTIER_AWS_REGION}" \
        --no-cli-pager \
        --query 'builds[0].buildStatus' \
        --output text)"
      case "${STATUS}" in
        SUCCEEDED)
          at::log "CodeBuild succeeded. Images pushed to ECR."
          break
          ;;
        FAILED|FAULT|STOPPED|TIMED_OUT)
          at::fatal "CodeBuild failed with status: ${STATUS}. Check logs: aws codebuild batch-get-builds --ids ${BUILD_ID} --region ${AGENTTIER_AWS_REGION}"
          ;;
        IN_PROGRESS|QUEUED|*)
          POLL=$(( POLL + 1 ))
          if [[ ${POLL} -ge ${MAX_POLLS} ]]; then
            at::fatal "CodeBuild did not complete within ${CODEBUILD_TIMEOUT} minutes (build ID: ${BUILD_ID}). Increase codebuild_timeout_minutes in Terraform or investigate the build."
          fi
          at::log "  CodeBuild status: ${STATUS} (poll ${POLL}/${MAX_POLLS}; checking again in 15s...)"
          sleep 15
          ;;
      esac
    done
  else
    # Default path: local docker buildx → ECR.
    at::step "Building images with docker buildx (platform: ${PLATFORM})"
    at::log "Pushing to ECR registry: ${ECR_REGISTRY}"

    # Ensure a buildx builder that supports the target platform.
    BUILDER_NAME="agenttier-builder"
    if ! docker buildx inspect "${BUILDER_NAME}" >/dev/null 2>&1; then
      at::log "Creating buildx builder '${BUILDER_NAME}'..."
      docker buildx create --name "${BUILDER_NAME}" --driver docker-container --use
    fi
    docker buildx use "${BUILDER_NAME}"

    at::log "Building + pushing controller: ${ECR_CONTROLLER_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_CONTROLLER_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/Dockerfile.controller" \
      --push \
      "${REPO_ROOT}"

    at::log "Building + pushing router: ${ECR_ROUTER_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_ROUTER_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/Dockerfile.router" \
      --push \
      "${REPO_ROOT}"

    at::log "Building + pushing web-ui: ${ECR_WEBUI_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_WEBUI_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/web-ui/Dockerfile" \
      --push \
      "${REPO_ROOT}/web-ui"

    # Sandbox images — all 6 ClusterSandboxTemplates in the chart (FR-1/D1).
    # images/minimal is NOT referenced by any default template — omitted.
    at::log "Building + pushing sandbox-general: ${ECR_SANDBOX_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_SANDBOX_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/images/general-coding/Dockerfile" \
      --push \
      "${REPO_ROOT}/images/general-coding"

    at::log "Building + pushing sandbox-claude-code: ${ECR_SANDBOX_CLAUDE_CODE_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_SANDBOX_CLAUDE_CODE_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/images/claude-code/Dockerfile" \
      --push \
      "${REPO_ROOT}/images/claude-code"

    at::log "Building + pushing sandbox-openclaw: ${ECR_SANDBOX_OPENCLAW_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_SANDBOX_OPENCLAW_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/images/openclaw/Dockerfile" \
      --push \
      "${REPO_ROOT}/images/openclaw"

    at::log "Building + pushing sandbox-langgraph: ${ECR_SANDBOX_LANGGRAPH_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_SANDBOX_LANGGRAPH_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/images/langgraph/Dockerfile" \
      --push \
      "${REPO_ROOT}/images/langgraph"

    at::log "Building + pushing sandbox-rl: ${ECR_SANDBOX_RL_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_SANDBOX_RL_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/images/rl/Dockerfile" \
      --push \
      "${REPO_ROOT}/images/rl"

    at::log "Building + pushing sandbox-strands-bedrock: ${ECR_SANDBOX_STRANDS_BEDROCK_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_SANDBOX_STRANDS_BEDROCK_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/images/strands-bedrock/Dockerfile" \
      --push \
      "${REPO_ROOT}/images/strands-bedrock"
  fi

  # Step 5: Configure kubectl for the new cluster.
  at::step "Configuring kubectl for EKS cluster '${CLUSTER_NAME}'"
  aws eks update-kubeconfig \
    --region "${AGENTTIER_AWS_REGION}" \
    --name "${CLUSTER_NAME}" \
    --no-cli-pager
  at::log "kubectl context updated. Nodes:"
  kubectl get nodes --no-headers

  # Step 6: Install / upgrade Helm chart.
  # Wires Cognito OIDC auth — NEVER uses devAuth (D8, design.md#8).
  at::step "Installing / upgrading Helm chart"
  at::log "Helm release: ${AGENTTIER_HELM_RELEASE} → namespace: ${AGENTTIER_NAMESPACE}"
  helm upgrade "${AGENTTIER_HELM_RELEASE}" "${REPO_ROOT}/helm/agenttier/" \
    --install \
    --namespace "${AGENTTIER_NAMESPACE}" \
    --create-namespace \
    --set "controller.image.repository=${ECR_CONTROLLER_URL}" \
    --set "controller.image.tag=${IMAGE_TAG}" \
    --set "router.image.repository=${ECR_ROUTER_URL}" \
    --set "router.image.tag=${IMAGE_TAG}" \
    --set "webui.image.repository=${ECR_WEBUI_URL}" \
    --set "webui.image.tag=${IMAGE_TAG}" \
    --set "defaults.sandbox.image=${ECR_SANDBOX_URL}:${IMAGE_TAG}" \
    --set "defaults.claudeCode.image=${ECR_SANDBOX_CLAUDE_CODE_URL}:${IMAGE_TAG}" \
    --set "defaults.openclaw.image=${ECR_SANDBOX_OPENCLAW_URL}:${IMAGE_TAG}" \
    --set "defaults.langgraph.image=${ECR_SANDBOX_LANGGRAPH_URL}:${IMAGE_TAG}" \
    --set "defaults.rl.image=${ECR_SANDBOX_RL_URL}:${IMAGE_TAG}" \
    --set "defaults.strandsBedrock.image=${ECR_SANDBOX_STRANDS_BEDROCK_URL}:${IMAGE_TAG}" \
    --set "auth.devAuth=false" \
    --set "auth.oidc.issuerUrl=${COGNITO_ISSUER}" \
    --set "auth.oidc.clientId=${COGNITO_CLIENT_ID}" \
    --set "auth.oidc.adminGroup=${COGNITO_ADMIN_GROUP}" \
    --set "auth.oidc.groupClaim=cognito:groups" \
    --set "optional.storageClass.enabled=true" \
    --set "optional.storageClass.isDefaultClass=true" \
    --wait \
    --timeout 300s

  # Step 7: Run smoke test.
  run_smoke_test

  at::log ""
  at::log "EKS deploy complete!"
  at::log ""
  at::log "Cluster         : ${CLUSTER_NAME}"
  at::log "Region          : ${AGENTTIER_AWS_REGION}"
  at::log "Cognito issuer  : ${COGNITO_ISSUER}"
  at::log "Cognito client  : ${COGNITO_CLIENT_ID}"
  at::log ""
  at::log "Access the web UI via the ALB (if ingress enabled) or:"
  at::log "  kubectl port-forward -n ${AGENTTIER_NAMESPACE} svc/${AGENTTIER_HELM_RELEASE}-webui 8080:80"
  at::log ""
  at::log "Tear down (WARNING: destroys all EKS resources + ECR images):"
  at::log "  ./deploy.sh --target=eks --teardown"
  at::log ""
  at::log "Estimated cost: ~\$8-10/day while cluster is running."
  exit 0
fi
