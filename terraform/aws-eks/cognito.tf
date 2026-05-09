# AWS Cognito User Pool for AgentLoft OIDC Authentication

resource "aws_cognito_user_pool" "agentloft" {
  name = "${var.cluster_name}-users"

  # Password policy
  password_policy {
    minimum_length    = 8
    require_lowercase = true
    require_numbers   = true
    require_symbols   = false
    require_uppercase = true
  }

  # Auto-verify email
  auto_verified_attributes = ["email"]

  # Schema: email is required
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

  # Admin create user config
  admin_create_user_config {
    allow_admin_create_user_only = false
  }

  tags = {
    Environment = "evaluation"
    Project     = "agentloft"
  }
}

# User Pool Domain (for hosted UI)
resource "aws_cognito_user_pool_domain" "agentloft" {
  domain       = "${var.cluster_name}-auth"
  user_pool_id = aws_cognito_user_pool.agentloft.id
}

# App Client for AgentLoft
resource "aws_cognito_user_pool_client" "agentloft" {
  name         = "agentloft-web"
  user_pool_id = aws_cognito_user_pool.agentloft.id

  # OAuth settings
  allowed_oauth_flows                  = ["code"]
  allowed_oauth_flows_user_pool_client = true
  allowed_oauth_scopes                 = ["openid", "email", "profile"]
  supported_identity_providers         = ["COGNITO"]

  # Callback URLs (update with actual domain after deployment)
  callback_urls = [
    "http://localhost:3000/callback",
    "http://localhost:8080/callback",
    var.agentloft_url != "" ? "${var.agentloft_url}/callback" : "http://localhost:3000/callback",
  ]

  logout_urls = [
    "http://localhost:3000",
    var.agentloft_url != "" ? var.agentloft_url : "http://localhost:3000",
  ]

  # Token validity
  access_token_validity  = 1  # hours
  id_token_validity      = 1  # hours
  refresh_token_validity = 30 # days

  token_validity_units {
    access_token  = "hours"
    id_token      = "hours"
    refresh_token = "days"
  }

  # Generate client secret
  generate_secret = false # SPA clients should not use client secrets (PKCE instead)

  # Enable PKCE
  explicit_auth_flows = [
    "ALLOW_REFRESH_TOKEN_AUTH",
    "ALLOW_USER_SRP_AUTH",
  ]
}

# Admin group
resource "aws_cognito_user_group" "admins" {
  name         = "agentloft-admins"
  user_pool_id = aws_cognito_user_pool.agentloft.id
  description  = "AgentLoft administrators with full access"
}

# Create a test admin user
resource "aws_cognito_user" "admin" {
  count        = var.create_test_user ? 1 : 0
  user_pool_id = aws_cognito_user_pool.agentloft.id
  username     = var.test_user_email

  attributes = {
    email          = var.test_user_email
    email_verified = true
  }

  temporary_password = var.test_user_password
}

# Add test user to admin group
resource "aws_cognito_user_in_group" "admin" {
  count        = var.create_test_user ? 1 : 0
  user_pool_id = aws_cognito_user_pool.agentloft.id
  group_name   = aws_cognito_user_group.admins.name
  username     = aws_cognito_user.admin[0].username
}

# --- Outputs ---

output "cognito_user_pool_id" {
  value = aws_cognito_user_pool.agentloft.id
}

output "cognito_client_id" {
  value = aws_cognito_user_pool_client.agentloft.id
}

output "cognito_issuer_url" {
  value = "https://cognito-idp.${var.region}.amazonaws.com/${aws_cognito_user_pool.agentloft.id}"
}

output "cognito_domain" {
  value = "https://${aws_cognito_user_pool_domain.agentloft.domain}.auth.${var.region}.amazoncognito.com"
}

output "helm_auth_values" {
  value = <<-EOT
    auth:
      oidc:
        issuerUrl: "https://cognito-idp.${var.region}.amazonaws.com/${aws_cognito_user_pool.agentloft.id}"
        clientId: "${aws_cognito_user_pool_client.agentloft.id}"
        adminGroup: "agentloft-admins"
        groupClaim: "cognito:groups"
  EOT
}
