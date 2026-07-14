# ARCHITECTURE

## Design Brief

> In order to support self-service creation of big peer deployments we need to provide some solution to guide the customer through "bootstrapping" their cloud account with all of the required resources that need to exist for cluster API and our systems to create clusters.

## Top Level

This module implements a flexible cloud infrastructure setup with the following features:

1. **Top-level selection mechanism**: Allows choosing which cloud provider to use
    (AWS, Azure, GCP, etc.) through configuration parameters.

2. **Sensible defaults**: Each provider implementation comes with carefully selected
    default configurations that follow best practices for security and performance.

3. **Variable passing**: All required variables and configuration parameters are properly
    passed through to the appropriate cloud provider-specific module, ensuring
    consistent deployment across different environments.

This architecture enables cloud provider flexibility while maintaining consistent
infrastructure configuration patterns.

## Cloud Provider Configuration

### AWS

This module creates cross-account IAM roles and an optional VPC for Ditto BYOC deployments.

#### VPC (`terraform/aws/vpc/`)

Creates a VPC with subnets across multiple availability zones. Requires a region with at least 3 AZs. The created VPC ID is passed directly to the IAM module so supported EC2 operations are confined to it from the initial deployment. The module is skipped entirely when `customer_managed_vpc = true` — in that case `vpc_id` is required, no VPC resources are created, and VPC lifecycle permissions are omitted from the CAPA controller role. Setting only `create_vpc = false` also skips Terraform VPC creation but retains lifecycle permissions so Cluster API can create the VPC.

#### Cross-Account IAM (`terraform/aws/cross_account_iam/`)

Creates the IAM roles that Ditto services assume to manage infrastructure in the customer account:

- **CAPA controller role** — assumed by the Ditto control plane to create and manage Kubernetes cluster resources (EC2 instances, security groups, load balancers, launch templates, OIDC providers, etc.)
- **IAM trust editor role** — allows Ditto to create IAM roles within the customer account, constrained by mandatory permission boundaries

##### CAPA Controller Policy Design

The CAPA controller role is granted permissions via two separate IAM policies:

| Policy | When attached | Purpose |
|--------|--------------|---------|
| `ditto-capa-controller-policy` (base) | Always | EC2, ELB, IAM, S3, and Secrets Manager permissions needed to run clusters inside any VPC |
| `ditto-capa-controller-vpc-lifecycle-policy` | `customer_managed_vpc = false` only | VPC, subnet, IGW, NAT gateway, and route table create/delete permissions |

All destructive and mutating operations in both policies are gated by resource tag conditions so the controller can only affect resources it created.

##### Two-Phase Permission Tightening

**Phase 1 (initial bootstrap):** Conditions use the `ditto.live/managed_by = terraform` tag. This is broad enough to cover all Ditto-managed resources without knowing the cluster name in advance.

**Phase 2 (optional re-run with `--cluster-name`):** Once the cluster name is known, conditions switch to cluster-specific tags:

- EC2 creates and resource mutations: `kubernetes.io/cluster/<name> = owned` (set by CAPA on every resource it provisions)
- ELBv2 creates and mutations: `elbv2.k8s.aws/cluster = <name>` (set by the AWS load balancer controller)

After a phase-2 run the controller can only affect resources belonging to that one cluster. Customers who do not perform the phase-2 re-run remain on the phase-1 conditions — both configurations are fully operational.

##### Removed Permissions

The following permissions were intentionally omitted because they are not used by the Ditto CAPA deployment model:

- **Auto Scaling Groups** — CAPI uses `MachineDeployment` objects for node scaling, not ASGs. Karpenter (when present) runs inside the customer cluster under its own IRSA role.
- **EventBridge / SQS** — No SQS queues are created; EventBridge rules are not used by CAPA in this deployment model.
