# Karpenter spot-interruption handling: an SQS queue that receives EC2 spot
# interruption, rebalance, instance state-change, and AWS Health events via
# EventBridge. Karpenter polls this queue (settings.interruptionQueue =
# karpenter-interruption, set in cloud-infra-apps) to cordon and drain nodes before
# they are reclaimed, instead of losing them abruptly.
#
# Account-scoped and gated on enable_eks (mirrors eks_imds_defaults.tf). The queue
# name is fixed rather than per-cluster because this module runs per AWS account and
# does not know cluster names; revisit if an account ever hosts multiple EKS clusters.

locals {
  karpenter_interruption_events = {
    spot_interrupt = {
      name        = "KarpenterSpotInterruption"
      source      = "aws.ec2"
      detail_type = "EC2 Spot Instance Interruption Warning"
    }
    rebalance = {
      name        = "KarpenterRebalanceRecommendation"
      source      = "aws.ec2"
      detail_type = "EC2 Instance Rebalance Recommendation"
    }
    instance_state_change = {
      name        = "KarpenterInstanceStateChange"
      source      = "aws.ec2"
      detail_type = "EC2 Instance State-change Notification"
    }
    health_event = {
      name        = "KarpenterHealthEvent"
      source      = "aws.health"
      detail_type = "AWS Health Event"
    }
  }
}

resource "aws_sqs_queue" "karpenter_interruption" {
  count                     = var.enable_eks ? 1 : 0
  name                      = "karpenter-interruption"
  message_retention_seconds = 300
  sqs_managed_sse_enabled   = true
  tags                      = var.tags
}

data "aws_iam_policy_document" "karpenter_interruption" {
  count = var.enable_eks ? 1 : 0

  statement {
    sid       = "EC2InterruptionPolicy"
    effect    = "Allow"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.karpenter_interruption[0].arn]
    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com", "sqs.amazonaws.com"]
    }
  }

  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["sqs:*"]
    resources = [aws_sqs_queue.karpenter_interruption[0].arn]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_sqs_queue_policy" "karpenter_interruption" {
  count     = var.enable_eks ? 1 : 0
  queue_url = aws_sqs_queue.karpenter_interruption[0].id
  policy    = data.aws_iam_policy_document.karpenter_interruption[0].json
}

resource "aws_cloudwatch_event_rule" "karpenter_interruption" {
  for_each = var.enable_eks ? local.karpenter_interruption_events : {}

  name = each.value.name
  event_pattern = jsonencode({
    source        = [each.value.source]
    "detail-type" = [each.value.detail_type]
  })
  tags = var.tags
}

resource "aws_cloudwatch_event_target" "karpenter_interruption" {
  for_each = var.enable_eks ? local.karpenter_interruption_events : {}

  rule = aws_cloudwatch_event_rule.karpenter_interruption[each.key].name
  arn  = aws_sqs_queue.karpenter_interruption[0].arn
}
