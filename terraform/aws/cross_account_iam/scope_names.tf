resource "terraform_data" "scope_contract" {
  count = local.scope_enabled ? 1 : 0

  input = {
    scope_ref = var.scope_ref
    region    = var.region
  }

  lifecycle {
    precondition {
      condition     = var.region != null
      error_message = "scope ${var.scope_ref} must provide its owning AWS Region."
    }

    precondition {
      condition     = !var.create_admin_view_role
      error_message = "scope ${var.scope_ref} cannot create the shared account-wide IAM admin view role."
    }
  }
}

resource "terraform_data" "scoped_name_validation" {
  for_each = local.scoped_generated_names

  input = {
    scope_ref = var.scope_ref
    kind      = each.value.kind
    name      = each.value.name
    limit     = each.value.limit
  }

  lifecycle {
    precondition {
      condition = (
        length(each.value.name) <= each.value.limit &&
        can(regex(each.value.pattern, each.value.name))
      )
      error_message = "scope ${var.scope_ref} generates ${each.value.kind} ${each.value.name} with ${length(each.value.name)} characters; the name must match ${each.value.pattern} and cannot exceed ${each.value.limit} characters."
    }
  }
}
