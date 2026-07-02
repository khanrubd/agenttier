#!/usr/bin/env bash
# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0
#
# hack/bootstrap-tfstate.sh — create (or verify) the hardened S3 bucket that
# backs terraform/aws-eks/backend.tf's S3 state backend (D-U6,
# agenttier-hardening design.md#2/§10).
#
# The module's backend.tf deliberately hardcodes no bucket/key/region so the
# same module works across clusters/environments — this script creates the
# bucket you then reference from backend.hcl (copy
# terraform/aws-eks/backend.hcl.example) or -backend-config= flags.
#
# Controls applied (AWS-security-guidelines Phase 1-3), idempotent — safe to
# re-run against an existing bucket to converge its configuration:
#   1. Bucket creation (skipped if it already exists and is owned by us)
#   2. Versioning enabled (state-history recovery; required alongside the S3
#      native lockfile so a bad apply's prior state is always recoverable)
#   3. SSE-KMS default encryption (encryption at rest) with bucket-key enabled
#      to reduce KMS request cost
#   4. Block Public Access — all four settings on
#   5. Bucket policy: deny non-TLS requests (aws:SecureTransport=false)
#   6. Tag: data-classification=confidential (terraform state contains
#      resource IDs, and can contain secrets depending on what the module
#      manages — treat as confidential)
#
# Usage:
#   hack/bootstrap-tfstate.sh <bucket-name> [region]
#   AWS_PROFILE=harniva-genai hack/bootstrap-tfstate.sh agenttier-tfstate-961341538768 us-east-1
#
# If <bucket-name> is omitted, defaults to agenttier-tfstate-<account-id> in
# the region below (matching backend.hcl.example's suggested name).
#
# Requirements: aws CLI v2, credentials with s3:CreateBucket/PutBucket* and
# kms:CreateKey/DescribeKey/CreateAlias (or an existing key ARN via
# TFSTATE_KMS_KEY_ID). Non-interactive — no prompts.
set -euo pipefail

REGION="${2:-${AWS_DEFAULT_REGION:-us-east-1}}"

# ---------------------------------------------------------------------------
# Resolve account ID + default bucket name.
# ---------------------------------------------------------------------------
ACCOUNT_ID="$(aws sts get-caller-identity --region "${REGION}" --no-cli-pager --output text --query 'Account')"
if [[ -z "${ACCOUNT_ID}" ]]; then
  echo "ERROR: could not resolve AWS account ID — check AWS credentials (aws sts get-caller-identity)." >&2
  exit 1
fi

BUCKET="${1:-agenttier-tfstate-${ACCOUNT_ID}}"

echo "[bootstrap-tfstate] Bucket : ${BUCKET}"
echo "[bootstrap-tfstate] Region : ${REGION}"
echo "[bootstrap-tfstate] Account: ${ACCOUNT_ID}"

# ---------------------------------------------------------------------------
# 1. Create the bucket (idempotent: tolerate BucketAlreadyOwnedByYou).
# ---------------------------------------------------------------------------
if aws s3api head-bucket --bucket "${BUCKET}" --region "${REGION}" --no-cli-pager >/dev/null 2>&1; then
  echo "[bootstrap-tfstate] Bucket already exists — converging configuration."
else
  echo "[bootstrap-tfstate] Creating bucket..."
  if [[ "${REGION}" == "us-east-1" ]]; then
    # us-east-1 is the one region that rejects a CreateBucketConfiguration.
    aws s3api create-bucket \
      --bucket "${BUCKET}" \
      --region "${REGION}" \
      --no-cli-pager
  else
    aws s3api create-bucket \
      --bucket "${BUCKET}" \
      --region "${REGION}" \
      --create-bucket-configuration "LocationConstraint=${REGION}" \
      --no-cli-pager
  fi
fi

# ---------------------------------------------------------------------------
# 2. Versioning (Phase 3 — state-history recovery for a confidential-tagged
#    bucket per AWS-security-guidelines).
# ---------------------------------------------------------------------------
echo "[bootstrap-tfstate] Enabling versioning..."
aws s3api put-bucket-versioning \
  --bucket "${BUCKET}" \
  --versioning-configuration Status=Enabled \
  --region "${REGION}" \
  --no-cli-pager

# ---------------------------------------------------------------------------
# 3. SSE-KMS default encryption (Phase 1 — encryption at rest).
#
# Reuses an existing key if TFSTATE_KMS_KEY_ID is set (e.g. an org-managed
# key); otherwise creates (or reuses, idempotently, via the alias) a
# dedicated customer-managed key for state so the bucket never falls back to
# the account's default AWS-managed S3 key.
# ---------------------------------------------------------------------------
KMS_ALIAS="alias/agenttier-tfstate"
if [[ -n "${TFSTATE_KMS_KEY_ID:-}" ]]; then
  KMS_KEY_ID="${TFSTATE_KMS_KEY_ID}"
  echo "[bootstrap-tfstate] Using caller-supplied KMS key: ${KMS_KEY_ID}"
else
  EXISTING_KEY="$(aws kms describe-key --key-id "${KMS_ALIAS}" --region "${REGION}" --no-cli-pager --output text --query 'KeyMetadata.Arn' 2>/dev/null || true)"
  if [[ -n "${EXISTING_KEY}" ]]; then
    KMS_KEY_ID="${EXISTING_KEY}"
    echo "[bootstrap-tfstate] Reusing existing KMS key: ${KMS_KEY_ID}"
  else
    echo "[bootstrap-tfstate] Creating KMS key for terraform state encryption..."
    # KMS keys are region-scoped, and S3-SSE-KMS requires the key to be in
    # the SAME region as the bucket — without --region here, the key would
    # silently follow the caller's ambient AWS_REGION/AWS_DEFAULT_REGION
    # (e.g. a profile defaulting elsewhere) instead of this script's REGION
    # arg, and put-bucket-encryption below would fail with
    # KMS.NotFoundException. Found live during T22 e2e validation.
    KMS_KEY_ID="$(aws kms create-key --region "${REGION}" \
      --description "AgentTier terraform state encryption (${BUCKET})" \
      --tags TagKey=service,TagValue=agenttier-tfstate TagKey=data-classification,TagValue=confidential \
      --no-cli-pager \
      --output text \
      --query 'KeyMetadata.Arn')"
    aws kms create-alias --region "${REGION}" \
      --alias-name "${KMS_ALIAS}" \
      --target-key-id "${KMS_KEY_ID}" \
      --no-cli-pager
    echo "[bootstrap-tfstate] Created KMS key: ${KMS_KEY_ID} (${KMS_ALIAS})"
  fi
fi

echo "[bootstrap-tfstate] Applying SSE-KMS default encryption..."
aws s3api put-bucket-encryption \
  --bucket "${BUCKET}" \
  --server-side-encryption-configuration "{\"Rules\":[{\"ApplyServerSideEncryptionByDefault\":{\"SSEAlgorithm\":\"aws:kms\",\"KMSMasterKeyID\":\"${KMS_KEY_ID}\"},\"BucketKeyEnabled\":true}]}" \
  --region "${REGION}" \
  --no-cli-pager

# ---------------------------------------------------------------------------
# 4. Block Public Access (Phase 1 — all four settings).
# ---------------------------------------------------------------------------
echo "[bootstrap-tfstate] Blocking public access..."
aws s3api put-public-access-block \
  --bucket "${BUCKET}" \
  --public-access-block-configuration \
    BlockPublicAcls=true,IgnorePublicAcls=true,BlockPublicPolicy=true,RestrictPublicBuckets=true \
  --region "${REGION}" \
  --no-cli-pager

# ---------------------------------------------------------------------------
# 5. TLS-only bucket policy (Phase 1 — encryption in transit).
#
# Principal: "*" in a DENY statement is the AWS-recommended pattern for
# enforcing TLS for all callers — this differs from ALLOW wildcards.
# ---------------------------------------------------------------------------
echo "[bootstrap-tfstate] Applying TLS-only bucket policy..."
TLS_POLICY=$(cat <<POLICY
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DenyNonTLS",
      "Effect": "Deny",
      "Principal": "*",
      "Action": "s3:*",
      "Resource": [
        "arn:aws:s3:::${BUCKET}",
        "arn:aws:s3:::${BUCKET}/*"
      ],
      "Condition": {
        "Bool": {
          "aws:SecureTransport": "false"
        }
      }
    }
  ]
}
POLICY
)
aws s3api put-bucket-policy \
  --bucket "${BUCKET}" \
  --policy "${TLS_POLICY}" \
  --region "${REGION}" \
  --no-cli-pager

# ---------------------------------------------------------------------------
# 6. Data classification tag (Phase 2).
# ---------------------------------------------------------------------------
echo "[bootstrap-tfstate] Applying data-classification tag..."
aws s3api put-bucket-tagging \
  --bucket "${BUCKET}" \
  --tagging 'TagSet=[{Key=data-classification,Value=confidential},{Key=service,Value=agenttier-tfstate}]' \
  --region "${REGION}" \
  --no-cli-pager

echo ""
echo "[bootstrap-tfstate] Done. Bucket ready for terraform's S3 backend:"
echo ""
echo "  bucket      = \"${BUCKET}\""
echo "  key         = \"aws-eks/terraform.tfstate\""
echo "  region      = \"${REGION}\""
echo "  kms_key_id  = \"${KMS_KEY_ID}\""
echo ""
echo "IMPORTANT: the S3 backend's own encrypt=true only gets you SSE-S3"
echo "(AES256) — Terraform's S3 backend does not consult the bucket's default"
echo "encryption for its own PutObject calls. To have terraform.tfstate"
echo "actually land under the CMK above (SSE-KMS, matching this bucket's"
echo "default encryption), you MUST also set kms_key_id in backend.hcl to the"
echo "key ARN printed above."
echo ""
echo "Copy terraform/aws-eks/backend.hcl.example to backend.hcl with these"
echo "values (including kms_key_id), then run:"
echo "  terraform init -backend-config=backend.hcl"
