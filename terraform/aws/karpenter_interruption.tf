# Karpenter spot-interruption handling: an SQS queue that receives EC2 spot
# interruption, rebalance, instance state-change, and AWS Health events via
# EventBridge. Karpenter polls this queue (settings.interruptionQueue =
# karpenter-interruption, set in cloud-infra-apps) to cordon and drain nodes before
# they are reclaimed, instead of losing them abruptly.
#
# The unsuffixed resources belong to the default scope and remain gated on that
# scope being EKS. Non-default EKS scopes use separately keyed resources.

locals {
  karpenter_interruption_events = {
    spot_interrupt = {
      name               = "KarpenterSpotInterruption"
      scoped_name_prefix = "KarpenterSpotInterruption"
      source             = "aws.ec2"
      detail_type        = "EC2 Spot Instance Interruption Warning"
    }
    rebalance = {
      name               = "KarpenterRebalanceRecommendation"
      scoped_name_prefix = "KarpenterRebalance"
      source             = "aws.ec2"
      detail_type        = "EC2 Instance Rebalance Recommendation"
    }
    instance_state_change = {
      name               = "KarpenterInstanceStateChange"
      scoped_name_prefix = "KarpenterInstanceState"
      source             = "aws.ec2"
      detail_type        = "EC2 Instance State-change Notification"
    }
    health_event = {
      name               = "KarpenterHealthEvent"
      scoped_name_prefix = "KarpenterHealth"
      source             = "aws.health"
      detail_type        = "AWS Health Event"
    }
  }

  scoped_karpenter_event_rules = {
    for event in flatten([
      for scope_ref, scope in local.non_default_eks_scopes : [
        for event_key, event in local.karpenter_interruption_events : {
          key         = "${scope_ref}/${event_key}"
          scope_ref   = scope_ref
          region      = scope.region
          name        = "${event.scoped_name_prefix}-${scope_ref}"
          source      = event.source
          detail_type = event.detail_type
        }
      ]
    ]) : event.key => event
  }

  scoped_karpenter_name_contracts = merge(
    {
      for scope_ref in keys(local.non_default_eks_scopes) : "${scope_ref}/queue" => {
        scope_ref = scope_ref
        kind      = "SQS queue"
        name      = "karpenter-interruption-${scope_ref}"
        limit     = 80
        pattern   = "^[A-Za-z0-9_-]+$"
      }
    },
    {
      for key, event in local.scoped_karpenter_event_rules : key => {
        scope_ref = event.scope_ref
        kind      = "EventBridge rule"
        name      = event.name
        limit     = 64
        pattern   = "^[A-Za-z0-9._-]+$"
      }
    },
  )
}

resource "terraform_data" "scoped_karpenter_name_validation" {
  for_each = local.scoped_karpenter_name_contracts

  input = each.value

  lifecycle {
    precondition {
      condition = (
        length(each.value.name) <= each.value.limit &&
        can(regex(each.value.pattern, each.value.name))
      )
      error_message = "scope ${each.value.scope_ref} generates ${each.value.kind} ${each.value.name} with ${length(each.value.name)} characters; the name must match ${each.value.pattern} and cannot exceed ${each.value.limit} characters."
    }
  }
}

resource "aws_sqs_queue" "karpenter_interruption" {
  count                     = local.default_enable_eks ? 1 : 0
  name                      = "karpenter-interruption"
  message_retention_seconds = 300
  sqs_managed_sse_enabled   = true
  tags                      = var.tags

  depends_on = [terraform_data.scope_registry]
}

data "aws_iam_policy_document" "karpenter_interruption" {
  count = local.default_enable_eks ? 1 : 0

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
  count     = local.default_enable_eks ? 1 : 0
  queue_url = aws_sqs_queue.karpenter_interruption[0].id
  policy    = data.aws_iam_policy_document.karpenter_interruption[0].json
}

resource "aws_cloudwatch_event_rule" "karpenter_interruption" {
  for_each = local.default_enable_eks ? local.karpenter_interruption_events : {}

  name = each.value.name
  event_pattern = jsonencode({
    source        = [each.value.source]
    "detail-type" = [each.value.detail_type]
  })
  tags = var.tags

  depends_on = [terraform_data.scope_registry]
}

resource "aws_cloudwatch_event_target" "karpenter_interruption" {
  for_each = local.default_enable_eks ? local.karpenter_interruption_events : {}

  rule = aws_cloudwatch_event_rule.karpenter_interruption[each.key].name
  arn  = aws_sqs_queue.karpenter_interruption[0].arn
}

resource "aws_sqs_queue" "scoped_karpenter_interruption" {
  for_each = local.non_default_eks_scopes

  region                    = each.value.region
  name                      = "karpenter-interruption-${each.key}"
  message_retention_seconds = 300
  sqs_managed_sse_enabled   = true
  tags = merge(
    var.tags,
    { "ditto.live/scope-ref" = each.key },
  )

  depends_on = [
    terraform_data.scope_registry,
    terraform_data.scoped_karpenter_name_validation,
  ]
}

data "aws_iam_policy_document" "scoped_karpenter_interruption" {
  for_each = local.non_default_eks_scopes

  statement {
    sid       = "EC2InterruptionPolicy"
    effect    = "Allow"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.scoped_karpenter_interruption[each.key].arn]
    principals {
      type        = "Service"
      identifiers = ["events.amazonaws.com", "sqs.amazonaws.com"]
    }
  }

  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["sqs:*"]
    resources = [aws_sqs_queue.scoped_karpenter_interruption[each.key].arn]
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

resource "aws_sqs_queue_policy" "scoped_karpenter_interruption" {
  for_each = local.non_default_eks_scopes

  region    = each.value.region
  queue_url = aws_sqs_queue.scoped_karpenter_interruption[each.key].id
  policy    = data.aws_iam_policy_document.scoped_karpenter_interruption[each.key].json
}

resource "aws_cloudwatch_event_rule" "scoped_karpenter_interruption" {
  for_each = local.scoped_karpenter_event_rules

  region = each.value.region
  name   = each.value.name
  event_pattern = jsonencode({
    source        = [each.value.source]
    "detail-type" = [each.value.detail_type]
  })
  tags = merge(
    var.tags,
    { "ditto.live/scope-ref" = each.value.scope_ref },
  )

  depends_on = [
    terraform_data.scope_registry,
    terraform_data.scoped_karpenter_name_validation,
  ]
}

resource "aws_cloudwatch_event_target" "scoped_karpenter_interruption" {
  for_each = local.scoped_karpenter_event_rules

  region = each.value.region
  rule   = aws_cloudwatch_event_rule.scoped_karpenter_interruption[each.key].name
  arn    = aws_sqs_queue.scoped_karpenter_interruption[each.value.scope_ref].arn
}
