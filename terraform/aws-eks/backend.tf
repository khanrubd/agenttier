# Terraform state backend: S3 with the native S3 lockfile (D-U6).
#
# Deliberate departure from the module's previous "no backend block" stance
# (kept the module drop-in for a pure laptop eval). A shared backend is now
# required because the private-mode CI/deploy actor (CodeBuild-in-VPC) and a
# human's laptop both need to read/write the *same* state — local state can't
# be shared between them. No DynamoDB lock table: `use_lockfile = true` uses a
# native S3 lock object instead (requires Terraform >= 1.10, see versions.tf).
#
# Bucket/key/region/kms_key_id are intentionally NOT hardcoded here — every
# value below must be supplied at `terraform init` time so the same module
# code works across multiple clusters/environments without editing this file:
#
#   terraform init -backend-config=backend.hcl
#
# Copy backend.hcl.example to backend.hcl (git-ignored — it names a specific,
# often account-specific bucket) and fill in the values, or pass
# -backend-config="bucket=..." flags individually. Use
# scripts/bootstrap-tfstate.sh to create a correctly hardened bucket first
# (versioned, SSE-KMS, Block Public Access, TLS-only policy, tagged
# data-classification=confidential) — it prints the kms_key_id to use below.
#
# encrypt=true alone only buys SSE-S3 (AES256): Terraform's S3 backend sets
# ServerSideEncryption=AES256 explicitly on every state PutObject regardless
# of the bucket's own default encryption, so the bucket's SSE-KMS default
# (from bootstrap-tfstate.sh) is silently overridden unless kms_key_id is
# ALSO supplied via -backend-config — that's what switches these PutObject
# calls to SSE-KMS using the named CMK. Supply it in backend.hcl
# (backend.hcl.example documents the exact key). Omitting it is not a
# failure (state stays AES256-encrypted), just a downgrade from the
# CMK/BYOK the bootstrap script provisions.
#
# For a quick local-only eval with no shared state, `terraform init
# -backend=false` still works and falls back to local state — the module
# stays usable without ever creating the bucket.

terraform {
  backend "s3" {
    encrypt      = true
    use_lockfile = true
  }
}
