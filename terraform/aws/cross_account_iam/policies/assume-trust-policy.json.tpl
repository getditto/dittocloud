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
      "Sid": "CreateOrChangeOnlyWithBoundary",
      "Effect": "Allow",
      "Action": [
          "iam:AttachRolePolicy",
          "iam:CreateRole",
          "iam:CreateRolePolicy",
          "iam:DeleteRolePolicy",
          "iam:DetachRolePolicy",
          "iam:PutRolePermissionsBoundary",
          "iam:PutRolePolicy",
          "iam:UpdateRole",
          "iam:PassRole"
      ],
      "Resource": "arn:aws:iam::${account_id}:role/dittocluster/*",
      "Condition": {
          "StringEquals": {
            "iam:PermissionsBoundary": [
                "arn:aws:iam::${account_id}:policy/ditto-cluster-resources-boundary-policy",
                "arn:aws:iam::${account_id}:policy/ditto-cluster-external-resources-boundary-policy"
            ]
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
      "Resource": "arn:aws:iam::${account_id}:role/dittocluster/*"
    },
    {
      "Effect": "Allow",
      "Action": [
          "iam:PutRolePermissionsBoundary"
      ],
      "Resource": "arn:aws:iam::${account_id}:role/dittocluster/*"
    },
    {
      "Sid": "NoBoundaryUserDelete",
      "Effect": "Deny",
      "Action": "iam:DeleteUserPermissionsBoundary",
      "Resource": "*"
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