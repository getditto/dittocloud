# Bring Your Own VPC (AWS)

Use the customer-managed VPC mode when your organization provides the AWS VPC
that will host the Ditto Kubernetes cluster. In this mode, Dittocloud creates
the required IAM resources but does not create, update, or delete the VPC,
subnets, route tables, gateways, DHCP options, or DNS configuration.

The customer owns the VPC lifecycle and must satisfy the requirements in this
document before running Dittocloud.

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

## Ownership Boundary

| Component | Owner in customer-managed VPC mode |
| --- | --- |
| VPC, subnets, route tables, gateways, and network ACLs | Customer |
| DHCP options and DNS configuration | Customer |
| Subnet capacity and Kubernetes subnet tags | Customer |
| Cross-account and Cluster API IAM resources | Dittocloud |
| Cluster EC2 instances, security groups, and load balancers | Cluster API within the permissions granted by Dittocloud |

Because Dittocloud omits VPC lifecycle permissions in this mode, it cannot
repair missing routes, subnet tags, DHCP options, or DNS settings on the
customer's behalf.

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
