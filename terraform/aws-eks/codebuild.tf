# =============================================================================
# CodeBuild opt-in (off by default — set enable_codebuild = true to activate)
#
# Decision D6: The default build path is local Docker buildx → ECR push.
# CodeBuild is an opt-in fallback for environments without a local Docker
# daemon (e.g. fully cloud-hosted CI pipelines).
#
# When enabled, CodeBuild reads source from the S3 bucket below, runs
# buildspec.yml, and pushes images to the ECR repos in ecr.tf.
# The timeout (var.codebuild_timeout_minutes) bounds the polling loop in
# deploy.sh so a hung build never loops forever (fixes audit M6).
# =============================================================================

# ---------------------------------------------------------------------------
# S3 source bucket (CodeBuild source artifacts)
# ---------------------------------------------------------------------------

resource "aws_s3_bucket" "codebuild_source" {
  count = var.enable_codebuild ? 1 : 0

  bucket        = "${var.cluster_name}-codebuild-source-${data.aws_caller_identity.current.account_id}"
  force_destroy = true

  tags = merge(local.tags, {
    service               = "agenttier-codebuild"
    "data-classification" = "internal"
  })
}

# Encryption at rest with AWS-managed key (SSE-S3).
resource "aws_s3_bucket_server_side_encryption_configuration" "codebuild_source" {
  count  = var.enable_codebuild ? 1 : 0
  bucket = aws_s3_bucket.codebuild_source[0].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "aws:kms"
    }
    bucket_key_enabled = true
  }
}

# Block all public access.
resource "aws_s3_bucket_public_access_block" "codebuild_source" {
  count  = var.enable_codebuild ? 1 : 0
  bucket = aws_s3_bucket.codebuild_source[0].id

  block_public_acls       = true
  ignore_public_acls      = true
  block_public_policy     = true
  restrict_public_buckets = true
}

# Deny non-TLS requests (encryption in transit — AWS-security-guidelines).
resource "aws_s3_bucket_policy" "codebuild_source_tls" {
  count  = var.enable_codebuild ? 1 : 0
  bucket = aws_s3_bucket.codebuild_source[0].id

  # Depends on public-access block being applied first so the bucket policy
  # change itself does not race the block-public-policy setting.
  depends_on = [aws_s3_bucket_public_access_block.codebuild_source]

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "DenyNonTLS"
        Effect = "Deny"
        # Principal: "*" in a DENY statement is the AWS-recommended pattern for
        # enforcing TLS for all callers — this differs from ALLOW wildcards.
        Principal = "*"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
          "s3:ListBucket",
        ]
        Resource = [
          "${aws_s3_bucket.codebuild_source[0].arn}/*",
          aws_s3_bucket.codebuild_source[0].arn,
        ]
        Condition = {
          Bool = {
            "aws:SecureTransport" = "false"
          }
        }
      },
    ]
  })
}

# Access logging for the source bucket (AWS-security-guidelines Phase 2).
resource "aws_s3_bucket_logging" "codebuild_source" {
  count  = var.enable_codebuild ? 1 : 0
  bucket = aws_s3_bucket.codebuild_source[0].id

  target_bucket = aws_s3_bucket.codebuild_source[0].id
  target_prefix = "access-logs/"
}

# ---------------------------------------------------------------------------
# IAM role for CodeBuild
# ---------------------------------------------------------------------------

data "aws_iam_policy_document" "codebuild_assume" {
  count = var.enable_codebuild ? 1 : 0

  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["codebuild.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "codebuild" {
  count = var.enable_codebuild ? 1 : 0

  name               = "${var.cluster_name}-codebuild"
  assume_role_policy = data.aws_iam_policy_document.codebuild_assume[0].json

  tags = local.tags
}

data "aws_iam_policy_document" "codebuild_permissions" {
  count = var.enable_codebuild ? 1 : 0

  # ECR: authenticate and push to the four repos.
  statement {
    sid    = "ECRAuth"
    effect = "Allow"
    actions = [
      "ecr:GetAuthorizationToken",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "ECRPush"
    effect = "Allow"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:CompleteLayerUpload",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
      "ecr:GetDownloadUrlForLayer",
      "ecr:BatchGetImage",
    ]
    # All nine ECR repos that buildspec.yml pushes to — scoped to exact ARNs
    # (least privilege; no wildcard). Adding all sandbox repos here so every
    # ClusterSandboxTemplate image can be pushed via the CodeBuild opt-in path.
    resources = [
      aws_ecr_repository.controller.arn,
      aws_ecr_repository.router.arn,
      aws_ecr_repository.webui.arn,
      aws_ecr_repository.sandbox_general.arn,
      aws_ecr_repository.sandbox_claude_code.arn,
      aws_ecr_repository.sandbox_openclaw.arn,
      aws_ecr_repository.sandbox_strands_bedrock.arn,
      aws_ecr_repository.sandbox_langgraph.arn,
      aws_ecr_repository.sandbox_rl.arn,
    ]
  }

  # S3: read source zip and write logs.
  statement {
    sid    = "S3Source"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:GetObjectVersion",
    ]
    resources = ["${aws_s3_bucket.codebuild_source[0].arn}/*"]
  }

  statement {
    sid    = "S3Logs"
    effect = "Allow"
    actions = [
      "s3:PutObject",
    ]
    resources = ["${aws_s3_bucket.codebuild_source[0].arn}/build-logs/*"]
  }

  # CloudWatch Logs for build output. Reference the managed log group's ARN
  # directly (rather than rebuilding the ARN string) so the policy can never
  # desync from the group's actual name — a mismatch would silently deny
  # logs:PutLogEvents and lose all build logs.
  statement {
    sid    = "CloudWatchLogs"
    effect = "Allow"
    actions = [
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]
    resources = [
      aws_cloudwatch_log_group.codebuild[0].arn,
      "${aws_cloudwatch_log_group.codebuild[0].arn}:*",
    ]
  }
}

resource "aws_iam_role_policy" "codebuild" {
  count = var.enable_codebuild ? 1 : 0

  name   = "${var.cluster_name}-codebuild-policy"
  role   = aws_iam_role.codebuild[0].id
  policy = data.aws_iam_policy_document.codebuild_permissions[0].json
}

# ---------------------------------------------------------------------------
# CloudWatch log group for build output.
#
# Declared explicitly (rather than letting CodeBuild auto-create it on first
# build) so `terraform destroy` cleans it up — an auto-created group survives
# destroy and lingers as an orphan on every teardown. Retention bounds cost.
# ---------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "codebuild" {
  count = var.enable_codebuild ? 1 : 0

  name              = "/aws/codebuild/${var.cluster_name}-build"
  retention_in_days = 14

  tags = merge(local.tags, {
    service = "agenttier-codebuild"
  })
}

# ---------------------------------------------------------------------------
# CodeBuild project
# ---------------------------------------------------------------------------

resource "aws_codebuild_project" "agenttier" {
  count = var.enable_codebuild ? 1 : 0

  name          = "${var.cluster_name}-build"
  description   = "Builds and pushes AgentTier images to ECR (opt-in path)."
  build_timeout = var.codebuild_timeout_minutes
  service_role  = aws_iam_role.codebuild[0].arn

  source {
    type      = "S3"
    location  = "${aws_s3_bucket.codebuild_source[0].bucket}/source.zip"
    buildspec = "buildspec.yml"
  }

  artifacts {
    type = "NO_ARTIFACTS"
  }

  environment {
    compute_type                = "BUILD_GENERAL1_SMALL"
    image                       = "aws/codebuild/standard:7.0"
    type                        = "LINUX_CONTAINER"
    image_pull_credentials_type = "CODEBUILD"
    privileged_mode             = true # required for docker-in-docker builds

    # ECR_REPO_PREFIX is the name buildspec.yml actually reads (D1d). It must be
    # the registry host + repo namespace: "<account>.dkr.ecr.<region>.amazonaws.com/<prefix>".
    # buildspec builds "${ECR_REPO_PREFIX}/controller" and the ECR repos are named
    # "<prefix>/controller", so the bare registry host (local.ecr_registry) would
    # push to a non-existent "<host>/controller" repo. Include local.ecr_prefix.
    # This makes a standalone/manual build push to the correct repos. When
    # deploy.sh drives the build it passes ECR_REPO_PREFIX via
    # --environment-variables-override, which takes precedence over this default.
    environment_variable {
      name  = "ECR_REPO_PREFIX"
      value = "${local.ecr_registry}/${local.ecr_prefix}"
    }

    # IMAGE_TAG default so a manually-triggered build is deterministic rather
    # than pushing :sha-unknown. deploy.sh overrides this with the version.sh tag
    # at start-build time (override precedence: override > project env > buildspec).
    environment_variable {
      name  = "IMAGE_TAG"
      value = "manual"
    }

    # BUILD_PLATFORM default (matches the standard EKS x86-64 node group).
    # deploy.sh overrides this from AGENTTIER_EKS_PLATFORM at start-build time.
    environment_variable {
      name  = "BUILD_PLATFORM"
      value = "linux/amd64"
    }

    environment_variable {
      name  = "CLUSTER_NAME"
      value = var.cluster_name
    }

    environment_variable {
      name  = "AWS_DEFAULT_REGION"
      value = var.region
    }
  }

  logs_config {
    cloudwatch_logs {
      group_name  = aws_cloudwatch_log_group.codebuild[0].name
      stream_name = "build"
    }
  }

  tags = merge(local.tags, {
    service = "agenttier-codebuild"
  })
}
