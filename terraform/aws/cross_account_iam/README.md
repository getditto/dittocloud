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

**Phase 1 (default, `cluster_name = null`):** EC2 mutation statements carry no resource ownership conditions — broad access. `ec2:CreateTags`/`ec2:DeleteTags` are scoped by tag key namespace only (`kubernetes.io/*`, `k8s.io/*`, `sigs.k8s.io/*`, `ditto.live/*`) so the controller can tag client-owned VPC and subnet resources in BYO-VPC without being able to apply arbitrary keys. VPC lifecycle creates require `aws:RequestTag/ditto.live/managed_by = dittocloud`; deletes require `ec2:ResourceTag/ditto.live/managed_by = dittocloud`. ELBv2 conditions require the `elbv2.k8s.aws/cluster` tag to be present (any value).

**Phase 2 (`cluster_name` variable set):** Conditions switch to cluster-specific tags:
- EC2 creates: `aws:RequestTag/kubernetes.io/cluster/<name> = owned`
- EC2 mutations: `ec2:ResourceTag/kubernetes.io/cluster/<name> = owned`
- ELBv2: `elbv2.k8s.aws/cluster = <name>`

Phase 2 requires an existing deployment. Pass `--cluster-name` on a re-run after the cluster has been provisioned.

### IAM Trust Editor Role (`trust-editor.ditto.live`)

Allows the Ditto control plane to create IAM roles within the customer account. All roles created through this role must carry a mandatory permission boundary, enforced by IAM conditions on the trust editor role itself.

## Boundaries

Permission boundaries are defined in the `policies/` folder. They constrain the maximum permissions any role created by Ditto can hold.

- `cluster-resources-boundary-policy.json.tpl` — applied to roles accessed by internal cluster services; parameterised with `ec2_project_tag`. EC2 actions are explicitly enumerated: reads (Describe*/Get*) for all controllers, plus `CreateFleet`/`CreateLaunchTemplate`/`DeleteLaunchTemplate`/`RunInstances`/`TerminateInstances` for Karpenter. Additional Karpenter services: `ssm:GetParameter` (scoped to EKS/Bottlerocket AMI paths), `sqs:*` (scoped to `karpenter-*` queues), `iam:PassRole` (scoped to CAPA node roles), `eks:DescribeCluster`, `pricing:GetProducts`. `autoscaling:*` is not included — Cluster Autoscaler runs in machine deployment mode and scales via the Kubernetes API, not direct ASG calls.
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
| `vpc_id` | string | `null` | Scopes security-group creation to the selected VPC and applies `ec2:Vpc` to supported resources such as subnets, security groups, and network interfaces |
| `ec2_project_tag` | string | `"ditto"` | Value for the `ec2:ResourceTag/ditto:project` condition in the cluster resources boundary policy |
| `controller_trusted_role_arns` | list(string) | Ditto control-plane roles | ARNs allowed to assume the CAPA controller role |
| `iam_trusted_role_arns` | list(string) | Ditto trust-editor roles | ARNs allowed to assume the IAM trust editor role |

## Deployment Workflow

Use the `dittocloud bootstrap aws` CLI command to manage this module. The flags map directly to the Terraform variables above.

### Ditto-managed VPC (3-step lockdown)

**Step 1 — Initial deployment.** Ditto provisions the VPC and all IAM roles with broad phase-1 permissions.

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn>
```

**Step 2 — Cluster-scoped IAM.** After the cluster is provisioned, re-run with `--cluster-name` to switch from broad phase-1 to cluster-specific conditions. Requires the state file from Step 1.

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn> \
  --cluster-name <cluster-name>
```

**Step 3 — VPC confinement.** Add `--vpc-id` to further restrict the CAPA controller and addon role boundary. Security-group creation is authorized against the selected VPC resource, while `ec2:Vpc` is applied only to action/resource combinations that expose it, such as `RunInstances` subnet, security-group, and network-interface authorization. Launch templates, volumes, and instances remain cluster-tag scoped because AWS does not expose `ec2:Vpc` for those resource types. Use the VPC ID created in Step 1 (available in the Terraform outputs).

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn> \
  --cluster-name <cluster-name> \
  --vpc-id <vpc-id>
```

### Customer-managed VPC (2-step lockdown)

When the VPC is provided by the customer, pass `--customer-managed-vpc` to omit VPC lifecycle permissions. Pass `--vpc-id` to confine EC2 operations to the customer VPC from day one.

**Prerequisites — tag the VPC subnets before running:**

| Tag | Value | Applies to |
|-----|-------|-----------|
| `kubernetes.io/cluster/<cluster-name>` | `shared` | All subnets used by the cluster |
| `kubernetes.io/role/elb` | `1` | Public subnets (external load balancers) |
| `kubernetes.io/role/internal-elb` | `1` | Private subnets (internal load balancers) |

**Step 1 — Initial deployment.**

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn> \
  --customer-managed-vpc \
  --vpc-id <vpc-id>
```

**Step 2 — Cluster-scoped IAM.** Re-run after the cluster is provisioned.

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn> \
  --customer-managed-vpc \
  --vpc-id <vpc-id> \
  --cluster-name <cluster-name>
```

### What each step locks down

| | VPC lifecycle | EC2 scope | VPC confinement |
|---|---|---|---|
| Ditto VPC — Step 1 | Ditto-managed | broad (phase 1) | none |
| Ditto VPC — Step 2 | Ditto-managed | cluster tag (phase 2) | none |
| Ditto VPC — Step 3 | Ditto-managed | cluster tag (phase 2) | VPC resource + supported `ec2:Vpc` conditions |
| Customer VPC — Step 1 | omitted | broad (phase 1) | VPC resource + supported `ec2:Vpc` conditions |
| Customer VPC — Step 2 | omitted | cluster tag (phase 2) | VPC resource + supported `ec2:Vpc` conditions |

> **Note:** `--cluster-name` always requires an existing state file. Run the initial deployment first, then re-run to tighten. The CLI enforces this and will exit with an error if no state file is found.
