# Ditto Cross Account IAM

This module creates a cross account IAM role that can be assumed by a specified account.

It is intended to used to create the IAM permissions that are required for the Ditto Cloud to operate in a different account.

## Boundaries

The ability to create Roles in other accounts is controlled by locks that only allow roles to be created with 
Boundaries that are defined in the `boundary-policy.json` files in the `polices` folder.

The `cluster-resources-boundary-policy.json` file is used to define the boundaries for roles that are created to 
be only accessed by internal services from the Ditto Cluster.

The `cluster-external-resources-boundary-policy.json` file is used to define the boundaries for roles that are 
created to be accessed by external services from the Ditto Cluster. The permissions on the roles associated with the 
policy are used to securely deploy secrets to the Client Account.

External Roles will include a trust policy that allows the Ditto Cluster to assume the role, as such the permissions
are locked down to only allow the following actions:

```json
"secretsmanager:CreateSecret",
"secretsmanager:UpdateSecret",
"secretsmanager:DeleteSecret",
"secretsmanager:PutSecretValue"
```

## Unrestricted Mode

Variable to determine if the IAM role should be unrestricted. Warning this will allow Ditto to create IAM roles with any 
permissions and with no boundaries.

This variable is intended to be used internally for testing purposes only. It is defaulted to `false`.
