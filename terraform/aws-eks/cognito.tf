# AWS Cognito user pool for AgentTier OIDC authentication.
#
# AgentTier's router validates OIDC ID tokens and maps a configurable group
# claim to admin access. This provisions a user pool, an SPA app client
# (PKCE, no client secret), and an admin group whose name matches the
# `auth.oidc.adminGroup` the AgentTier chart expects. Wiring into the Helm
# release happens in agenttier.tf.

locals {
  cognito_issuer_url = "https://cognito-idp.${var.region}.amazonaws.com/${aws_cognito_user_pool.agenttier.id}"
  cognito_domain_url = "https://${aws_cognito_user_pool_domain.agenttier.domain}.auth.${var.region}.amazoncognito.com"
}

resource "aws_cognito_user_pool" "agenttier" {
  name = "${var.cluster_name}-users"

  # Auto-verify email so invited users can confirm themselves.
  auto_verified_attributes = ["email"]

  password_policy {
    minimum_length    = 8
    require_lowercase = true
    require_numbers   = true
    require_symbols   = false
    require_uppercase = true
  }

  schema {
    name                = "email"
    attribute_data_type = "String"
    required            = true
    mutable             = true
    string_attribute_constraints {
      min_length = 1
      max_length = 256
    }
  }

  admin_create_user_config {
    allow_admin_create_user_only = false
  }

  tags = local.tags
}

# Hosted-UI domain. Cognito domain prefixes must be globally unique within a
# region; override var.cognito_domain_prefix if the default is taken.
resource "aws_cognito_user_pool_domain" "agenttier" {
  domain       = var.cognito_domain_prefix != "" ? var.cognito_domain_prefix : "${var.cluster_name}-auth"
  user_pool_id = aws_cognito_user_pool.agenttier.id
}

# SPA app client (the AgentTier web UI). Public client using PKCE, so no
# client secret is generated.
resource "aws_cognito_user_pool_client" "agenttier" {
  name         = "agenttier-web"
  user_pool_id = aws_cognito_user_pool.agenttier.id

  allowed_oauth_flows                  = ["code"]
  allowed_oauth_flows_user_pool_client = true
  allowed_oauth_scopes                 = ["openid", "email", "profile"]
  supported_identity_providers         = ["COGNITO"]

  # Callback / logout URLs. Add your AgentTier URL via var.agenttier_url once
  # the load balancer hostname is known (re-apply to update).
  callback_urls = distinct(compact([
    "http://localhost:3000/callback",
    "http://localhost:8080/callback",
    var.agenttier_url != "" ? "${var.agenttier_url}/callback" : "",
  ]))

  logout_urls = distinct(compact([
    "http://localhost:3000",
    var.agenttier_url != "" ? var.agenttier_url : "",
  ]))

  access_token_validity  = 1
  id_token_validity      = 1
  refresh_token_validity = 30

  token_validity_units {
    access_token  = "hours"
    id_token      = "hours"
    refresh_token = "days"
  }

  generate_secret = false

  explicit_auth_flows = [
    "ALLOW_REFRESH_TOKEN_AUTH",
    "ALLOW_USER_SRP_AUTH",
  ]
}

# Admin group. The name matches the AgentTier chart's default
# auth.oidc.adminGroup so members are granted admin in the UI/API.
resource "aws_cognito_user_group" "admins" {
  name         = "agenttier-admins"
  user_pool_id = aws_cognito_user_pool.agenttier.id
  description  = "AgentTier administrators with full access"
}

# Optional seed admin user, handy for evaluation clusters.
resource "aws_cognito_user" "admin" {
  count        = var.create_test_user ? 1 : 0
  user_pool_id = aws_cognito_user_pool.agenttier.id
  username     = var.test_user_email

  attributes = {
    email          = var.test_user_email
    email_verified = true
  }

  temporary_password = var.test_user_password
}

resource "aws_cognito_user_in_group" "admin" {
  count        = var.create_test_user ? 1 : 0
  user_pool_id = aws_cognito_user_pool.agenttier.id
  group_name   = aws_cognito_user_group.admins.name
  username     = aws_cognito_user.admin[0].username
}
