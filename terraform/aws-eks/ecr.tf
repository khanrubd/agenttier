# =============================================================================
# ECR repositories
#
# Creates one ECR repository per AgentTier image. The deploy.sh --target=eks
# path builds locally with `docker buildx --platform`, pushes to these repos,
# and passes the derived tag to Helm via --set *.image.tag=<tag>.
#
# Outputs consumed by deploy.sh: ecr_registry, ecr_controller_url,
# ecr_router_url, ecr_webui_url, ecr_sandbox_general_url,
# ecr_sandbox_claude_code_url, ecr_sandbox_openclaw_url,
# ecr_sandbox_langgraph_url, ecr_sandbox_rl_url,
# ecr_sandbox_strands_bedrock_url.
# =============================================================================

resource "aws_ecr_repository" "controller" {
  name                 = "${local.ecr_prefix}/controller"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-controller"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_repository" "router" {
  name                 = "${local.ecr_prefix}/router"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-router"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_repository" "webui" {
  name                 = "${local.ecr_prefix}/web-ui"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-webui"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_repository" "sandbox_general" {
  name                 = "${local.ecr_prefix}/sandbox-general"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-sandbox"
    "data-classification" = "internal"
  })
}

# ---------------------------------------------------------------------------
# ECR lifecycle policies — keep the 10 most recent tagged images per repo to
# contain storage costs. Untagged images are expired after 1 day.
# ---------------------------------------------------------------------------

resource "aws_ecr_lifecycle_policy" "controller" {
  repository = aws_ecr_repository.controller.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_lifecycle_policy" "router" {
  repository = aws_ecr_repository.router.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_lifecycle_policy" "webui" {
  repository = aws_ecr_repository.webui.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_lifecycle_policy" "sandbox_general" {
  repository = aws_ecr_repository.sandbox_general.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

# ---------------------------------------------------------------------------
# Additional sandbox images: claude-code, openclaw, langgraph, rl,
# strands-bedrock. These back the ClusterSandboxTemplates shipped by the
# Helm chart; without them a sandbox using one of those templates enters
# ImagePullBackOff on an from-source EKS deploy.
# images/minimal is NOT referenced by any default template — omitted.
# ---------------------------------------------------------------------------

resource "aws_ecr_repository" "sandbox_claude_code" {
  name                 = "${local.ecr_prefix}/sandbox-claude-code"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-sandbox"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_lifecycle_policy" "sandbox_claude_code" {
  repository = aws_ecr_repository.sandbox_claude_code.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_repository" "sandbox_openclaw" {
  name                 = "${local.ecr_prefix}/sandbox-openclaw"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-sandbox"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_lifecycle_policy" "sandbox_openclaw" {
  repository = aws_ecr_repository.sandbox_openclaw.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_repository" "sandbox_langgraph" {
  name                 = "${local.ecr_prefix}/sandbox-langgraph"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-sandbox"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_lifecycle_policy" "sandbox_langgraph" {
  repository = aws_ecr_repository.sandbox_langgraph.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_repository" "sandbox_rl" {
  name                 = "${local.ecr_prefix}/sandbox-rl"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-sandbox"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_lifecycle_policy" "sandbox_rl" {
  repository = aws_ecr_repository.sandbox_rl.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}

resource "aws_ecr_repository" "sandbox_strands_bedrock" {
  name                 = "${local.ecr_prefix}/sandbox-strands-bedrock"
  image_tag_mutability = "MUTABLE"
  force_delete         = true

  encryption_configuration {
    encryption_type = "AES256"
  }

  image_scanning_configuration {
    scan_on_push = true
  }

  tags = merge(local.tags, {
    service               = "agenttier-sandbox"
    "data-classification" = "internal"
  })
}

resource "aws_ecr_lifecycle_policy" "sandbox_strands_bedrock" {
  repository = aws_ecr_repository.sandbox_strands_bedrock.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Expire untagged images after 1 day"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 1
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Keep latest 10 tagged images"
        selection = {
          tagStatus   = "any"
          countType   = "imageCountMoreThan"
          countNumber = 10
        }
        action = { type = "expire" }
      },
    ]
  })
}
