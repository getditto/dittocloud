# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Go CLI tool that bootstraps cloud infrastructure (AWS and GCP) for Ditto deployment. The tool wraps embedded Terraform modules and provides an interactive CLI experience using Cobra.

## Important rules of conduct you should never violate.

1. NEVER RUN THIS COMMAND LOCALLY.  This command makes modifications to AWS and GCP accounts and is too dangerous to run locally. Instead, you should only run the tests.

## Common Commands

### Build
```bash
go build -o dittocloud ./cmd/dittocloud
```

### Test
```bash
go test ./...
```

## Architecture

### Code Structure

**CLI Layer** (`cmd/dittocloud/` and `cmd/internal/`):
- `main.go`: Root Cobra command setup
- `bootstrap/`: Core bootstrap command with shared logic for all cloud providers
  - `bootstrap.go`: Handles Terraform lifecycle: init → plan → apply
  - Manages state file copying between local and temp directories
  - Implements interactive prompts and confirmation flows
  - Provider-agnostic orchestration
  - `aws.go`: AWS-specific variable prompting and flag definitions
  - `gcp.go`: GCP-specific variable prompting and flag definitions
  - `install.go`: Terraform version management (downloads v1.15.2 if needed, caches in `~/.cache/dittocloud/terraform/`)
- `privatenetworking/`: Private networking commands for Big Peer PrivateLink access (temporary stopgap solution)
  - `private_networking.go`: Parent command, endpoint-service, and endpoint commands

**Terraform Layer** (`terraform/`):
- `embed.go`: Embeds all Terraform files into the binary using `//go:embed`
- `aws/`: AWS VPC and cross-account IAM role modules
  - `vpc/`: VPC with subnets across availability zones
  - `cross_account_iam/`: CAPA controller, control plane, and node IAM roles
  - `private_networking/`: Temporary stopgap for Big Peer PrivateLink access
    - `vpc_endpoint_service/`: VPC Endpoint Service in BYOC account (binds to NLB)
    - `vpc_endpoint/`: VPC Endpoint in customer account (connects to endpoint service)
- `gcp/`: GCP networking, service accounts, and custom IAM roles
  - VPC network (subnets are created by CAPG during cluster deployment)
  - Optional firewall rules
  - Tag-based IAM access control (uses resource tags to limit IAM permissions)
  - Service accounts for CAPG control plane and worker nodes
  - Custom roles for CAPG, Crossplane, and Velero

### Key Patterns

**Variable Flow**: User flags/prompts → `[]*tfexec.VarOption` slice → Terraform plan/apply

**State Management**: Local `terraform.tfstate` is copied to temp dir, modified during apply, then copied back

**Terraform Lifecycle**:
1. Copy embedded Terraform files to temp directory
2. Copy existing state file (if exists) to temp directory
3. Run `terraform init` and `terraform plan`
4. Prompt user for confirmation (unless `--dry-run`)
5. Run `terraform apply`
6. Copy state file back to local directory
7. Display outputs as JSON

**Interactive Prompts**: The `FlagOrPrompt()` helper checks if a flag was set; if not, prompts the user interactively

**Terraform Version**: Tool requires Terraform 1.15.2 (checks system, downloads if needed, caches for reuse). Must match the version pinned in `.mise/config.toml` and `cmd/internal/bootstrap/install.go`.

## GCP-Specific Details

**Tag-based IAM Access Control**: The GCP module uses resource tags to restrict IAM permissions. All resources created by Ditto are tagged, and IAM roles include conditions that check for these tags. This prevents service accounts from accessing resources outside Ditto's management scope.

**VPC Configuration**: The GCP module creates a VPC network without subnets. CAPG (Cluster API Provider GCP) creates subnets with appropriate CIDR ranges and secondary IP ranges for Kubernetes pods and services during cluster deployment.

**Firewall Rules Module**: Optional firewall rules can be created using the `--create-default-firewall-rules` flag. When enabled, rules are created in a separate module to avoid conditional type errors.

## AWS-Specific Details

**Cross-Account IAM**: The AWS module creates IAM roles that can be assumed by Ditto services running in a different AWS account (via trusted role ARNs).

**VPC Module**: Creates a VPC with subnets across multiple availability zones (requires region with at least 3 AZs). Omitted when `--customer-managed-vpc` is passed.

**DMZ + Workload Split**: The primary CIDR (`vpc_cidr`) is a DMZ carrying only load balancers, NAT gateways, and explicitly placed EC2 — it is the only surface a peered VPC ever sees. Public subnets default to `/24` and private to `/23`, each allocated from its own aligned block so resizing one tier never renumbers another (`terraform/aws/vpc/layout.tf`). Setting `vpc_secondary_cidr` adds a second CIDR out of `100.64.0.0/10` carrying every workload tier: pod `/18`, node `/22`, and database `/22` per AZ, plus one spare block per tier and a reserved `/19`. `100.66.0.0/16` is rejected because Valet clusters use it for in-cluster pod and Service addressing.

**Subnet Renumbering**: Subnet CIDRs are derived from `public_subnet_netmask` and `private_subnet_netmask`, so changing either renumbers live subnets and recreates the NAT gateways, nodes, and load balancers with them. A deployment created before the DMZ split must pin `public_subnet_netmask = 22` and `private_subnet_netmask = 18`; against a `/16` primary that reproduces the previous allocation exactly. `bootstrap aws scopes generate` reads the sizing back from the subnets already in state, and a schema 1 configuration snapshot recovers with those legacy values pinned.

**Workload Routing**: Pod and node subnets share one route table per AZ (`<vpc>-workload-<az>`) carrying `local` plus a default route to that AZ's NAT gateway. Database subnets get their own per-AZ tables. None of them carry peering routes — peering attaches to the private DMZ only, and a workload initiating a connection to a peered VPC from a `100.64` address fails on the return path. That case needs a terminating proxy in the DMZ and is unsupported.

**VPC CNI Custom Networking**: Pods only land in the pod subnet when the cluster sets `AWS_VPC_K8S_CNI_CUSTOM_NETWORK_CFG=true`, one `ENIConfig` per AZ, and `ENI_CONFIG_LABEL_DEF=topology.kubernetes.io/zone`. Without all three, VPC CNI places pods in the node subnet and the pod tier goes unused. The `aws_workload_networking` output publishes the per-AZ pod subnets for generating those `ENIConfig` resources; that cluster configuration lives in `valet-cluster`, not here.

**Subnet Tags**: Tags are the placement mechanism. `kubernetes.io/role/elb` on public DMZ, `kubernetes.io/role/internal-elb` on private DMZ, `karpenter.sh/discovery` on node subnets. Terraform owns the Karpenter tag because the CAPA controller boundary does not permit the `karpenter.sh` namespace (`ec2_tag_keys_cond` in `cross_account_iam/locals.tf`). A mis-tagged subnet silently puts nodes in the DMZ or load balancers in the node subnet.

**NAT Addresses**: The module owns the NAT Elastic IPs (`terraform/aws/vpc/nat.tf`) rather than letting the upstream module create them, so egress addresses are stable and readable as an output. `moved` blocks adopt the addresses the upstream module created previously. Pass `nat_gateway_eip_allocation_ids` to use addresses allocated out of band, which is what keeps them fixed across a VPC rebuild.

**CAPA Controller Policy Split**: The CAPA controller role is granted permissions via two separate IAM policies. The base policy (`ditto-capa-controller-policy`) is always attached. The VPC lifecycle policy (`ditto-capa-controller-vpc-lifecycle-policy`) is only attached when `customer_managed_vpc = false`.

**Two-Phase IAM Tightening**: Phase 1 (default, no `--cluster-name`) is condition-free for EC2 resource mutations — broad access needed because existing CAPA resources may not carry `ditto.live/managed_by`. `ec2:CreateTags`/`ec2:DeleteTags` are scoped by tag key namespace (`kubernetes.io/*`, `k8s.io/*`, `sigs.k8s.io/*`, `ditto.live/*`) to support BYO-VPC tagging without allowing arbitrary tag writes on client resources. VPC lifecycle creates/deletes are gated on `ditto.live/managed_by = dittocloud`. Phase 2 (`--cluster-name` set) switches EC2 conditions to cluster-specific `kubernetes.io/cluster/<name>` tags; ELBv2 conditions use `elbv2.k8s.aws/cluster = <name>`. The second run requires an existing state file — the CLI enforces this with an early error before any Terraform runs.

**AWS CLI Flags** (`bootstrap aws`):
- `--aws-profile` — AWS profile
- `--aws-region` — AWS region (default `us-east-1`)
- `--aws-vpc-name` — VPC name (default `ditto`)
- `--aws-vpc-cidr` — primary (DMZ) VPC CIDR block (default `10.210.0.0/16`)
- `--aws-vpc-secondary-cidr` — secondary CIDR carrying pod, node, and database capacity; a `/16` inside `100.64.0.0/10`, unique per VPC
- `--aws-vpc-public-subnet-netmask` — per-AZ public subnet netmask (default `24`); pin to the existing value or subnets renumber
- `--aws-vpc-private-subnet-netmask` — per-AZ private subnet netmask (default `23`); pin to the existing value or subnets renumber
- `--aws-vpc-nat-eip-allocation-ids` — pre-allocated Elastic IP allocation IDs for the NAT gateways, one per AZ (repeatable)
- `--karpenter-discovery-tag-value` — value for `karpenter.sh/discovery` on the node subnets; defaults to `--cluster-name`
- `--controller-trusted-role-arns` — override CAPA controller trusted ARNs
- `--iam-trusted-role-arns` — override trust editor trusted ARNs
- `--customer-managed-vpc` — omit VPC creation and VPC lifecycle IAM permissions
- `--vpc-id` — restrict CAPA EC2 create/mutate operations to a specific VPC via `ec2:Vpc` condition; can be combined with either phase; mutually exclusive with `--aws-vpc-name`/`--aws-vpc-cidr`
- `--cluster-name` — phase-2 lock-down; requires existing state file

## Testing

Only one test file exists: `cmd/internal/bootstrap/install_test.go` for Terraform installation logic.

### Testing Patterns

**Test Structure**:
- Use `t.Run()` for subtests with descriptive names that explain the scenario
- Follow setup → execute → verify pattern
- Initialize context with logger: `ctx := log.WithLogger(context.Background(), log.Setup("debug"))`

**Environment Isolation**:
- Create helper functions that return structs with cleanup functions
- `setupCleanEnvironment(t)` pattern:
  - Save original environment variables (e.g., PATH)
  - Clear or modify environment to isolate test
  - Use `t.TempDir()` for temporary directories
  - Return struct with `cleanup func()` field
  - Always call `defer cleanup()` immediately after setup

**Helper Functions**:
- Create reusable helpers for common setup tasks (e.g., `installSystemTerraform()`)
- Helpers should return structs with cleanup functions, not just values
- Example struct pattern:
  ```go
  type setupResult struct {
      field1  string
      cleanup func()
  }
  ```

**Verification Pattern**:
1. Check for errors: `if err != nil { t.Fatalf("unexpected error: %v", err) }`
2. Check for empty/nil results: `if result == "" { t.Fatal("expected non-empty result") }`
3. Verify functionality works (e.g., execute binary, call function)
4. Verify location/source using `strings.Contains()` for path checks

**Test Style**:
- When non-destructive, prefer integration tests with real operations over mocks
- Each test should be completely independent and isolated
- Use descriptive variable names and clear test scenario names

**Cleanup Management**:
- Always use `defer cleanup()` immediately after setup
- Cleanup functions should restore original state (environment variables, PATH, etc.)
- Use `defer` for all cleanup, even if test might fail

## Private Networking (Temporary Stopgap Solution)

**Important Context**: This is a temporary workaround until Valet natively supports private networking management.

### Overview

The private networking feature enables customers to access Big Peer NLBs via AWS PrivateLink endpoints, providing secure private connectivity without exposing services to the public internet.

### Commands

**`dittocloud private-networking endpoint-service`**: Creates VPC Endpoint Service in BYOC account
- Auto-discovers NLB using Big Peer name tags
- Configures endpoint service with auto-accept and private DNS
- Outputs domain verification details for TXT record setup
- State file: `terraform-endpoint-service.tfstate`

**`dittocloud private-networking endpoint`**: Creates VPC Endpoint in customer account
- Deploys Interface-type VPC endpoint in specified VPC/subnets
- Auto-creates security group allowing VPC CIDR ingress
- Enables private DNS for seamless connectivity
- State file: `terraform-endpoint.tfstate`

### State File Management

Each component uses separate state files:
- `terraform-endpoint-service.tfstate`: VPC Endpoint Service in BYOC account
- `terraform-endpoint.tfstate`: VPC Endpoint in customer account

This allows independent lifecycle management of each component.

### Future Direction

This implementation is temporary. Future Valet versions will:
- Natively manage VPC Endpoint Services and protection of underlying resources
- Handle private networking lifecycle automatically
