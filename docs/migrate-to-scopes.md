# Migrate a Legacy Version-1 AWS Cluster to Scopes

This runbook migrates one existing pre-scopes AWS deployment into the default
scope, then moves that scope to tag-policy version `1` for the same single
cluster.

Use this runbook only when the legacy deployment already has cluster-specific
IAM conditions from a previous `--cluster-name` bootstrap. If the VPC is shared
by multiple clusters, complete the migration only through version `0` and do
not perform the version-`1` steps.

The migration deliberately starts the default scope at
`scopeTagPolicyVersion: 0`. When legacy state proves that cluster-specific IAM
is already enabled, Dittocloud preserves those restrictions during this bridge
phase. The later live-verification workflow moves the scope marker and YAML to
version `1` without temporarily broadening IAM.

> [!IMPORTANT]
> Run all commands from an interactive terminal. Use the same state file, AWS
> account, AWS profile, and Dittocloud version throughout the migration. Stop
> all other Dittocloud and Terraform operations that use this state before
> starting.

For the full scope contract, recovery behavior, and rollback constraints, see
[AWS Multi-Scope Configuration and Migration](./aws-multi-scope.md).

## Runbook

### 1. Set the migration inputs

Use absolute paths. `cluster_name` must be the exact name already used by the
legacy cluster-specific IAM policies.

```bash
state_path=/absolute/path/to/terraform.tfstate
scopes_path=/absolute/path/to/scopes.yaml
aws_profile=customer-account
cluster_name=existing-cluster-name
```

If the legacy deployment used explicit `--controller-trusted-role-arns` or
`--iam-trusted-role-arns` overrides, add the same flags to every normal
scope-mode bootstrap in this runbook. Do not pass them to the review-only
scope-file generation or registry-seed commands.

### 2. Confirm the starting state and take an operator backup

The state must exist, and the scopes-file destination must be missing or empty.

```bash
test -f "$state_path"
test ! -s "$scopes_path"

timestamp=$(date -u +%Y%m%dT%H%M%SZ)
operator_backup="${state_path}.pre-scopes-${timestamp}"
cp -p -- "$state_path" "$operator_backup"
sha256sum "$state_path" "$operator_backup"
```

Both hashes must match. Keep this backup outside any automated cleanup. The
registry-seed command creates another state backup and manifest immediately
before its own apply.

### 3. Generate the default-scope draft

Do not add `--dry-run`; generation is already review-only and does not run
Terraform.

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --generate-scopes-file \
  --scopes-file "$scopes_path" \
  --state "$state_path"
```

Dittocloud derives values from the selected state and prompts only for required
missing values. It generates the immutable `scopeRef` and stops without
changing Terraform state.

### 4. Review the generated scope

```bash
sed -n '1,200p' "$scopes_path"
```

Confirm all of the following before continuing:

1. The file contains exactly one generated `dsc-...` scope.
2. The scope has `default: true`.
3. The scope has `scopeTagPolicyVersion: 0`.
4. `clusterName` exactly matches `$cluster_name` and came from the legacy
   cluster-specific IAM state evidence.
5. `clusterType`, `region`, and the VPC ownership fields match the deployed
   cluster.
6. For a Dittocloud-managed VPC, `natGatewayName` matches the stable `Name` tag
   currently applied to every managed NAT gateway.
7. For a Dittocloud-managed VPC, `publicSubnetNetmask` and
   `privateSubnetNetmask` match the subnets that exist today. Generation reads
   these back from state, and they are usually `22` and `18` for a VPC created
   before the DMZ address layout. **Do not drop them.** Every subnet CIDR is
   derived from these values, so leaving them out applies today's defaults,
   renumbers live subnets, and recreates the NAT gateways, nodes, and load
   balancers with them.

Do not hand-edit the generated `scopeRef`. The seed preflight rejects a
`clusterName`, Region, VPC, or cluster-type value that conflicts with state
evidence.

### 5. Validate the registry seed

```bash
dittocloud bootstrap aws scopes migrate seed-registry \
  --scopes-file "$scopes_path" \
  --state "$state_path" \
  --dry-run
```

The validated plan must create only this built-in Terraform resource:

```text
terraform_data.scope_registry["<scopeRef>"]
```

Stop if the command reports drift or any other resource action. A dry run does
not create a backup or change state.

### 6. Seed the immutable default-scope identity

```bash
dittocloud bootstrap aws scopes migrate seed-registry \
  --scopes-file "$scopes_path" \
  --state "$state_path"
```

Review the same one-resource plan and approve the interactive confirmation.
Record the state-backup and manifest paths printed by Dittocloud. The command
must report that it persisted only the default-scope registry sentinel.

### 7. Review the first normal scope-mode plan

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file "$scopes_path" \
  --state "$state_path" \
  --aws-profile "$aws_profile" \
  --dry-run
```

The plan may contain only the guarded initial migration changes:

1. A version-`0` scope tag-policy marker.
2. A normalized applied-configuration snapshot.
3. Additive `ditto.live/scope-ref` tags and scope-aware outputs.
4. No replacement, deletion, unrelated resource update, or legacy output
   mutation.

For a legacy version-`1` cluster, its existing cluster-specific IAM conditions
must remain in place through the version-`0` bridge. Stop if the plan broadens
those conditions.

### 8. Apply the first normal scope-mode plan

Rerun the same command without `--dry-run`:

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file "$scopes_path" \
  --state "$state_path" \
  --aws-profile "$aws_profile"
```

Review the validated plan again and approve it. Do not add non-default scopes
until this default-scope migration has applied successfully.

### 9. Confirm the scope state records

```bash
terraform state list -state="$state_path" | grep '^terraform_data.scope_'
```

The state must contain records for the registry, the version-`0` tag-policy
marker, and the applied configuration:

```text
terraform_data.scope_configuration["<scopeRef>"]
terraform_data.scope_registry["<scopeRef>"]
terraform_data.scope_tag_policy["<scopeRef>"]
```

Copy the generated scope reference from `scopes.yaml` for the remaining steps:

```bash
scope_ref=dsc-...
```

### 10. Verify live AWS ownership for version 1

This command is read-only. It verifies state-backed Dittocloud resources and
the cluster's native Kubernetes, Cluster API, load-balancer, and EKS identities
where applicable.

```bash
dittocloud bootstrap aws scopes tags verify \
  --state "$state_path" \
  --scopes-file "$scopes_path" \
  --scope-ref "$scope_ref" \
  --cluster-name "$cluster_name" \
  --aws-profile "$aws_profile"
```

The AWS credentials must resolve to the account recorded in state. Stop if the
command finds a conflicting scope tag, another owned cluster identity, a Region
or account mismatch, or an incomplete resource identity. Leave the scope at
version `0` until every verification issue is resolved.

### 11. Enable version 1 in the scopes file

Repeat the successful verification with `--enable`:

```bash
dittocloud bootstrap aws scopes tags verify \
  --state "$state_path" \
  --scopes-file "$scopes_path" \
  --scope-ref "$scope_ref" \
  --cluster-name "$cluster_name" \
  --aws-profile "$aws_profile" \
  --enable
```

This repeats live verification, then atomically changes only the selected scope
to `scopeTagPolicyVersion: 1`. It does not run Terraform or mutate AWS.

Review the file again:

```bash
sed -n '1,200p' "$scopes_path"
```

Confirm that `clusterName` is unchanged and the selected scope now has
`scopeTagPolicyVersion: 1`.

### 12. Review the version-1 Terraform plan

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file "$scopes_path" \
  --state "$state_path" \
  --aws-profile "$aws_profile" \
  --dry-run
```

Because state still records applied policy version `0`, Dittocloud repeats the
live inventory verification before planning. Review only the expected
single-cluster IAM tightening, the version-`1` marker, and the updated applied
configuration. Stop on replacements, deletions, unrelated changes, or a
different cluster name.

### 13. Apply the version-1 plan

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file "$scopes_path" \
  --state "$state_path" \
  --aws-profile "$aws_profile"
```

The non-dry-run workflow verifies live ownership before planning and again
after confirmation immediately before applying. Do not bypass a failed second
verification.

### 14. Run the final checks

First, confirm that the normal scope-mode plan has no intended changes:

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file "$scopes_path" \
  --state "$state_path" \
  --aws-profile "$aws_profile" \
  --dry-run
```

Then repeat the read-only ownership verification against the applied version-1
marker:

```bash
dittocloud bootstrap aws scopes tags verify \
  --state "$state_path" \
  --scopes-file "$scopes_path" \
  --scope-ref "$scope_ref" \
  --cluster-name "$cluster_name" \
  --aws-profile "$aws_profile"
```

### 15. Verify state-backed scopes-file recovery

Choose a destination that does not exist:

```bash
recovered_scopes="${scopes_path}.recovered"
test ! -e "$recovered_scopes"

dittocloud bootstrap aws scopes recover \
  --state "$state_path" \
  --scopes-file "$recovered_scopes"

diff -u -- "$scopes_path" "$recovered_scopes"
```

The recovered configuration must represent the same applied scope, cluster
name, and policy version. Recovery never plans, applies, queries AWS, or changes
Terraform state.

## Stop and recovery conditions

- Do not continue after unexpected drift, a replacement, a deletion, or a
  legacy-output mutation.
- Do not enable version `1` when more than one owned cluster identity exists in
  the scope VPC. Version `0` is the supported shared-VPC compatibility mode.
- Do not manually set `scopeTagPolicyVersion: 1`; use `scopes tags verify
  --enable` so live ownership is checked first.
- Do not restore the registry-seed or operator backup after later successful
  state changes without first preserving the current state. Restoring an older
  backup discards every state change made after it.
- If Dittocloud reports that valid partial state was saved after a failed
  apply, reconcile from that state unless a reviewed recovery plan requires an
  older backup.
