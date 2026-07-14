# Ditto Cross Account IAM

This module creates the IAM roles and policies that Ditto services need to operate inside a customer AWS account.

## Roles

### CAPA Controller Role (`controllers.cluster-api-provider-aws.sigs.k8s.io`)

Assumed by the Ditto control plane to create and manage Kubernetes cluster infrastructure (EC2 instances, security groups, load balancers, launch templates, OIDC providers, Secrets Manager secrets, and S3 buckets).

#### Policies

Four IAM policies are managed; the VPC lifecycle policy is conditional:

| Policy name | Variable | Description |
|-------------|----------|-------------|
| `ditto-capa-controller-policy` | always attached | Core EC2, IAM SLR, Secrets Manager, S3, and OIDC permissions |
| `ditto-capa-controller-network-policy` | always attached | VPC-aware route, network-interface, and EC2 tagging permissions |
| `ditto-capa-controller-elb-policy` | always attached | ELBv2 load balancer, target group, listener, rule, target registration, and tagging permissions |
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

- `cluster-resources-boundary-policy.json.tpl` — applied to roles accessed by internal cluster services; parameterised with `ec2_project_tag`, the selected VPC ARN, and its subnet IDs. Security-group mutations require the `ditto:project` resource tag, with a VPC-scoped path for load balancer controller operations on untagged node security groups; there is no account-wide mutation allow. Load-balancer creation and `SetSubnets` require every requested subnet to be one of the selected VPC's subnets while preserving the remaining operations needed to reconcile Kubernetes Services and Ingresses. EC2 actions are explicitly enumerated: reads (Describe*/Get*) for all controllers, plus `CreateFleet`/`CreateLaunchTemplate`/`DeleteLaunchTemplate`/`RunInstances`/`TerminateInstances` for Karpenter. Additional Karpenter services: `ssm:GetParameter` (scoped to EKS/Bottlerocket AMI paths), `sqs:*` (scoped to `karpenter-*` queues), `iam:PassRole` (scoped to CAPA node roles), `eks:DescribeCluster`, `pricing:GetProducts`. `autoscaling:*` is not included — Cluster Autoscaler runs in machine deployment mode and scales via the Kubernetes API, not direct ASG calls.
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
| `customer_managed_vpc` | bool | `false` | When `true`, uses an existing VPC and omits the VPC lifecycle policy from the CAPA controller role |
| `cluster_name` | string | `null` | When set, tightens IAM conditions to this specific cluster name |
| `vpc_id` | string | `null` | Scopes security-group creation to the selected VPC and applies `ec2:Vpc` to supported resources such as subnets, security groups, and network interfaces |
| `vpc_subnet_ids` | list(string) | `[]` | Subnets in `vpc_id` that ELB load balancers may select; populated automatically by the root AWS module |
| `ec2_project_tag` | string | `"ditto"` | Value for the `ec2:ResourceTag/ditto:project` condition in the cluster resources boundary policy |
| `controller_trusted_role_arns` | list(string) | Ditto control-plane roles | ARNs allowed to assume the CAPA controller role |
| `iam_trusted_role_arns` | list(string) | Ditto trust-editor roles | ARNs allowed to assume the IAM trust editor role |

## Deployment Workflow

Use the `dittocloud bootstrap aws` CLI command to manage this module. The flags map directly to the Terraform variables above.

### Ditto-managed VPC (2-step lockdown)

**Step 1 — Initial deployment.** Terraform provisions the VPC and automatically
uses its ID to confine supported EC2 permissions. No `--vpc-id` input is needed.

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn>
```

**Step 2 — Cluster-scoped IAM.** After the cluster is provisioned, re-run with `--cluster-name` to switch from broad phase-1 to cluster-specific conditions. Requires the state file from Step 1. VPC confinement continues to use the Terraform-created VPC ID automatically.

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn> \
  --cluster-name <cluster-name>
```

### Customer-managed VPC (2-step lockdown)

When the customer provides an existing VPC, pass `--customer-managed-vpc` and the required `--vpc-id`. Terraform does not create a VPC, VPC lifecycle permissions are omitted, and supported EC2 operations are confined to the selected VPC from day one.

The root module discovers the existing VPC's public/private Kubernetes
load-balancer subnets from the prerequisite role tags and uses them to keep
load-balancer creation and subsequent `SetSubnets` calls inside that VPC.

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

### Cluster API-managed VPC

To skip Terraform VPC creation while retaining the VPC lifecycle permissions
that Cluster API needs to create one, set `create_vpc=false` without setting
`customer_managed_vpc=true`:

```sh
dittocloud bootstrap aws \
  --aws-profile <profile> \
  --controller-trusted-role-arns <capa-role-arn> \
  --iam-trusted-role-arns <trust-editor-role-arn> \
  --tf-var=create_vpc=false
```

This mode cannot be VPC-ID confined initially because the VPC does not exist
yet. VPC lifecycle permissions remain attached so Cluster API can create and
manage it.

### What each step locks down

| | VPC lifecycle | EC2 scope | VPC confinement |
|---|---|---|---|
| Ditto VPC — Step 1 | Ditto-managed | broad (phase 1) | VPC resource + supported `ec2:Vpc` conditions |
| Ditto VPC — Step 2 | Ditto-managed | cluster tag (phase 2) | VPC resource + supported `ec2:Vpc` conditions |
| Customer VPC — Step 1 | omitted | broad (phase 1) | VPC resource + supported `ec2:Vpc` conditions |
| Customer VPC — Step 2 | omitted | cluster tag (phase 2) | VPC resource + supported `ec2:Vpc` conditions |
| Cluster API VPC — initial | retained | broad (phase 1) | none until the VPC exists |

> **Note:** `--cluster-name` always requires an existing state file. Run the initial deployment first, then re-run to tighten. The CLI enforces this and will exit with an error if no state file is found.

### Exact scope of VPC confinement

When `vpc_id` is known, the policies confine route-table operations,
network-interface mutations and IPv6 assignment, security-group operations,
VPC-aware EC2 tagging, VPC-aware `RunInstances` resource contexts, and ELB
load-balancer subnet selection. The node role retains network-interface access
for the VPC CNI, and the cluster boundary retains the ELB actions required by
the AWS Load Balancer Controller to reconcile Kubernetes Services and
Ingresses.

AWS does not expose an equivalent VPC condition for every resource. Instances,
volumes, snapshots, launch templates, NAT gateways, internet gateways, and VPC
endpoints remain limited by explicit ARN type and the existing ownership or tag
conditions where supported. `CreateTargetGroup`, listener/rule operations,
target registration, and `SetSecurityGroups` also remain explicit ELB
exceptions because applying the subnet condition to them would deny valid
load-balancer-controller requests. These permissions do not make the broader
claim that every allowed API call is VPC-bound.
