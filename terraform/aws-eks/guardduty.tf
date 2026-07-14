# GuardDuty EKS Protection (opt-in, off by default — main.tf variable
# enable_guardduty_eks_protection).
#
# Off by default because GuardDuty is frequently managed at the AWS
# Organizations level (a delegated administrator account owns the single
# org-wide detector); creating a second standalone detector here can conflict
# with that setup. Enable only when this account has no org-managed detector.
#
# Uses aws_guardduty_detector_feature (the current provider-idiomatic
# resource for AWS provider ~> 5.0) rather than the detector's deprecated
# inline `datasources` block.

resource "aws_guardduty_detector" "this" {
  count  = var.enable_guardduty_eks_protection ? 1 : 0
  enable = true

  tags = merge(local.tags, {
    service               = "agenttier-eks"
    "data-classification" = "internal"
  })
}

resource "aws_guardduty_detector_feature" "eks_audit_logs" {
  count       = var.enable_guardduty_eks_protection ? 1 : 0
  detector_id = aws_guardduty_detector.this[0].id
  name        = "EKS_AUDIT_LOGS"
  status      = "ENABLED"
}

resource "aws_guardduty_detector_feature" "eks_runtime_monitoring" {
  count       = var.enable_guardduty_eks_protection ? 1 : 0
  detector_id = aws_guardduty_detector.this[0].id
  name        = "EKS_RUNTIME_MONITORING"
  status      = "ENABLED"

  additional_configuration {
    name   = "EKS_ADDON_MANAGEMENT"
    status = "ENABLED"
  }
}
