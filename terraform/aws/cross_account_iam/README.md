# Ditto Cross Account IAM

This module creates the IAM roles and policies that Ditto services need to operate inside a customer AWS account.

## Roles

### CAPA Controller Role (`controllers.cluster-api-provider-aws.sigs.k8s.io`)

Assumed by the Ditto control plane to create and manage Kubernetes cluster infrastructure (EC2 instances, security groups, load balancers, launch templates, OIDC providers, Secrets Manager secrets, and S3 buckets).

#### Policies

Two IAM policies are managed; the second is conditional:

| Policy name | Variable | Description |
|-------------|----------|-------------|
| `ditto-capa-controller-policy` | always attached | Core EC2, ELBv2, IAM SLR, Secrets Manager, S3, and OIDC permissions |
| `ditto-capa-controller-vpc-lifecycle-policy` | `customer_managed_vpc = false` | VPC, subnet, IGW, NAT gateway, and route table lifecycle |

#### Tag Conditions

All destructive and mutating operations are conditioned on resource tags to prevent the controller from affecting resources it did not create.

**Phase 1 (default):** `ditto.live/managed_by = terraform` — set by Terraform at resource creation time.

**Phase 2 (`cluster_name` variable set):** Conditions switch to cluster-specific tags:
- EC2: `kubernetes.io/cluster/<name> = owned`
- ELBv2: `elbv2.k8s.aws/cluster = <name>`

Phase 2 requires an existing deployment. Pass `--cluster-name` on a re-run after the cluster has been provisioned.

### IAM Trust Editor Role (`trust-editor.ditto.live`)

Allows the Ditto control plane to create IAM roles within the customer account. All roles created through this role must carry a mandatory permission boundary, enforced by IAM conditions on the trust editor role itself.

## Boundaries

Permission boundaries are defined in the `policies/` folder. They constrain the maximum permissions any role created by Ditto can hold.

- `cluster-resources-boundary-policy.json` — applied to roles accessed by internal cluster services
- `cluster-external-resources-boundary-policy.json` — applied to roles accessed by external cluster services; limited to Secrets Manager write operations:
  ```
  secretsmanager:CreateSecret
  secretsmanager:UpdateSecret
  secretsmanager:DeleteSecret
  secretsmanager:PutSecretValue
  ```

## Variables

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `customer_managed_vpc` | bool | `false` | When `true`, omits the VPC lifecycle policy from the CAPA controller role |
| `cluster_name` | string | `null` | When set, tightens IAM conditions to this specific cluster name |
| `controller_trusted_role_arns` | list(string) | Ditto control-plane roles | ARNs allowed to assume the CAPA controller role |
| `iam_trusted_role_arns` | list(string) | Ditto trust-editor roles | ARNs allowed to assume the IAM trust editor role |