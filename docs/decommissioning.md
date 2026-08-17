# Decommission an AWS Dittocloud Deployment

This runbook removes a Dittocloud AWS deployment and everything it delegated
access for: the clusters, the cross-account IAM, and — where the VPC was created
by Dittocloud — the network.

Dittocloud has no `destroy` command. Removal is a manual sequence, and the order
matters: the IAM Dittocloud creates is what allows Ditto's controllers to clean
up after themselves. Remove it too early and the cluster resources become
orphaned, recoverable only with your own administrator credentials.

> [!IMPORTANT]
> Run all commands from an interactive terminal. Use the same state file, AWS
> account, AWS profile, and Dittocloud version throughout. Stop all other
> Dittocloud and Terraform operations against this state before starting.

## Order of operations

| # | Step | Who |
| --- | --- | --- |
| 1 | Delete every cluster in the deployment | Ditto control plane |
| 2 | Confirm cluster resources are gone, and remove what was left behind | You |
| 3 | Remove the Dittocloud cross-account IAM | You |
| 4 | Remove the VPC | Whoever owns it |
| 5 | Audit for residue | You |

Do not begin step 3 until step 2 is complete. Once the cross-account IAM is
gone, Ditto's controllers can no longer delete anything in your account.

## 1. Set the inputs

Use absolute paths.

```bash
state_path=/absolute/path/to/terraform.tfstate
scopes_path=/absolute/path/to/scopes.yaml     # scope mode only
aws_profile=customer-account
region=us-east-2
```

Record the flags the deployment was **originally created with**. You will need
them in step 3, and Dittocloud does not prompt for them:

```bash
customer_managed_vpc=true                      # was --customer-managed-vpc used?
vpc_id=vpc-0123456789abcdef0                   # the value passed to --vpc-id
```

If you do not know, recover them from state rather than guessing. In scope mode
the applied configuration is recorded per scope:

```bash
terraform state show -state="$state_path" \
  'terraform_data.scope_configuration["<scopeRef>"]'
```

In legacy mode, inspect the VPC mode marker:

```bash
terraform state show -state="$state_path" terraform_data.validate_vpc_mode
```

Take a backup before changing anything:

```bash
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
cp -p -- "$state_path" "${state_path}.pre-decommission-${timestamp}"
```

## 2. Delete the clusters

Delete every cluster in the deployment through Ditto's control plane, not by
deleting AWS resources by hand. The controllers remove EC2 instances, load
balancers, target groups, and security groups in dependency order; deleting
instances directly leaves the rest behind.

Wait for deletion to complete before continuing. Deletion is complete when the
control plane reports it *and* AWS agrees:

```bash
aws --profile "$aws_profile" --region "$region" ec2 describe-instances \
  --query 'length(Reservations[].Instances[?State.Name!=`terminated`][])' \
  --output text

aws --profile "$aws_profile" --region "$region" elbv2 describe-load-balancers \
  --query 'length(LoadBalancers)' --output text
```

Both must return `0`.

### Remove what the controllers could not

A reported-complete deletion does not mean the account is clean. Audit and
remove the remainder yourself.

**Security groups.** On a scope at tag-policy version `1`, Cluster API is denied
permission to delete its own security groups: the IAM conditions require
`ec2:ResourceTag/kubernetes.io/cluster/<clusterName>`, and Cluster API tags
security groups with `sigs.k8s.io/cluster-api-provider-aws/cluster/<clusterName>`
instead. The denial appears in CloudTrail as:

```text
ec2:RevokeSecurityGroupIngress   Client.UnauthorizedOperation
```

Orphaned security groups block deletion of the VPC that contains them, so they
must be removed before step 4. They reference one another, so revoke their rules
before deleting:

```bash
vpc_id_to_clean="$vpc_id"

sgs=$(aws --profile "$aws_profile" --region "$region" ec2 describe-security-groups \
  --filters "Name=vpc-id,Values=$vpc_id_to_clean" \
  --query 'SecurityGroups[?GroupName!=`default`].GroupId' --output text)

for sg in $sgs; do
  for dir in ingress egress; do
    [ "$dir" = ingress ] && q=IpPermissions || q=IpPermissionsEgress
    perms=$(aws --profile "$aws_profile" --region "$region" \
      ec2 describe-security-groups --group-ids "$sg" \
      --query "SecurityGroups[0].$q" --output json)
    [ "$(printf '%s' "$perms" | tr -d ' \n')" = '[]' ] && continue
    printf '%s' "$perms" > /tmp/sg-perms.json
    aws --profile "$aws_profile" --region "$region" \
      "ec2" "revoke-security-group-$dir" --group-id "$sg" \
      --ip-permissions file:///tmp/sg-perms.json >/dev/null
  done
done

for sg in $sgs; do
  aws --profile "$aws_profile" --region "$region" \
    ec2 delete-security-group --group-id "$sg"
done
```

Review the list before deleting. Delete only groups belonging to the cluster you
are removing — never the VPC's `default` group, and never a group your own
workloads use.

**Volumes, buckets, and cluster roles.** Check for each:

```bash
aws --profile "$aws_profile" --region "$region" ec2 describe-volumes \
  --query 'Volumes[?State==`available`].[VolumeId,Size,Tags[?Key==`Name`]|[0].Value]' \
  --output table

aws --profile "$aws_profile" s3api list-buckets \
  --query 'Buckets[].Name' --output text | tr '\t' '\n' | grep '^ditto-'

aws --profile "$aws_profile" iam list-roles \
  --path-prefix /dittocluster/ --query 'Roles[].RoleName' --output text
```

Unattached volumes are not always tagged with the cluster identity, so confirm
by creation time and size before deleting. Buckets matching `ditto-cluster-*` and
`ditto-hydra-blob-*` are created per cluster; confirm with Ditto whether the
contents are needed before removing them, because deletion is irreversible.
Roles under `/dittocluster/` are created by the trust editor and are usually
cleaned up shortly after the cluster; re-check before removing them manually.

> [!IMPORTANT]
> Deleting a bucket destroys its contents. Confirm the retention expectation with
> Ditto before this step, not after.

## 3. Remove the Dittocloud IAM

There is no `dittocloud destroy`. The supported approach is to have Dittocloud
materialize its embedded Terraform without applying anything, then destroy from
that directory.

Materialize the configuration. `--dry-run` makes no AWS changes;
`--remove-tmpdir=false` keeps the generated files:

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file "$scopes_path" \
  --state "$state_path" \
  --aws-profile "$aws_profile" \
  --dry-run --remove-tmpdir=false
```

Omit `--scopes` and `--scopes-file` for a legacy deployment. If the deployment
was created with `--controller-trusted-role-arns` or `--iam-trusted-role-arns`
overrides, pass the same values here.

Note the path from the output:

```text
Copying terraform files to temporary directory "/var/folders/.../dittocloudNNNNNNN"
```

Destroy from that directory, supplying the variables the deployment was created
with:

```bash
cd /var/folders/.../dittocloudNNNNNNN/aws

terraform init

terraform plan -destroy \
  -var profile="$aws_profile" \
  -var region="$region" \
  -var customer_managed_vpc="$customer_managed_vpc" \
  -var vpc_id="$vpc_id"
```

Review the plan. It must report `0 to add, 0 to change`, and every resource
listed for destruction must belong to Dittocloud. Then apply it:

```bash
terraform destroy \
  -var profile="$aws_profile" \
  -var region="$region" \
  -var customer_managed_vpc="$customer_managed_vpc" \
  -var vpc_id="$vpc_id"
```

Three things to expect:

1. **Omitting the original variables breaks the plan.** Without
   `-var customer_managed_vpc=true -var vpc_id=...`, Terraform falls back to
   `create_vpc=true`, instantiates the VPC module, and fails with
   `Invalid count argument` before producing a plan. This is a plan-time failure,
   not a partial destroy.
2. **The destroy may need two passes.** The first can fail with
   `DeleteConflict: Cannot delete a policy attached to entities` on one or two
   policies — a race between role deletion and policy deletion. Re-run the same
   command; the remaining resources are removed.
3. **It operates on a copy of state.** The temporary directory holds a copy, so
   your canonical state file is stale afterwards. Keep the backup from step 1 and
   retire the original state file rather than reusing it.

Confirm nothing is left:

```bash
aws --profile "$aws_profile" iam list-roles \
  --query 'Roles[?!contains(Path, `aws-service-role`)].RoleName' \
  --output text | tr '\t' '\n' | grep -E 'cluster-api|ditto' || echo "no Ditto roles"

aws --profile "$aws_profile" iam list-policies --scope Local \
  --query 'Policies[].PolicyName' --output text | tr '\t' '\n' | grep -i ditto \
  || echo "no Ditto policies"
```

## 4. Remove the VPC

**Customer-managed VPC.** The VPC is yours and Dittocloud never had permission to
delete it. Remove it with whatever created it. It will not delete while cluster
security groups or network interfaces remain, so step 2 must be complete.

**Dittocloud-managed VPC.** The VPC is in the Terraform destroyed in step 3 and
is removed with it. If the destroy reported it as skipped or failed, re-run.

## 5. Audit for residue

Run in every Region any scope covered, not only the primary one:

```bash
for r in us-east-2 us-west-2; do
  echo "== $r"
  printf '  instances       %s\n' "$(aws --profile "$aws_profile" --region $r ec2 describe-instances --query 'length(Reservations[].Instances[?State.Name!=`terminated`][])' --output text)"
  printf '  volumes         %s\n' "$(aws --profile "$aws_profile" --region $r ec2 describe-volumes --query 'length(Volumes)' --output text)"
  printf '  load balancers  %s\n' "$(aws --profile "$aws_profile" --region $r elbv2 describe-load-balancers --query 'length(LoadBalancers)' --output text)"
  printf '  nat gateways    %s\n' "$(aws --profile "$aws_profile" --region $r ec2 describe-nat-gateways --query 'length(NatGateways[?State!=`deleted`])' --output text)"
  printf '  non-default SGs %s\n' "$(aws --profile "$aws_profile" --region $r ec2 describe-security-groups --query 'length(SecurityGroups[?GroupName!=`default`])' --output text)"
  printf '  secrets         %s\n' "$(aws --profile "$aws_profile" --region $r secretsmanager list-secrets --query 'length(SecretList)' --output text)"
done

echo "== global"
printf '  oidc providers        %s\n' "$(aws --profile "$aws_profile" iam list-open-id-connect-providers --query 'length(OpenIDConnectProviderList)' --output text)"
printf '  instance profiles     %s\n' "$(aws --profile "$aws_profile" iam list-instance-profiles --query 'length(InstanceProfiles)' --output text)"
printf '  /dittocluster/ roles  %s\n' "$(aws --profile "$aws_profile" iam list-roles --path-prefix /dittocluster/ --query 'length(Roles)' --output text)"
```

Compare against what the account contained before Dittocloud was installed —
not against zero. Accounts carry pre-existing roles and policies for
observability, compliance, and cost tooling, and those are not yours to remove.

## Stop and recovery conditions

- Do not remove the cross-account IAM before cluster deletion has completed. If
  you already have, the cluster's AWS resources must be removed with your own
  administrator credentials; Ditto can no longer act in the account.
- Do not delete a security group, volume, or bucket you cannot positively
  attribute to the cluster being removed.
- Do not reuse the canonical state file after step 3. It no longer reflects
  reality.
- If `terraform destroy` reports resources it cannot delete after two passes,
  stop and capture the error. Repeated `DeleteConflict` on the same resource
  indicates an attachment created outside Dittocloud.
- Retain the step 1 backup until the residue audit is clean.

## Related

- [Bring Your Own VPC](./bring-your-own-vpc.md) — customer-managed VPC
  requirements and installation verification.
- [AWS Multi-Scope Configuration and Migration](./aws-multi-scope.md) — scope
  contract, tag-policy versions, and rollback constraints.
- [Migrate a Legacy Version-1 AWS Cluster to Scopes](./migrate-to-scopes.md)
