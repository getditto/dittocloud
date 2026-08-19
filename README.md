# Ditto Cloud

A command-line tool to bootstrap your cloud infrastructure with the necessary resources and configurations required for Ditto deployment. This tool prepares your AWS or GCP environment with the proper networking, IAM roles, and security settings needed to deploy and run Ditto services.

## Overview

The Ditto Cloud tool automates the setup of cloud infrastructure components that are prerequisites to deploy a BYOC Ditto Server. It creates the foundation layer including VPCs, IAM roles, service accounts, and security configurations.

For more information, see: https://docs.ditto.live/cloud/public-cloud/overview

### What it creates:

**AWS:**
- VPC with proper networking configuration (skipped when customer provides their own VPC)
- Cross-account IAM roles for Ditto services
- Security groups and network ACLs
- CAPA controller IAM policy scoped by Ditto-managed resource tags (tightenable to a specific cluster name on re-runs)
- (Optional) Private networking via AWS PrivateLink for secure Big Peer access

**GCP:**
- VPC network (subnets are created by CAPG during cluster deployment)
- Service accounts with appropriate IAM bindings
- Project-level IAM roles and custom roles
- Resource tagging for access control
- Optional firewall rules for secure communication
- Support for CAPG (Cluster API Provider GCP) and Crossplane

## Installation

### Download Pre-built Binaries

Download the latest release from the [releases page](https://github.com/getditto/dittocloud/releases):

```bash
# For macOS (Apple Silicon)
curl -LO https://github.com/getditto/dittocloud/releases/latest/download/dittocloud_Darwin_arm64.tar.gz
tar -xzf dittocloud_Darwin_arm64.tar.gz

# For macOS (Intel)
curl -LO https://github.com/getditto/dittocloud/releases/latest/download/dittocloud_Darwin_x86_64.tar.gz
tar -xzf dittocloud_Darwin_x86_64.tar.gz

# For Linux (x86_64)
curl -LO https://github.com/getditto/dittocloud/releases/latest/download/dittocloud_Linux_x86_64.tar.gz
tar -xzf dittocloud_Linux_x86_64.tar.gz
```

### Build from Source

```bash
git clone https://github.com/getditto/dittocloud.git
cd dittocloud
go build -o dittocloud ./cmd/dittocloud
```

## Prerequisites

### AWS Requirements
- AWS CLI configured with appropriate credentials
- An AWS profile with sufficient permissions to create:
  - VPCs, subnets, and networking resources
  - IAM roles and policies
  - Security groups
- A region with at least 3 Availability Zones

### GCP Requirements
- Google Cloud CLI (`gcloud`) installed and authenticated
- A GCP project with billing enabled
- Sufficient permissions to create:
  - VPC networks
  - Service accounts and IAM bindings
  - Custom IAM roles
  - Firewall rules (optional)
  - Project-level tags

## Usage

### Bootstrap AWS

```bash
# Interactive mode - the tool will prompt for required values
dittocloud bootstrap aws

# With command-line flags
dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2 \
  --create-vpc=true \
  --aws-vpc-name ditto-vpc \
  --aws-vpc-cidr 10.0.0.0/16
```

Kubeadm is the default. Add `--enable-eks` only when provisioning an EKS
cluster and its supporting IAM resources.

#### VPC address layout

`--aws-vpc-cidr` is a DMZ. It carries internet-facing load balancers, NAT
gateways, internal load balancers, and explicitly placed EC2 — nothing else —
and it is the only surface a peered VPC ever sees. Public subnets default to a
`/24` per availability zone and private subnets to a `/23`.

Pod, node, and database capacity comes from a separate block:

```bash
dittocloud bootstrap aws   --aws-profile my-profile   --aws-region us-west-2   --create-vpc=true   --aws-vpc-name ditto-vpc   --aws-vpc-cidr 10.214.0.0/20   --aws-vpc-secondary-cidr 100.64.0.0/16
```

The secondary CIDR must be one of the 64 `/16` blocks inside `100.64.0.0/10`
and must be unique per VPC: AWS rejects a peering connection when any
associated CIDR overlaps, secondary blocks included, regardless of routing
intent. Traffic from these subnets reaches the internet through the NAT gateway
in its own availability zone and never crosses a peering connection.

> **Upgrading an existing deployment:** every subnet CIDR is derived from
> `--aws-vpc-public-subnet-netmask` and `--aws-vpc-private-subnet-netmask`, so
> changing either renumbers live subnets and recreates the NAT gateways, nodes,
> and load balancers with them. A VPC created before this layout used a `/22`
> public and `/18` private tier. Pin those values to keep the subnets you have:
>
> ```bash
> dittocloud bootstrap aws >   --aws-vpc-cidr 10.214.0.0/16 >   --aws-vpc-public-subnet-netmask 22 >   --aws-vpc-private-subnet-netmask 18
> ```
>
> Dittocloud refuses the run before invoking Terraform when the requested
> sizing differs from the subnets that already exist, and names the flags to
> pin. Pass `--allow-subnet-renumbering` only when replacing those subnets is
> what you actually intend.

AWS multi-scope configuration, legacy conversion, registry seeding, and
backup-based rollback are documented in
[AWS Multi-Scope Configuration and Migration](docs/aws-multi-scope.md).
Scope mode leaves the open-ended Cluster API membership tag namespaces on
shared VPC resources externally managed, so one VPC can safely retain tags for
multiple clusters without enabling the optional phase-2 IAM lock-down.

#### Customer-Managed VPC

If your organisation provides an existing VPC rather than letting Ditto create one, pass `--customer-managed-vpc` together with the required `--vpc-id`. This skips VPC creation, restricts supported EC2 operations to that VPC, confines load-balancer subnet selection to the Kubernetes-role-tagged subnets discovered in that VPC, and omits VPC lifecycle permissions (create/delete VPC, subnets, internet gateways, NAT gateways) from the CAPA controller IAM role:

Before deployment, review the complete [Bring Your Own VPC requirements and
preflight checks](docs/bring-your-own-vpc.md). The customer-managed VPC must
provide compatible DNS, DHCP options, subnets, routing, capacity, and
Kubernetes subnet tags; Dittocloud does not manage those resources in this
mode.

```bash
dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2 \
  --customer-managed-vpc \
  --vpc-id vpc-09e877f9012f52241
```

To skip Terraform VPC creation while retaining VPC lifecycle permissions for
Cluster API, set `--create-vpc=false` without enabling `--customer-managed-vpc`:

```bash
dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2 \
  --create-vpc=false
```

#### Phase-2 IAM Lock-Down (Optional)

After the initial bootstrap and once Ditto has provisioned your cluster, you can re-run with `--cluster-name` to tighten the CAPA controller IAM policy to that specific cluster. Before that re-run, direct EC2 tag changes already require CAPA's role tag to exist on the resource; ownership tags cannot be assigned to an arbitrary existing resource to unlock permissions. Phase 2 additionally requires cluster-specific `kubernetes.io/cluster/<name>`, `sigs.k8s.io/cluster-api-provider-aws/cluster/<name>`, and `elbv2.k8s.aws/cluster` tags.

**Requirements:**
- The initial bootstrap must have been run first — `--cluster-name` requires an existing state file.
- Use the same working directory (or `--state` path) as the initial run.

```bash
# First run — creates the deployment with broad tag-based permissions
dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2

# Second run — lock down to a specific cluster (cluster name provided by Ditto)
dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2 \
  --cluster-name my-cluster-name
```

The second run is optional. Customers who do not re-run retain the initial broad permissions; both configurations are fully supported.

### Bootstrap GCP

```bash
# Interactive mode - the tool will prompt for required values
dittocloud bootstrap gcp

# With command-line flags
dittocloud bootstrap gcp \
  --project-id my-project-id \
  --region us-central1
```

## Private Networking (AWS PrivateLink)

> **Note**: This is a temporary stopgap solution. Future versions of Valet will natively manage private networking.

The private networking feature enables secure, private access to Big Peer deployments via AWS PrivateLink, eliminating exposure to the public internet.

### Setup Flow

1. **Bootstrap your AWS account** (if not already done):
   ```bash
   dittocloud bootstrap aws --aws-profile my-profile --aws-region us-east-2
   ```

2. **Deploy Big Peer** via Valet control plane (done by Ditto)

3. **Create VPC Endpoint Service** in your BYOC account:
   ```bash
   dittocloud private-networking endpoint-service \
     --big-peer-name my-big-peer \
     --private-dns-name private.example.com \
     --allowed-principal arn:aws:iam::123456789012:root \
     --aws-region us-east-2
   ```

   This will output domain verification details. Provide these to Ditto to set up the required TXT records.

4. **Create VPC Endpoint** in your customer account (where you want to access the Big Peer):
   ```bash
   dittocloud private-networking endpoint \
     --service-name com.amazonaws.vpce.us-east-2.vpce-svc-xxx \
     --vpc-id vpc-customer123 \
     --subnet-ids subnet-a,subnet-b,subnet-c \
     --private-dns-name private.example.com \
     --aws-region us-east-2
   ```

### Teardown

To remove private networking:

```bash
# 1. Destroy the customer VPC endpoint
dittocloud private-networking endpoint --destroy --aws-region us-east-2

# 2. Destroy the endpoint service
dittocloud private-networking endpoint-service --destroy \
  --big-peer-name my-big-peer \
  --aws-region us-east-2
```

### State Files

Private networking uses separate state files:
- `terraform-endpoint-service.tfstate` - VPC Endpoint Service
- `terraform-endpoint.tfstate` - Customer VPC Endpoint

Keep these files safe alongside your bootstrap state file.

## Output

After successful execution, the tool displays important resource information that you'll need for Ditto deployment, including:

**For AWS:**
- AWS Account ID and region
- VPC configuration details (VPC ID, subnets, CIDR blocks)

**For GCP:**
- Project ID and available zones
- VPC network details
- Service account details for control plane and worker nodes
- Custom IAM role information for CAPG, Crossplane, and Velero
- Resource tagging information for access control

The outputs are displayed in the console in JSON format for easy consumption by other tools or scripts.

## State Management

The tool uses Terraform state files to track the infrastructure it creates. By default, it looks for and creates a `terraform.tfstate` file in your current directory. You can specify a custom location using the `--state` flag.

**Important:** Keep your state file safe and backed up, as it's required for any future updates or destruction of the created resources.

### Importing Existing Resources

Use `--import-resource` to associate an existing cloud resource with an address
in the embedded Terraform configuration. The value uses `address=id` format and
the flag can be repeated to import multiple resources in order:

```bash
cp terraform.tfstate terraform.tfstate.before-import

dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2 \
  --state terraform.tfstate \
  --import-resource 'module.cross_account_iam[0].aws_iam_policy.capa_controller_network=arn:aws:iam::123456789012:policy/ditto-capa-controller-network-policy' \
  --import-resource 'module.cross_account_iam[0].aws_iam_policy.capa_control_plane_tags=arn:aws:iam::123456789012:policy/control-plane-tags.cluster-api-provider-aws.sigs.k8s.io'
```

Import mode modifies the selected state file after each successful import, then
shows a detailed Terraform plan. It never runs `terraform apply`, even when
`--dry-run` is omitted. Review the plan and rerun the same bootstrap command
without `--import-resource` when you are ready to apply any proposed changes.

AWS scope mode requires the selected state to contain a seeded scope registry
that exactly matches the reviewed scopes YAML. The CLI passes the complete
validated `deployment_scopes` object and legal account-level variables to every
import and to the post-import plan. Default-scope resources retain their legacy
addresses; non-default resources use their `scopeRef`-keyed addresses:

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file scopes.yaml \
  --state terraform.tfstate \
  --import-resource 'module.scoped_cross_account_iam["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].aws_iam_role.capa_nodes=ditto-capa-nodes-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4' \
  --import-resource 'aws_sqs_queue.scoped_karpenter_interruption["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"]=https://sqs.us-west-2.amazonaws.com/123456789012/karpenter-interruption-dsc-01k2m8g7n4p6q9r3t5v8x1y2z4'
```

For regional AWS resources, scope mode resolves the owning scope or regional
singleton and appends the AWS provider v6 `@region` import suffix. A caller may
provide an already qualified ID, but its Region must match the scopes YAML.
IAM import IDs remain unqualified because IAM is account-global.

Immediately before the first scope-mode import, Dittocloud creates a
byte-for-byte state backup and a `0600` manifest beside the selected state. A
backup failure stops before any import. Each successful import is validated to
retain the exact scope registry and is atomically persisted before the next
import; if a later import fails, the earlier successful imports remain in the
selected state and the original backup remains available for rollback.

The Terraform address must already exist in Dittocloud's embedded configuration,
and the provider-specific import ID must identify exactly one existing resource.
Pass the same provider, region, VPC, cluster, and trust configuration used by the
deployment so the post-import plan is accurate.

## Updating an Existing Deployment

Occasionally Ditto may request that you re-run the script to make necessary updates to permissions and resources.

To do so, run the same bootstrap command you used for the initial setup. Using Terraform, the script will detect the existing resources via the state file and only apply the necessary changes.

**Before you run:**

1. Ensure the `terraform.tfstate` file from the original run is in your current working directory (or use the `--state` flag to point to its location).
2. Download or build the new version of the `dittocloud` binary.

**Then run the same command as before:**

```bash
# AWS
dittocloud bootstrap aws \
  --aws-profile my-profile \
  --aws-region us-west-2 \
  --aws-vpc-name ditto-vpc \
  --aws-vpc-cidr 10.0.0.0/16

# GCP
dittocloud bootstrap gcp \
  --project-id my-project-id \
  --region us-central1
```

The tool will show a Terraform plan of what will change and prompt for confirmation before applying. For example:

```bash
Plan: 0 to add, 1 to change, 0 to destroy.

  # google_project_iam_custom_role.capg will be updated in-place
  ~ resource "google_project_iam_custom_role" "capg" {
        id          = "projects/<project-id>/roles/DittoCapg"
        name        = "projects/<project-id>/roles/DittoCapg"
      ~ permissions = [
          + "compute.disks.createSnapshot",
          + "compute.disks.get",
          + "compute.disks.setLabels",
          + "compute.snapshots.create",
          + "compute.snapshots.delete",
          + "compute.snapshots.get",
          + "compute.snapshots.list",
          + "compute.snapshots.setLabels",
          + "compute.snapshots.useReadOnly",
          + "compute.zones.get",
            # (96 unchanged elements hidden)
        ]
        # (6 unchanged attributes hidden)
    }
```

Use the `--dry-run` flag to preview changes without applying them.

**If you don't have the state file:** Terraform will treat it as a fresh deployment and attempt to recreate all resources.  If you've lost the state file, contact the Ditto team for assistance.

## Architecture

For detailed information about the infrastructure components created by this tool, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Development

### Building

```bash
# Build for current platform
go build -o dittocloud ./cmd/dittocloud

# Build for all platforms (requires GoReleaser)
goreleaser release --snapshot --clean
```

### Testing

```bash
go test ./...
```

## Contributing

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Support

For questions, issues, or support:
- Open an issue on [GitHub](https://github.com/getditto/dittocloud/issues)
- Contact the Ditto team through your support channels

## Security

If you discover a security vulnerability, please report it responsibly by contacting the Ditto security team directly rather than opening a public issue.
