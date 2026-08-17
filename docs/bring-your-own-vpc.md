# Bring Your Own VPC (AWS)

Use the customer-managed VPC mode when your organization provides the AWS VPC
that will host the Ditto Kubernetes cluster. In this mode, Dittocloud does not
create or delete the VPC, subnets, gateways, DHCP options, or DNS
configuration, and it does not grant Cluster API permission to do so.

The customer owns the VPC lifecycle and must satisfy the requirements in this
document before running Dittocloud.

This mode is not zero access to your network. The generated IAM permits Cluster
API to add, replace, and delete routes in your route tables, and to add and
remove tags in a fixed set of key namespaces on your subnets, route tables,
security groups, network interfaces, gateways, and NAT gateways. See
[Ownership Boundary](#ownership-boundary) for exactly what that covers and why
it is needed.

## Requirements

### Account and Region

- The VPC must be in the AWS account and Region targeted by the Dittocloud run.
- The Region must provide at least three Availability Zones.
- Provide subnets across at least three Availability Zones so that the cluster
  control plane and workloads can be distributed for high availability.
- VPC and subnet CIDRs must not overlap the Kubernetes Pod or Service CIDRs
  supplied by Ditto, or any connected network that must reach the cluster.
- Subnets must have enough available addresses for control-plane Nodes, worker
  Nodes, load balancers, and workload scaling.

### VPC DNS and DHCP

AWS Cloud Controller Manager must be able to associate each Kubernetes Node
with its EC2 instance. The Node name must therefore use an AWS-compatible
hostname, and that hostname must resolve inside the VPC.

The VPC must have:

- `enableDnsSupport = true`
- `enableDnsHostnames = true`
- An associated DHCP options set with a real `dopt-...` ID
- `domain-name-servers = AmazonProvidedDNS`
- An AWS regional domain name:
  - `ec2.internal` in `us-east-1`
  - `<region>.compute.internal` in every other Region

In the EC2 API, a VPC reporting `DhcpOptionsId` as `default` has no DHCP
options set associated with it. This is not the ID of the Region's default
DHCP options set and is not supported for this deployment mode.

Custom DNS is supported only when it provides equivalent behavior: instances
must receive and resolve their AWS private DNS names, and the Node name created
during bootstrap must match the EC2 instance identity expected by AWS Cloud
Controller Manager. Review custom DNS designs with Ditto before deployment.

For background, see the AWS documentation for
[DHCP option sets](https://docs.aws.amazon.com/vpc/latest/userguide/DHCPOptionSetConcepts.html)
and [VPC DNS attributes](https://docs.aws.amazon.com/vpc/latest/userguide/AmazonDNS-concepts.html#vpc-dns-support).

### Choose the cluster name first

Subnet tagging requires the cluster name, so you must choose it before you tag
anything and before you run Dittocloud. The same string is used in three
places:

1. The `kubernetes.io/cluster/<cluster-name>` subnet tags below.
2. `--cluster-name` on a later phase-2 Dittocloud run, if you tighten IAM to a
   single cluster.
3. `--cluster-name` when verifying a scope with
   `scopes tags verify` (see [AWS Multi-Scope Configuration and
   Migration](./aws-multi-scope.md)).

All three must match exactly.

> [!IMPORTANT]
> The name AWS sees is not necessarily the name you typed into the tool that
> creates the cluster. Ditto's control plane composes the cluster name from the
> name and the owning organization, as `<name>-<organization>`. Entering name
> `example` for organization `acme` produces the cluster name `example-acme`,
> and `example-acme` is what appears in tags on every AWS resource.
>
> Confirm the composed name before tagging subnets. Tagging for the wrong name
> is not reported as an error — Dittocloud finds no matching subnets, or the
> cluster comes up and load balancer provisioning fails later with an error
> that does not mention subnet tags.

### Subnets and Tags

Dittocloud discovers only subnets carrying the Kubernetes load-balancer role
tags. These discovered subnet IDs are placed into the IAM permissions used by
Cluster API and the AWS Load Balancer Controller.

Apply the following tags before running Dittocloud:

| Tag | Value | Applies to |
| --- | --- | --- |
| `kubernetes.io/cluster/<cluster-name>` | `shared` | Every subnet used by the cluster |
| `kubernetes.io/role/internal-elb` | `1` | Private subnets used by internal load balancers |
| `kubernetes.io/role/elb` | `1` | Public subnets used by internet-facing load balancers |

Additional requirements:

- Every tagged subnet must belong to the selected VPC.
- Use no more than nine tagged public and private load-balancer subnets in
  total. Dittocloud rejects larger sets because the generated AWS managed
  policy would exceed its size limit.
- Private subnets must have the routes needed to reach required AWS APIs and
  container registries, either through NAT gateways or suitable VPC endpoints.
- Public subnets require an internet gateway and appropriate routing when they
  are used for internet-facing load balancers.
- Network ACLs and upstream firewalls must permit cluster traffic. Cluster API
  creates the cluster security groups, but it does not override restrictive
  customer-managed network ACLs, DNS controls, or routing.

### Reference implementation

If you are building the VPC rather than adapting an existing one, Dittocloud's
own VPC module is the reference: [`terraform/aws/vpc`](../terraform/aws/vpc).
It is the configuration used when Dittocloud manages the VPC itself, and it
satisfies every requirement above — three Availability Zones, public and private
subnets, NAT gateways, an explicit DHCP option set, and the subnet role tags.

It uses the upstream `terraform-aws-modules/vpc/aws` module. Building your VPC
with the same module and the same subnet tags is the shortest path to a
conformant network.

## Preflight Checks

Set the values for the target environment:

```bash
AWS_PROFILE=my-profile
AWS_REGION=us-west-2
VPC_ID=vpc-0123456789abcdef0
```

Confirm that the VPC is in the expected account and Region:

```bash
aws --profile "$AWS_PROFILE" --region "$AWS_REGION" ec2 describe-vpcs \
  --vpc-ids "$VPC_ID" \
  --query 'Vpcs[0].{VpcId:VpcId,CIDR:CidrBlock,DhcpOptionsId:DhcpOptionsId,State:State}' \
  --output table
```

Both DNS attributes must return `true`:

```bash
aws --profile "$AWS_PROFILE" --region "$AWS_REGION" ec2 describe-vpc-attribute \
  --vpc-id "$VPC_ID" \
  --attribute enableDnsSupport

aws --profile "$AWS_PROFILE" --region "$AWS_REGION" ec2 describe-vpc-attribute \
  --vpc-id "$VPC_ID" \
  --attribute enableDnsHostnames
```

Retrieve the associated DHCP options ID:

```bash
DHCP_OPTIONS_ID=$(
  aws --profile "$AWS_PROFILE" --region "$AWS_REGION" ec2 describe-vpcs \
    --vpc-ids "$VPC_ID" \
    --query 'Vpcs[0].DhcpOptionsId' \
    --output text
)

echo "$DHCP_OPTIONS_ID"
```

Stop if the result is `default`. A valid configuration returns a `dopt-...`
ID. Inspect that options set and confirm the regional domain name and
`AmazonProvidedDNS`:

```bash
aws --profile "$AWS_PROFILE" --region "$AWS_REGION" ec2 describe-dhcp-options \
  --dhcp-options-ids "$DHCP_OPTIONS_ID" \
  --query 'DhcpOptions[0].DhcpConfigurations' \
  --output table
```

Review the candidate subnets, Availability Zones, address capacity, and role
tags. Replace `<cluster-name>` before running:

```bash
aws --profile "$AWS_PROFILE" --region "$AWS_REGION" ec2 describe-subnets \
  --filters "Name=vpc-id,Values=$VPC_ID" \
  --query 'Subnets[].{SubnetId:SubnetId,AZ:AvailabilityZone,AvailableIPs:AvailableIpAddressCount,Cluster:Tags[?Key==`kubernetes.io/cluster/<cluster-name>`]|[0].Value,PublicRole:Tags[?Key==`kubernetes.io/role/elb`]|[0].Value,PrivateRole:Tags[?Key==`kubernetes.io/role/internal-elb`]|[0].Value}' \
  --output table
```

## Run Dittocloud

After the preflight checks pass:

```bash
dittocloud bootstrap aws \
  --aws-profile "$AWS_PROFILE" \
  --aws-region "$AWS_REGION" \
  --customer-managed-vpc \
  --vpc-id "$VPC_ID"
```

Do not pass `--aws-vpc-name` or `--aws-vpc-cidr` in customer-managed VPC mode.
Keep the generated Terraform state file for future updates.

The `--create-vpc=false` mode is different: it allows Cluster API to create and
manage a VPC and therefore retains VPC lifecycle permissions. Use
`--customer-managed-vpc` only when the customer owns the selected VPC.

### Review the plan before approving it

Dittocloud asks for confirmation before applying, but by default it does not
display what it is about to create. The summary reads only:

```text
📋 Terraform Plan Summary:
Changes have been detected and will be applied.
Use --log-level debug to see detailed plan output.
```

The confirmation prompt that follows grants cross-account access to your AWS
account. Do not approve it unseen. Add `--dry-run --log-level debug` to the
command above and read the plan first:

```bash
dittocloud bootstrap aws \
  --aws-profile "$AWS_PROFILE" \
  --aws-region "$AWS_REGION" \
  --customer-managed-vpc \
  --vpc-id "$VPC_ID" \
  --dry-run --log-level debug
```

Confirm all of the following in the output, then rerun without `--dry-run` and
`--log-level debug`:

1. `Plan:` reports `0 to destroy`.
2. Every `ec2:Vpc` condition names your VPC ARN, and no other VPC appears.
3. The `elasticloadbalancing:Subnet` conditions list exactly your tagged
   load-balancer subnet IDs.
4. The trust policies on the controller and trust-editor roles name the Ditto
   principals you expect. If you supplied `--controller-trusted-role-arns` or
   `--iam-trusted-role-arns`, confirm your values appear rather than the
   built-in defaults.

### Verify the installation

Neither the apply output nor the tool's exit status tells you which roles now
exist — a successful run prints only the account ID and Region. Verify
explicitly.

The five roles Dittocloud creates:

```bash
aws --profile "$AWS_PROFILE" iam list-roles \
  --query 'Roles[?!contains(Path, `aws-service-role`)].[RoleName,Path]' \
  --output table
```

| Role | Path | Purpose |
| --- | --- | --- |
| `controllers.cluster-api-provider-aws.sigs.k8s.io` | `/` | Assumed by Ditto to create and manage cluster infrastructure |
| `iam-trust-editor.ditto.live` | `/ditto/` | Creates cluster-specific roles under `/dittocluster/`, constrained by permission boundary |
| `iam-admin-view.ditto.live` | `/ditto/` | Read-only access for Ditto support |
| `control-plane.cluster-api-provider-aws.sigs.k8s.io` | `/` | Instance role for control-plane Nodes |
| `nodes.cluster-api-provider-aws.sigs.k8s.io` | `/` | Instance role for worker Nodes |

Confirm the controller role trusts the expected Ditto account and no other:

```bash
aws --profile "$AWS_PROFILE" iam get-role \
  --role-name controllers.cluster-api-provider-aws.sigs.k8s.io \
  --query 'Role.AssumeRolePolicyDocument.Statement[].Principal' \
  --output json
```

Confirm the generated permissions are scoped to your VPC. Every EC2 condition
in the controller policy must name it:

```bash
CONTROLLER_POLICY_ARN=$(
  aws --profile "$AWS_PROFILE" iam list-policies --scope Local \
    --query "Policies[?PolicyName=='ditto-capa-controller-policy'].Arn" \
    --output text
)

aws --profile "$AWS_PROFILE" iam get-policy-version \
  --policy-arn "$CONTROLLER_POLICY_ARN" \
  --version-id "$(
    aws --profile "$AWS_PROFILE" iam get-policy \
      --policy-arn "$CONTROLLER_POLICY_ARN" \
      --query 'Policy.DefaultVersionId' --output text
  )" \
  --query 'PolicyVersion.Document.Statement[?Condition!=null].Condition' \
  --output json
```

Keep this output. It is the authoritative answer to "what can Ditto do in our
account", and it is the document to review if your security team asks.

## Ownership Boundary

| Component | Owner in customer-managed VPC mode |
| --- | --- |
| Creating and deleting the VPC, subnets, gateways, and network ACLs | Customer only |
| DHCP options and DNS configuration | Customer only |
| Subnet capacity, CIDR allocation, and initial Kubernetes subnet tags | Customer only |
| Route table lifecycle — creating and deleting route tables | Customer only |
| **Route entries within existing route tables** | **Customer, and Cluster API within your VPC** |
| **Tags in the namespaces listed below, on your subnets, route tables, security groups, network interfaces, gateways, and NAT gateways** | **Customer, and Cluster API within your VPC** |
| Cross-account and Cluster API IAM resources | Dittocloud |
| Cluster EC2 instances, security groups, and load balancers | Cluster API within the permissions granted by Dittocloud |

The two bold rows are shared, not customer-only. Specifically, the generated
controller policy allows, conditioned on `ec2:Vpc` equal to your VPC:

- `ec2:CreateRoute`, `ec2:DeleteRoute`, `ec2:ReplaceRoute` on route tables in
  your VPC. Cluster API needs this to maintain Pod network routes; it is not
  gated on a Ditto ownership tag, so it applies to any route table in the VPC.
- `ec2:CreateTags` and `ec2:DeleteTags` on your subnets, route tables, security
  groups, network interfaces, elastic IPs, internet gateways, NAT gateways, and
  the VPC itself. Tag keys are restricted to `kubernetes.io/*`, `k8s.io/*`,
  `sigs.k8s.io/*`, `ditto.live/*`, `Name`, and `MachineName`. Note that `Name`
  is not namespaced: Cluster API can overwrite the `Name` tag on these
  resources.

Dittocloud does not receive permission to create or delete VPCs, subnets,
gateways, route tables, DHCP option sets, or DNS settings in this mode. It
therefore cannot repair a missing DHCP option set, a missing DNS attribute, an
absent subnet, or an absent route table on your behalf — if a preflight check in
this document fails, you must fix it.

If your security policy requires that no Ditto principal can alter routing or
tags on customer-owned network resources, raise it with Ditto before deploying.
Narrowing this boundary is tracked work, not current behavior.

Review the exact conditions in your own account rather than relying on this
table — see [Verify the installation](#verify-the-installation).

## Troubleshooting Node Initialization

The following symptoms commonly indicate that the DHCP or DNS requirements
were not met:

- Nodes register with short names such as `ip-10-0-1-25` instead of an AWS
  private FQDN.
- `Node.spec.providerID` remains empty.
- Nodes retain the
  `node.cloudprovider.kubernetes.io/uninitialized:NoSchedule` taint.
- Worker Nodes retain the
  `node.cluster.x-k8s.io/uninitialized:NoSchedule` taint.
- Cluster API Machines remain `Provisioned` while reporting that they are
  waiting for a Node with a matching provider ID.
- Pod log requests fail because the API server cannot resolve the short Node
  hostname when contacting the kubelet on port `10250`.

Correct the VPC DHCP and DNS configuration before replacing affected Machines.
Changing the bootstrap template does not rename existing Kubernetes Node
objects.

## Related

- [AWS Multi-Scope Configuration and Migration](./aws-multi-scope.md) — running
  more than one deployment in a single AWS account, and tightening IAM to a
  single named cluster.
- [Migrate a Legacy Version-1 AWS Cluster to Scopes](./migrate-to-scopes.md) —
  converting an existing pre-scopes deployment.
- [Decommissioning](./decommissioning.md) — removing a deployment, including the
  customer-owned VPC.
