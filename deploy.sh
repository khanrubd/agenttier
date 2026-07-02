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
#   eks:   aws cli, terraform >=1.10, docker (with buildx), kubectl, helm
#
# All operations are non-interactive (-auto-approve, --yes, etc.).
set -euo pipefail

# ---------------------------------------------------------------------------
# Bash version guard (belt-and-suspenders; script requires bash 3.2+).
# On macOS the system bash at /bin/bash is 3.2; both bash 3.2 and 4+ are
# supported — no bash-4 features (declare -A) are used.
# ---------------------------------------------------------------------------
if [[ "${BASH_VERSINFO[0]:-0}" -lt 3 ]]; then
  echo "ERROR: deploy.sh requires bash 3.2 or newer." >&2
  echo "       On macOS run with: /bin/bash deploy.sh ${*}" >&2
  exit 1
fi

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
            Requires: aws cli, terraform >=1.10, docker (buildx), kubectl, helm.

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

# Short git commit stamped into controller/router binaries via --build-arg
# GIT_COMMIT (Dockerfile ldflags -X …version.GitCommit). Falls back to
# "unknown" (the Dockerfile default) outside a git checkout. VERSION is the
# derived image tag so the /version endpoint matches the deployed tag.
GIT_COMMIT="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)"

# ---------------------------------------------------------------------------
# Decide the EKS image-build path (D1b): CodeBuild (in-cloud) vs local buildx.
#
# USE_CODEBUILD=true when ANY of:
#   - endpoint_access_mode=private (AGENTTIER_ENDPOINT_MODE, set by
#     hack/lib/common.sh::at::load_config) — the private endpoint has no
#     public path, so BOTH image builds and on-cluster deploy steps MUST run
#     inside the VPC (design.md#4). main.tf also asserts this with a
#     precondition (private⇒enable_codebuild) as a fail-closed backstop, but
#     it is checked here first so the error is a clear deploy.sh message
#     instead of a raw terraform precondition failure at apply time.
#   - AGENTTIER_USE_CODEBUILD=true is set explicitly, OR
#   - no local Docker buildx is available (auto-detect Docker-less machines).
#
# When true, deploy.sh passes -var=enable_codebuild=true to terraform apply
# (so the CodeBuild project + S3 source bucket exist) and takes the CodeBuild
# branch in Step 4 (image build) and — when endpoint_access_mode=private —
# Steps 5-7 (on-cluster deploy, delegated to a second CodeBuild run; see
# below). When false, the unchanged local-buildx + local-helm path is used
# throughout.
#
# Exported so hack/lib/common.sh::check_eks_prereqs can skip the docker/buildx
# hard-requirement on the CodeBuild path (D1a).
# ---------------------------------------------------------------------------
if [[ "${DEPLOY_TARGET}" == "eks" ]]; then
  if [[ "${AGENTTIER_ENDPOINT_MODE}" == "private" ]]; then
    if [[ "${AGENTTIER_USE_CODEBUILD:-}" == "false" ]]; then
      at::fatal "endpoint_access_mode=private requires CodeBuild-in-VPC — AGENTTIER_USE_CODEBUILD cannot be 'false' in private mode (design.md#4)."
    fi
    USE_CODEBUILD=true
    at::log "CodeBuild path : enabled (forced — endpoint_access_mode=private requires CodeBuild-in-VPC)"
  elif [[ "${AGENTTIER_USE_CODEBUILD:-}" == "true" ]]; then
    USE_CODEBUILD=true
    at::log "CodeBuild path : enabled (AGENTTIER_USE_CODEBUILD=true)"
  elif ! docker buildx version >/dev/null 2>&1; then
    USE_CODEBUILD=true
    at::log "CodeBuild path : enabled (auto-detected — no local docker buildx)"
  else
    USE_CODEBUILD=false
    at::log "CodeBuild path : disabled (using local docker buildx)"
  fi
else
  USE_CODEBUILD=false
fi
# Export so check_eks_prereqs (sourced from common.sh) sees the decision.
export AGENTTIER_USE_CODEBUILD="${USE_CODEBUILD}"

# ---------------------------------------------------------------------------
# AWS Load Balancer Controller helm toggle (relocated from terraform —
# D-U3/D-A1: load_balancer_controller.tf is deleted, terraform apply is now
# pure-AWS). The two terraform input variables that used to gate this
# (install toggle + chart-version pin) were dead code and have been removed
# (D-A11) — they stopped gating anything once terraform stopped installing
# the LBC. deploy.sh now owns this toggle entirely via its own env vars,
# defaulted here (install=true, chart version 1.8.1) and documented in
# config/config.env.example; there is no terraform side to stay in sync
# with.
# ---------------------------------------------------------------------------
AGENTTIER_INSTALL_LBC="${AGENTTIER_INSTALL_LBC:-true}"
AGENTTIER_LBC_CHART_VERSION="${AGENTTIER_LBC_CHART_VERSION:-1.8.1}"

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

# ---------------------------------------------------------------------------
# CodeBuild poll helper — bounded wait for a build to finish (fixes audit M6:
# never loops forever). Shared by the image-build CodeBuild run (Step 4) and,
# in private mode, the on-cluster deploy-build CodeBuild run (Steps 5-7).
# ---------------------------------------------------------------------------
codebuild_wait_for_build() {
  local build_id="$1"
  local timeout_minutes="$2"
  local max_polls poll status
  max_polls=$(( timeout_minutes * 60 / 15 ))  # check every 15s
  poll=0
  while true; do
    status="$(aws codebuild batch-get-builds \
      --ids "${build_id}" \
      --region "${AGENTTIER_AWS_REGION}" \
      --no-cli-pager \
      --query 'builds[0].buildStatus' \
      --output text)"
    case "${status}" in
      SUCCEEDED)
        at::log "CodeBuild succeeded (build ID: ${build_id})."
        return 0
        ;;
      FAILED|FAULT|STOPPED|TIMED_OUT)
        at::fatal "CodeBuild failed with status: ${status}. Check logs: aws codebuild batch-get-builds --ids ${build_id} --region ${AGENTTIER_AWS_REGION}"
        ;;
      IN_PROGRESS|QUEUED|*)
        poll=$(( poll + 1 ))
        if [[ ${poll} -ge ${max_polls} ]]; then
          at::fatal "CodeBuild did not complete within ${timeout_minutes} minutes (build ID: ${build_id}). Increase codebuild_timeout_minutes in Terraform or investigate the build."
        fi
        at::log "  CodeBuild status: ${status} (poll ${poll}/${max_polls}; checking again in 15s...)"
        sleep 15
        ;;
    esac
  done
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

  # Step 1: Read Terraform outputs needed for on-cluster cleanup. This init
  # uses the REAL backend (matching apply — C2 fix) and also satisfies Step 4's
  # destroy below, so it is not repeated there. Outputs are read best-effort
  # (2>/dev/null || true) — a prior partial teardown or an empty/missing state
  # means there is nothing on-cluster left to clean up, and Step 4's destroy
  # is then a no-op.
  at::step "Reading Terraform outputs"
  cd "${TF_DIR}"
  terraform init -input=false
  CLUSTER_NAME="$(terraform output -raw cluster_name 2>/dev/null || true)"
  # CodeBuild outputs (only meaningful when enable_codebuild=true).
  CODEBUILD_PROJECT="$(terraform output -raw codebuild_project 2>/dev/null || true)"
  CODEBUILD_S3_BUCKET="$(terraform output -raw codebuild_s3_bucket 2>/dev/null || true)"
  CODEBUILD_TIMEOUT="$(terraform output -raw codebuild_timeout_minutes 2>/dev/null || echo "30")"
  cd "${REPO_ROOT}"

  if [[ -z "${CLUSTER_NAME}" ]]; then
    at::log "No cluster_name in Terraform state — skipping on-cluster cleanup (nothing to uninstall)."
  elif [[ "${AGENTTIER_ENDPOINT_MODE}" == "private" ]]; then
    # private: there is no public path to the API server — kubectl/helm
    # invoked from this machine cannot reach it (same reason deploy delegates
    # in Step 5 above). Delegate the on-cluster teardown steps (helm uninstall
    # + sandbox/PVC cleanup + LoadBalancer Service deletion, so real AWS
    # ALBs/NLBs deprovision BEFORE the cluster disappears) to a CodeBuild-in-VPC
    # run using a dedicated buildspec-teardown.yml, reusing
    # codebuild_wait_for_build(). main.tf's precondition already enforces
    # private⇒enable_codebuild at apply time, so CODEBUILD_PROJECT should be
    # populated whenever a private cluster exists — fail loudly rather than
    # silently skip cleanup (and risk orphaned, billable ALBs/NLBs) if it is not.
    if [[ -z "${CODEBUILD_PROJECT}" ]]; then
      at::fatal "endpoint_access_mode=private but no codebuild_project Terraform output — cannot reach the private API server to uninstall Helm releases or release LoadBalancer services. Investigate before running terraform destroy (orphaned ALBs/NLBs may otherwise be left running and billing)."
    fi

    at::step "Delegating on-cluster teardown to CodeBuild-in-VPC (private mode)"
    at::log "CodeBuild project : ${CODEBUILD_PROJECT}"
    at::log "Build timeout     : ${CODEBUILD_TIMEOUT} minutes"

    # Refresh the CodeBuild source.zip before starting the build, mirroring
    # the deploy path's Step 4 upload (above). Teardown can run as a
    # standalone invocation — e.g. a fresh checkout, or long after the last
    # deploy — where the CodeBuild project's S3 source is stale or was never
    # uploaded. Without this, start-build below would run against missing or
    # outdated source, and a hard CodeBuild failure here strands the cluster
    # (and its billable ALBs/NLBs) with terraform destroy never reached.
    if [[ -z "${CODEBUILD_S3_BUCKET}" ]]; then
      at::fatal "endpoint_access_mode=private but no codebuild_s3_bucket Terraform output — cannot upload source for the teardown CodeBuild run."
    fi
    at::log "Packaging source..."
    TEARDOWN_SOURCE_ZIP="/tmp/agenttier-source-$$.zip"
    zip -r "${TEARDOWN_SOURCE_ZIP}" . \
      -x '.git/*' 'terraform/*' \
         'node_modules/*' '*/node_modules/*' \
         'web-ui/dist/*' \
         '.venv/*' '*/.venv/*' \
         'bin/*' '_output/*' \
         '*.DS_Store' '*.log' \
      >/dev/null
    at::log "Uploading source to s3://${CODEBUILD_S3_BUCKET}/source.zip..."
    aws s3 cp "${TEARDOWN_SOURCE_ZIP}" \
      "s3://${CODEBUILD_S3_BUCKET}/source.zip" \
      --region "${AGENTTIER_AWS_REGION}" \
      --no-cli-pager
    rm -f "${TEARDOWN_SOURCE_ZIP}"

    TEARDOWN_BUILD_ID="$(aws codebuild start-build \
      --project-name "${CODEBUILD_PROJECT}" \
      --region "${AGENTTIER_AWS_REGION}" \
      --buildspec-override "buildspec-teardown.yml" \
      --environment-variables-override \
        "name=CLUSTER_NAME,value=${CLUSTER_NAME},type=PLAINTEXT" \
        "name=AWS_DEFAULT_REGION,value=${AGENTTIER_AWS_REGION},type=PLAINTEXT" \
        "name=AGENTTIER_HELM_RELEASE,value=${AGENTTIER_HELM_RELEASE},type=PLAINTEXT" \
        "name=AGENTTIER_NAMESPACE,value=${AGENTTIER_NAMESPACE},type=PLAINTEXT" \
      --no-cli-pager \
      --output text \
      --query 'build.id')"
    at::log "CodeBuild teardown build ID : ${TEARDOWN_BUILD_ID}"
    codebuild_wait_for_build "${TEARDOWN_BUILD_ID}" "${CODEBUILD_TIMEOUT}"
    at::log "On-cluster teardown (helm uninstall + PVC/LoadBalancer cleanup) completed inside CodeBuild-in-VPC."
  else
    # public-restricted: kubectl/helm invoked from here reach the public
    # (CIDR-restricted) endpoint directly. Refresh the kubeconfig explicitly
    # (rather than relying on a context left over from an earlier deploy in
    # the same shell) so teardown also works as a standalone invocation.
    at::step "Configuring kubectl for EKS cluster '${CLUSTER_NAME}'"
    aws eks update-kubeconfig \
      --region "${AGENTTIER_AWS_REGION}" \
      --name "${CLUSTER_NAME}" \
      --no-cli-pager

    # Step 2: Uninstall Helm release (fail loudly — orphaned pods/PVCs cause TF destroy issues).
    at::step "Uninstalling Helm release"
    if helm status "${AGENTTIER_HELM_RELEASE}" -n "${AGENTTIER_NAMESPACE}" >/dev/null 2>&1; then
      at::log "Uninstalling ${AGENTTIER_HELM_RELEASE}..."
      helm uninstall "${AGENTTIER_HELM_RELEASE}" -n "${AGENTTIER_NAMESPACE}" --wait \
        --timeout 120s
    else
      at::log "Helm release not found (already uninstalled)."
    fi

    # Step 3: Delete PVCs and LoadBalancer services, wait for LB deprovisioning.
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
  fi

  # Step 4: Terraform destroy.
  # Reuses the real-backend init from Step 1 above (no need to re-init).
  # No || true — fail loudly if destroy fails.
  at::step "Destroying Terraform infrastructure"
  cd "${TF_DIR}"
  # Pass the same three vars as the apply path (Step 5 below) for plan-output
  # cleanliness — terraform reads them from state either way (they're not
  # destroy-blocking or orphan-causing on their own, confirmed empirically
  # during T22's live e2e), but mismatched var sets between apply and destroy
  # otherwise show spurious diffs in -var-file-less `terraform plan` output.
  terraform destroy -auto-approve \
    -var="region=${AGENTTIER_AWS_REGION}" \
    -var="endpoint_access_mode=${AGENTTIER_ENDPOINT_MODE}" \
    -var="enable_codebuild=${USE_CODEBUILD}"
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
    --build-arg "VERSION=${IMAGE_TAG}" \
    --build-arg "GIT_COMMIT=${GIT_COMMIT}" \
    "${REPO_ROOT}"

  at::log "Building router image: ${ROUTER_IMG}"
  docker build -t "${ROUTER_IMG}" -f "${REPO_ROOT}/Dockerfile.router" \
    --build-arg "VERSION=${IMAGE_TAG}" \
    --build-arg "GIT_COMMIT=${GIT_COMMIT}" \
    "${REPO_ROOT}"

  at::log "Building web-ui image: ${WEBUI_IMG}"
  docker build -t "${WEBUI_IMG}" -f "${REPO_ROOT}/web-ui/Dockerfile" \
    "${REPO_ROOT}/web-ui"

  # Build all 6 sandbox images.
  # Bash-3.2-compatible: parallel indexed arrays replace declare -A maps.
  # SANDBOX_NAMES[i] → image short-name; SANDBOX_DIRS[i] → subdirectory under images/.
  SANDBOX_NAMES=(
    sandbox-general
    sandbox-claude-code
    sandbox-openclaw
    sandbox-langgraph
    sandbox-rl
    sandbox-strands-bedrock
  )
  SANDBOX_DIRS=(
    general-coding
    claude-code
    openclaw
    langgraph
    rl
    strands-bedrock
  )
  # Resolved image refs are stored in the same positional order.
  SANDBOX_IMGS=()
  for _i in 0 1 2 3 4 5; do
    sbx_name="${SANDBOX_NAMES[${_i}]}"
    sbx_dir="${SANDBOX_DIRS[${_i}]}"
    sbx_img="${REGISTRY}/${sbx_name}:${IMAGE_TAG}"
    SANDBOX_IMGS+=("${sbx_img}")
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
    for _i in 0 1 2 3 4 5; do
      kind load docker-image "${SANDBOX_IMGS[${_i}]}" \
        --name "${AGENTTIER_KIND_CLUSTER}"
    done
  else
    at::log "Loading images into minikube..."
    minikube image load "${CONTROLLER_IMG}"
    minikube image load "${ROUTER_IMG}"
    minikube image load "${WEBUI_IMG}"
    for _i in 0 1 2 3 4 5; do
      minikube image load "${SANDBOX_IMGS[${_i}]}"
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
    --set "defaults.sandbox.image=${SANDBOX_IMGS[0]}" \
    --set "defaults.claudeCode.image=${SANDBOX_IMGS[1]}" \
    --set "defaults.openclaw.image=${SANDBOX_IMGS[2]}" \
    --set "defaults.langgraph.image=${SANDBOX_IMGS[3]}" \
    --set "defaults.rl.image=${SANDBOX_IMGS[4]}" \
    --set "defaults.strandsBedrock.image=${SANDBOX_IMGS[5]}" \
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
  # install_agenttier/agenttier_chart_version/agenttier_oidc_auth/
  # agenttier_extra_values are gone from the module (D-A2) — the published-
  # chart-from-terraform path never existed as anything but an inert
  # count=0 no-op, and deploy.sh installing the SOURCE chart in Step 6/
  # buildspec-deploy.yml has always been the canonical path (D1/D20).
  at::step "Provisioning infrastructure via Terraform"
  at::log "Terraform directory: ${TF_DIR}"
  at::log "Endpoint mode      : ${AGENTTIER_ENDPOINT_MODE}"
  at::log "CodeBuild enabled  : ${USE_CODEBUILD}"
  cd "${TF_DIR}"
  terraform init -input=false
  # D1b: pass enable_codebuild so the CodeBuild project + S3 source bucket are
  # created when the CodeBuild path is selected. Without this the
  # codebuild_project output is "" and the CodeBuild branch below is dead.
  # endpoint_access_mode=private additionally requires enable_codebuild=true —
  # main.tf's precondition fails closed if that combination is ever violated
  # (already guaranteed above by the USE_CODEBUILD derivation).
  # Any extra terraform vars (e.g. create_test_user, test_user_password,
  # cluster_endpoint_public_access_cidrs) are supplied via TF_VAR_*
  # environment variables, so they are picked up here too. In
  # public-restricted mode (the default), cluster_endpoint_public_access_cidrs
  # MUST be set via TF_VAR_cluster_endpoint_public_access_cidrs — the module
  # no longer defaults it to 0.0.0.0/0 (NFR-7) and terraform fails closed
  # (validation) on an empty list.
  terraform apply -auto-approve \
    -var="region=${AGENTTIER_AWS_REGION}" \
    -var="endpoint_access_mode=${AGENTTIER_ENDPOINT_MODE}" \
    -var="enable_codebuild=${USE_CODEBUILD}"
  cd "${REPO_ROOT}"

  # Step 2: Read ECR registry and cluster info from Terraform outputs.
  # F15: stderr NOT suppressed — real terraform errors are surfaced to the caller
  # so misconfigurations (wrong state dir, missing output, etc.) are diagnosable.
  # The C5 empty-guard below catches the "output key missing / empty" case.
  at::step "Reading Terraform outputs"
  cd "${TF_DIR}"
  ECR_REGISTRY="$(terraform output -raw ecr_registry)"
  ECR_CONTROLLER_URL="$(terraform output -raw ecr_controller_url)"
  ECR_ROUTER_URL="$(terraform output -raw ecr_router_url)"
  ECR_WEBUI_URL="$(terraform output -raw ecr_webui_url)"
  ECR_SANDBOX_URL="$(terraform output -raw ecr_sandbox_general_url)"
  ECR_SANDBOX_CLAUDE_CODE_URL="$(terraform output -raw ecr_sandbox_claude_code_url)"
  ECR_SANDBOX_OPENCLAW_URL="$(terraform output -raw ecr_sandbox_openclaw_url)"
  ECR_SANDBOX_LANGGRAPH_URL="$(terraform output -raw ecr_sandbox_langgraph_url)"
  ECR_SANDBOX_RL_URL="$(terraform output -raw ecr_sandbox_rl_url)"
  ECR_SANDBOX_STRANDS_BEDROCK_URL="$(terraform output -raw ecr_sandbox_strands_bedrock_url)"
  CLUSTER_NAME="$(terraform output -raw cluster_name)"
  COGNITO_ISSUER="$(terraform output -raw cognito_issuer_url)"
  COGNITO_CLIENT_ID="$(terraform output -raw cognito_client_id)"
  COGNITO_ADMIN_GROUP="$(terraform output -raw cognito_admin_group)"
  # LBC_ROLE_ARN/VPC_ID: consumed by the Step 5 `helm upgrade` for the AWS
  # Load Balancer Controller (relocated from terraform — D-U3/D-A1) and, in
  # private mode, forwarded to the deploy-build CodeBuild run.
  LBC_ROLE_ARN="$(terraform output -raw aws_load_balancer_controller_role_arn)"
  VPC_ID="$(terraform output -raw vpc_id)"

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
                 COGNITO_ADMIN_GROUP LBC_ROLE_ARN VPC_ID; do
    if [[ -z "${!_tf_var:-}" ]]; then
      at::fatal "Terraform output for ${_tf_var} is empty — is the cluster applied and is ${TF_DIR} the correct state directory? Re-run: (cd ${TF_DIR} && terraform apply)."
    fi
  done

  # CodeBuild outputs (only meaningful when enable_codebuild=true).
  CODEBUILD_PROJECT="$(terraform output -raw codebuild_project 2>/dev/null || true)"
  CODEBUILD_S3_BUCKET="$(terraform output -raw codebuild_s3_bucket 2>/dev/null || true)"
  CODEBUILD_TIMEOUT="$(terraform output -raw codebuild_timeout_minutes 2>/dev/null || echo "30")"

  cd "${REPO_ROOT}"

  at::log "ECR registry     : ${ECR_REGISTRY}"
  at::log "EKS cluster      : ${CLUSTER_NAME}"
  at::log "Cognito issuer   : ${COGNITO_ISSUER}"

  # Step 3: Authenticate Docker to ECR.
  # Only needed for the local docker-buildx path. On the CodeBuild path there is
  # no local Docker daemon (that is the whole reason CodeBuild is used) and the
  # buildspec performs its own `aws ecr get-login-password | docker login` inside
  # the build container — so a local docker login here would fail with
  # set -euo pipefail and abort the deploy. Skip it when using CodeBuild.
  if [[ "${USE_CODEBUILD}" != "true" ]]; then
    at::step "Authenticating Docker to ECR"
    aws ecr get-login-password --region "${AGENTTIER_AWS_REGION}" --no-cli-pager \
      | docker login --username AWS --password-stdin "${ECR_REGISTRY}"
  fi

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
    #
    # Exclude VCS, terraform state, build artifacts, and dependency dirs at ANY
    # depth. Note zip's -x globs must match the full stored path: 'node_modules/*'
    # only matches a TOP-LEVEL node_modules, so nested ones (e.g.
    # web-ui/node_modules) also need '*/node_modules/*'. Shipping a host-built
    # node_modules would both bloat the upload and (via COPY . .) break the
    # in-container npm build with a platform/stale mismatch.
    at::log "Packaging source..."
    SOURCE_ZIP="/tmp/agenttier-source-$$.zip"
    zip -r "${SOURCE_ZIP}" . \
      -x '.git/*' 'terraform/*' \
         'node_modules/*' '*/node_modules/*' \
         'web-ui/dist/*' \
         '.venv/*' '*/.venv/*' \
         'bin/*' '_output/*' \
         '*.DS_Store' '*.log' \
      >/dev/null
    at::log "Uploading source to s3://${CODEBUILD_S3_BUCKET}/source.zip..."
    aws s3 cp "${SOURCE_ZIP}" \
      "s3://${CODEBUILD_S3_BUCKET}/source.zip" \
      --region "${AGENTTIER_AWS_REGION}" \
      --no-cli-pager
    rm -f "${SOURCE_ZIP}"

    # Start build.
    #
    # D1c: pass IMAGE_TAG, ECR_REPO_PREFIX, and BUILD_PLATFORM as environment
    # overrides. Without these, buildspec.yml falls back to IMAGE_TAG=sha-unknown
    # (→ CodeBuild pushes :sha-unknown while Helm Step 6 references the real
    # version.sh tag → guaranteed ImagePullBackOff) and an empty ECR_REPO_PREFIX.
    #
    # ECR_REPO_PREFIX must be the registry host + repo namespace, e.g.
    # <acct>.dkr.ecr.<region>.amazonaws.com/agenttier — NOT the bare registry host
    # (the ecr_registry output). buildspec.yml builds "${ECR_REPO_PREFIX}/controller"
    # and the ECR repos are named "<prefix>/controller", so the host alone would
    # push to a non-existent "<host>/controller" repo. Derive it by stripping the
    # image name from a full repo URL (ecr_controller_url = "<host>/<prefix>/controller").
    ECR_REPO_PREFIX="${ECR_CONTROLLER_URL%/controller}"
    at::log "Build overrides    : IMAGE_TAG=${IMAGE_TAG} ECR_REPO_PREFIX=${ECR_REPO_PREFIX} BUILD_PLATFORM=${PLATFORM}"
    BUILD_ID="$(aws codebuild start-build \
      --project-name "${CODEBUILD_PROJECT}" \
      --region "${AGENTTIER_AWS_REGION}" \
      --environment-variables-override \
        "name=IMAGE_TAG,value=${IMAGE_TAG},type=PLAINTEXT" \
        "name=ECR_REPO_PREFIX,value=${ECR_REPO_PREFIX},type=PLAINTEXT" \
        "name=BUILD_PLATFORM,value=${PLATFORM},type=PLAINTEXT" \
      --no-cli-pager \
      --output text \
      --query 'build.id')"
    at::log "CodeBuild build ID : ${BUILD_ID}"

    # Poll with bounded timeout (M6 fix — never loops forever). Shared with
    # the private-mode on-cluster deploy-build below (codebuild_wait_for_build).
    codebuild_wait_for_build "${BUILD_ID}" "${CODEBUILD_TIMEOUT}"
    at::log "Images pushed to ECR."
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
      --build-arg "VERSION=${IMAGE_TAG}" \
      --build-arg "GIT_COMMIT=${GIT_COMMIT}" \
      --push \
      "${REPO_ROOT}"

    at::log "Building + pushing router: ${ECR_ROUTER_URL}:${IMAGE_TAG}"
    docker buildx build \
      --platform "${PLATFORM}" \
      --tag "${ECR_ROUTER_URL}:${IMAGE_TAG}" \
      --file "${REPO_ROOT}/Dockerfile.router" \
      --build-arg "VERSION=${IMAGE_TAG}" \
      --build-arg "GIT_COMMIT=${GIT_COMMIT}" \
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

  # Step 5: On-cluster deploy — AWS Load Balancer Controller helm install
  # (relocated from terraform, design.md#2.3/D-U3) + AgentTier Helm release
  # (wires Cognito OIDC auth — NEVER devAuth, D8) + smoke test.
  #
  # public-restricted (default): the public endpoint (narrow CIDR allowlist,
  # var.cluster_endpoint_public_access_cidrs) is reachable from here, so all
  # three steps run locally exactly as before T8, plus the new LBC step.
  #
  # private: there is no public path to the API server at all — kubectl/helm
  # invoked from this machine cannot reach it. Delegate the identical steps
  # to a second CodeBuild run using buildspec-deploy.yml (design.md#4.2 shape
  # B), reusing the source.zip already uploaded to S3 in Step 4 (Step 4 is
  # unconditionally the CodeBuild image-build path in private mode — see the
  # USE_CODEBUILD derivation above — so CODEBUILD_PROJECT/CODEBUILD_S3_BUCKET
  # are always populated here).
  if [[ "${AGENTTIER_ENDPOINT_MODE}" == "private" ]]; then
    at::step "Delegating on-cluster deploy to CodeBuild-in-VPC (private mode)"
    at::log "CodeBuild project : ${CODEBUILD_PROJECT}"
    at::log "Build timeout     : ${CODEBUILD_TIMEOUT} minutes"

    DEPLOY_BUILD_ID="$(aws codebuild start-build \
      --project-name "${CODEBUILD_PROJECT}" \
      --region "${AGENTTIER_AWS_REGION}" \
      --buildspec-override "buildspec-deploy.yml" \
      --environment-variables-override \
        "name=CLUSTER_NAME,value=${CLUSTER_NAME},type=PLAINTEXT" \
        "name=AWS_DEFAULT_REGION,value=${AGENTTIER_AWS_REGION},type=PLAINTEXT" \
        "name=ECR_CONTROLLER_URL,value=${ECR_CONTROLLER_URL},type=PLAINTEXT" \
        "name=ECR_ROUTER_URL,value=${ECR_ROUTER_URL},type=PLAINTEXT" \
        "name=ECR_WEBUI_URL,value=${ECR_WEBUI_URL},type=PLAINTEXT" \
        "name=ECR_SANDBOX_URL,value=${ECR_SANDBOX_URL},type=PLAINTEXT" \
        "name=ECR_SANDBOX_CLAUDE_CODE_URL,value=${ECR_SANDBOX_CLAUDE_CODE_URL},type=PLAINTEXT" \
        "name=ECR_SANDBOX_OPENCLAW_URL,value=${ECR_SANDBOX_OPENCLAW_URL},type=PLAINTEXT" \
        "name=ECR_SANDBOX_LANGGRAPH_URL,value=${ECR_SANDBOX_LANGGRAPH_URL},type=PLAINTEXT" \
        "name=ECR_SANDBOX_RL_URL,value=${ECR_SANDBOX_RL_URL},type=PLAINTEXT" \
        "name=ECR_SANDBOX_STRANDS_BEDROCK_URL,value=${ECR_SANDBOX_STRANDS_BEDROCK_URL},type=PLAINTEXT" \
        "name=IMAGE_TAG,value=${IMAGE_TAG},type=PLAINTEXT" \
        "name=LBC_ROLE_ARN,value=${LBC_ROLE_ARN},type=PLAINTEXT" \
        "name=VPC_ID,value=${VPC_ID},type=PLAINTEXT" \
        "name=LBC_CHART_VERSION,value=${AGENTTIER_LBC_CHART_VERSION},type=PLAINTEXT" \
        "name=COGNITO_ISSUER,value=${COGNITO_ISSUER},type=PLAINTEXT" \
        "name=COGNITO_CLIENT_ID,value=${COGNITO_CLIENT_ID},type=PLAINTEXT" \
        "name=COGNITO_ADMIN_GROUP,value=${COGNITO_ADMIN_GROUP},type=PLAINTEXT" \
        "name=AGENTTIER_HELM_RELEASE,value=${AGENTTIER_HELM_RELEASE},type=PLAINTEXT" \
        "name=AGENTTIER_NAMESPACE,value=${AGENTTIER_NAMESPACE},type=PLAINTEXT" \
      --no-cli-pager \
      --output text \
      --query 'build.id')"
    at::log "CodeBuild deploy build ID : ${DEPLOY_BUILD_ID}"
    codebuild_wait_for_build "${DEPLOY_BUILD_ID}" "${CODEBUILD_TIMEOUT}"
    at::log "On-cluster deploy (kubeconfig + LBC + AgentTier helm + smoke test) completed inside CodeBuild-in-VPC."
  else
    # public-restricted: kubectl/helm invoked from here reach the public
    # (CIDR-restricted) endpoint directly — unchanged local execution.
    at::step "Configuring kubectl for EKS cluster '${CLUSTER_NAME}'"
    aws eks update-kubeconfig \
      --region "${AGENTTIER_AWS_REGION}" \
      --name "${CLUSTER_NAME}" \
      --no-cli-pager
    at::log "kubectl context updated. Nodes:"
    kubectl get nodes --no-headers

    # AWS Load Balancer Controller — replaces the removed terraform
    # helm_release (load_balancer_controller.tf, design.md#2.3/D-U3).
    # Idempotent (upgrade --install); mirrors the deleted resource's set{}
    # blocks exactly so the controller's config is unchanged for existing
    # deployments migrating from the terraform-managed release.
    if [[ "${AGENTTIER_INSTALL_LBC}" == "true" ]]; then
      at::step "Installing / upgrading AWS Load Balancer Controller"
      helm repo add eks-charts https://aws.github.io/eks-charts >/dev/null 2>&1 || true
      helm repo update eks-charts >/dev/null
      helm upgrade aws-load-balancer-controller eks-charts/aws-load-balancer-controller \
        --install \
        --namespace kube-system \
        --version "${AGENTTIER_LBC_CHART_VERSION}" \
        --set "clusterName=${CLUSTER_NAME}" \
        --set "region=${AGENTTIER_AWS_REGION}" \
        --set "vpcId=${VPC_ID}" \
        --set "serviceAccount.create=true" \
        --set "serviceAccount.name=aws-load-balancer-controller" \
        --set "serviceAccount.annotations.eks\.amazonaws\.com/role-arn=${LBC_ROLE_ARN}" \
        --wait \
        --timeout 180s
    else
      at::log "AWS Load Balancer Controller install skipped (AGENTTIER_INSTALL_LBC=false)."
    fi

    # Install / upgrade the AgentTier Helm chart.
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

    # Run smoke test.
    run_smoke_test
  fi

  at::log ""
  at::log "EKS deploy complete!"
  at::log ""
  at::log "Cluster         : ${CLUSTER_NAME}"
  at::log "Endpoint mode   : ${AGENTTIER_ENDPOINT_MODE}"
  at::log "Region          : ${AGENTTIER_AWS_REGION}"
  at::log "Cognito issuer  : ${COGNITO_ISSUER}"
  at::log "Cognito client  : ${COGNITO_CLIENT_ID}"
  at::log ""
  if [[ "${AGENTTIER_ENDPOINT_MODE}" == "private" ]]; then
    at::log "The API endpoint is private-only — kubectl/helm from this machine cannot"
    at::log "reach the cluster directly. Access the web UI and API server via an SSM"
    at::log "Session Manager port-forward; see docs/docs/port-forwarding.md for the"
    at::log "full runbook (includes the required tls-server-name kubeconfig setting)."
  else
    at::log "Access the web UI via the ALB (if ingress enabled) or:"
    at::log "  kubectl port-forward -n ${AGENTTIER_NAMESPACE} svc/${AGENTTIER_HELM_RELEASE}-webui 8080:80"
  fi
  at::log ""
  at::log "Tear down (WARNING: destroys all EKS resources + ECR images):"
  at::log "  ./deploy.sh --target=eks --teardown"
  at::log ""
  at::log "Estimated cost: ~\$8-10/day while cluster is running."
  exit 0
fi
