{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "iam:GetRole",
        "iam:GetRolePolicy",
        "iam:ListRolePolicies",
        "iam:ListAttachedRolePolicies",
        "iam:ListInstanceProfilesForRole"
      ],
      "Resource": "*"
    },
    {
      "Sid": "CreateOrManageOnlyWithBoundary",
      "Effect": "Allow",
      "Action": [
          "iam:AttachRolePolicy",
          "iam:CreateRole",
          "iam:CreateRolePolicy",
          "iam:DeleteRolePolicy",
          "iam:DetachRolePolicy",
          "iam:PutRolePolicy",
          "iam:UpdateRole",
          "iam:PassRole"
      ],
      "Resource": ${jsonencode(managed_role_arn)},
      "Condition": {
          "StringEquals": {
            "iam:PermissionsBoundary": ${jsonencode(boundary_policy_arns)}
          }
      }
    },
    {
      "Sid": "PathOnly",
      "Effect": "Allow",
      "Action": [
          "iam:DeleteRole",
          "iam:TagRole",
          "iam:UpdateAssumeRolePolicy"
      ],
      "Resource": ${jsonencode(managed_role_arn)}
    },
    {
      "Sid": "DenyRoleBoundaryReplacementOrRemoval",
      "Effect": "Deny",
      "Action": [
          "iam:DeleteRolePermissionsBoundary",
          "iam:PutRolePermissionsBoundary"
      ],
      "Resource": ${jsonencode(managed_role_arn)}
    },
    {
      "Sid": "S3Permissions",
      "Effect": "Allow",
      "Action": [
          "s3:*"
      ],
      "Resource": [
          "arn:aws:s3:::ditto-*",
          "arn:aws:s3:::ditto-*/*"
      ]
    }
  ]
}
