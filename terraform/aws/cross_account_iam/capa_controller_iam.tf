/*

R O L E

*/

module "capa_controller_role" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role"
  version = "6.4.0"

  create          = true
  name            = "controllers.cluster-api-provider-aws.sigs.k8s.io"
  use_name_prefix = false
  description     = "Ditto Cross Account Infrastructure Controller"

  trust_policy_permissions = {
    TrustedRoles = {
      actions = ["sts:AssumeRole", "sts:TagSession"]
      principals = [
        {
          type        = "AWS"
          identifiers = var.controller_trusted_role_arns
        }
      ]
    }
  }

  policies = {
    capa-controller     = aws_iam_policy.capa_controller_policy.arn
    capa-controller-eks = aws_iam_policy.capa_controller_eks_policy.arn
  }
}


/*

P O L I C I E S

*/

resource "aws_iam_policy" "capa_controller_policy" {
  name   = "ditto-capa-controller-policy"
  policy = file("${path.module}/policies/capa-controller-policy.json")
  tags   = local.tags
}

# EKS permissions for the CAPA controller (managed control plane, node groups,
# addons, access entries, and the IRSA OIDC provider). Kept as a separate managed
# policy so the base kubeadm policy stays under the IAM size limit and EKS perms
# are independently versioned. Based on `clusterawsadm bootstrap iam print-policy
# --document AWSIAMManagedPolicyControllersEKS`, plus the access-entry and
# OpenIDConnectProvider statements CAPA >= v2.10 needs that clusterawsadm omits.
resource "aws_iam_policy" "capa_controller_eks_policy" {
  name   = "ditto-capa-controller-eks-policy"
  policy = file("${path.module}/policies/capa-controller-eks-policy.json")
  tags   = local.tags
}
