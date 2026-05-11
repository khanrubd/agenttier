#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# AgentTier Quickstart Script
# Deploys a complete AgentTier environment on AWS (EKS + Cognito + ECR).
#
# Prerequisites:
#   - AWS CLI configured with admin credentials
#   - Terraform >= 1.5
#   - kubectl
#   - Go 1.22+ (for local builds) OR CodeBuild (for remote builds)
#   - Helm 3.x
#
# Usage:
#   ./hack/quickstart.sh          # Full deployment
#   ./hack/quickstart.sh destroy  # Tear down everything
#
# Cost: ~$8-10/day while running. Always run `destroy` when done.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
TF_DIR="${ROOT_DIR}/terraform/aws-eks"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log() { echo -e "${GREEN}[AgentTier]${NC} $1"; }
warn() { echo -e "${YELLOW}[AgentTier]${NC} $1"; }
error() { echo -e "${RED}[AgentTier]${NC} $1" >&2; }

# --- Destroy mode ---
if [[ "${1:-}" == "destroy" ]]; then
  log "Tearing down AgentTier infrastructure..."
  cd "$TF_DIR"

  # Uninstall Helm release first
  if kubectl get namespace agenttier &>/dev/null; then
    log "Uninstalling Helm release..."
    helm uninstall agenttier -n agenttier 2>/dev/null || true
    kubectl delete namespace agenttier 2>/dev/null || true
  fi

  # Delete all sandboxes
  kubectl delete sandboxes --all --all-namespaces 2>/dev/null || true

  # Destroy infrastructure
  log "Destroying Terraform resources..."
  terraform destroy -auto-approve

  log "Teardown complete!"
  exit 0
fi

# --- Deploy mode ---
log "Starting AgentTier deployment..."
log "This will take approximately 15-20 minutes."
echo ""

# Step 1: Check prerequisites
log "Checking prerequisites..."
command -v aws >/dev/null 2>&1 || { error "AWS CLI not found. Install: https://aws.amazon.com/cli/"; exit 1; }
command -v terraform >/dev/null 2>&1 || { error "Terraform not found. Install: https://terraform.io/downloads"; exit 1; }
command -v kubectl >/dev/null 2>&1 || { error "kubectl not found. Install: https://kubernetes.io/docs/tasks/tools/"; exit 1; }
command -v helm >/dev/null 2>&1 || { error "Helm not found. Install: https://helm.sh/docs/intro/install/"; exit 1; }

# Verify AWS credentials
aws sts get-caller-identity >/dev/null 2>&1 || { error "AWS credentials not configured. Run: aws configure"; exit 1; }
AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
AWS_REGION=$(aws configure get region 2>/dev/null || echo "us-east-1")
log "AWS Account: ${AWS_ACCOUNT_ID}, Region: ${AWS_REGION}"

# Step 2: Provision infrastructure
log "Provisioning EKS cluster + Cognito + ECR (this takes ~12 minutes)..."
cd "$TF_DIR"
terraform init -input=false
terraform apply -auto-approve -var="region=${AWS_REGION}"

# Extract outputs
CLUSTER_NAME=$(terraform output -raw cluster_name)
ECR_REGISTRY=$(terraform output -raw ecr_registry)
CODEBUILD_PROJECT=$(terraform output -raw codebuild_project)
S3_BUCKET=$(terraform output -raw codebuild_s3_bucket)
COGNITO_ISSUER=$(terraform output -raw cognito_issuer_url)
COGNITO_CLIENT_ID=$(terraform output -raw cognito_client_id)

# Step 3: Configure kubectl
log "Configuring kubectl..."
aws eks update-kubeconfig --region "${AWS_REGION}" --name "${CLUSTER_NAME}"
kubectl get nodes

# Step 4: Create gp3 StorageClass
log "Creating gp3 StorageClass..."
kubectl apply -f - <<EOF
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: gp3
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: ebs.csi.aws.com
parameters:
  type: gp3
volumeBindingMode: WaitForFirstConsumer
allowVolumeExpansion: true
EOF

# Step 5: Build and push Docker images via CodeBuild
log "Building Docker images via CodeBuild..."
cd "$ROOT_DIR"

# Upload source to S3
zip -r /tmp/agenttier-source.zip . -x '.kiro/*' '.git/*' 'terraform/*' 'node_modules/*' '*.DS_Store' >/dev/null
aws s3 cp /tmp/agenttier-source.zip "s3://${S3_BUCKET}/source.zip" --region "${AWS_REGION}"
rm /tmp/agenttier-source.zip

# Start build
BUILD_ID=$(aws codebuild start-build --project-name "${CODEBUILD_PROJECT}" --region "${AWS_REGION}" --query 'build.id' --output text)
log "CodeBuild started: ${BUILD_ID}"
log "Waiting for image build (~5 minutes)..."

# Wait for build to complete
while true; do
  STATUS=$(aws codebuild batch-get-builds --ids "${BUILD_ID}" --region "${AWS_REGION}" --query 'builds[0].buildStatus' --output text)
  if [[ "$STATUS" == "SUCCEEDED" ]]; then
    log "Images built and pushed to ECR!"
    break
  elif [[ "$STATUS" == "FAILED" || "$STATUS" == "FAULT" || "$STATUS" == "STOPPED" ]]; then
    error "CodeBuild failed with status: ${STATUS}"
    error "Check logs: aws codebuild batch-get-builds --ids ${BUILD_ID} --region ${AWS_REGION}"
    exit 1
  fi
  sleep 15
done

# Step 6: Install CRDs
log "Installing CRDs..."
kubectl apply -f "${ROOT_DIR}/config/crd/"

# Step 7: Deploy AgentTier via Helm
log "Deploying AgentTier..."
helm install agenttier "${ROOT_DIR}/helm/agenttier/" \
  --namespace agenttier --create-namespace \
  --set controller.image.repository="${ECR_REGISTRY}/agenttier/controller" \
  --set controller.image.tag=v0.1.0 \
  --set router.image.repository="${ECR_REGISTRY}/agenttier/router" \
  --set router.image.tag=v0.1.0 \
  --set defaults.sandbox.image="${ECR_REGISTRY}/agenttier/sandbox-general:latest" \
  --set auth.oidc.issuerUrl="" \
  --wait --timeout=120s

# Step 8: Verify deployment
log "Verifying deployment..."
kubectl get pods -n agenttier
kubectl get clustersandboxtemplates

# Step 9: Create a test sandbox
log "Creating test sandbox..."
kubectl apply -f - <<EOF
apiVersion: agenttier.io/v1alpha1
kind: Sandbox
metadata:
  name: quickstart-sandbox
  namespace: default
spec:
  templateRef:
    name: general-coding
    kind: ClusterSandboxTemplate
  image:
    repository: ${ECR_REGISTRY}/agenttier/sandbox-general:latest
EOF

log "Waiting for sandbox to be ready..."
sleep 30
PHASE=$(kubectl get sandbox quickstart-sandbox -o jsonpath='{.status.phase}')
if [[ "$PHASE" == "Running" ]]; then
  log "Sandbox is running!"
else
  warn "Sandbox status: ${PHASE} (may still be starting)"
fi

echo ""
echo "=============================================="
echo -e "${GREEN}AgentTier deployment complete!${NC}"
echo "=============================================="
echo ""
echo "Cluster:     ${CLUSTER_NAME}"
echo "Region:      ${AWS_REGION}"
echo "Cognito:     ${COGNITO_ISSUER}"
echo "Client ID:   ${COGNITO_CLIENT_ID}"
echo ""
echo "Quick commands:"
echo "  kubectl get sandboxes                    # List sandboxes"
echo "  kubectl exec -it quickstart-sandbox-pod -c sandbox -- /bin/bash  # Open terminal"
echo "  kubectl delete sandbox quickstart-sandbox  # Delete sandbox"
echo ""
echo "To tear down everything:"
echo "  ./hack/quickstart.sh destroy"
echo ""
echo "Estimated cost: ~\$8-10/day. Remember to destroy when done!"
