# AWS Multi-Scope Configuration and Migration

Dittocloud AWS scope mode manages multiple deployment scopes in one AWS
account and one Terraform state. Each scope has an immutable generated
`scopeRef`; exactly one scope is the default migration bridge that preserves
the existing unsuffixed Terraform addresses and AWS IAM names.

Scope tag-policy version `0` is compatibility mode for zero, one, or multiple
clusters in a scope VPC. Version `1` is the secure single-cluster mode and
requires one exact `clusterName` plus the guarded live verification and enable
workflow described below. Do not enable version `1` by editing YAML directly.

> [!IMPORTANT]
> Dittocloud can create and manage additional non-default scopes, but dependent
> services do not yet consume their scope bindings. Additional scopes are not
> end-to-end supported until those downstream integrations are available.

## Choosing a tag-policy version

This is the highest-consequence decision in scope mode, and the answer is
determined by how many clusters share the scope's VPC — not by how locked down
you would prefer to be.

| Clusters sharing the scope VPC | Version you may use | Why |
| --- | --- | --- |
| Zero — IAM prepared, no cluster yet | `0` | There is no cluster identity to verify against. |
| One | `0` or `1` | `1` is the secure choice. `0` remains valid indefinitely. |
| Two or more | `0` only | `1` scopes IAM to one exact cluster name. Applying it while a second cluster shares the VPC removes the permissions that cluster's controllers depend on. |

Additional rules that are easy to get wrong:

- **A recorded `clusterName` does not mean you are on version `1`.**
  `clusterName` is the phase-2 IAM-tightening input. At version `0` it is
  recorded but does not restrict IAM. The version is the only thing that decides
  whether cluster-specific conditions are active.
- **Do not set `scopeTagPolicyVersion: 1` by editing the YAML.** The supported
  transition is `scopes tags verify --enable`, which checks live AWS ownership
  first. Direct Terraform enablement is rejected.
- **Version `1` cannot be downgraded to `0`**, and its applied `clusterName`
  cannot be changed. Treat enablement as one-way.
- **Version `1` currently leaves security groups behind on cluster deletion.**
  The IAM conditions key on `kubernetes.io/cluster/<name>`, which Cluster API
  does not apply to security groups, so cleanup of those is denied. See
  [Decommissioning](./decommissioning.md) before enabling version `1` on a
  deployment you expect to tear down.

If you are unsure how many clusters share the VPC, run the read-only
verification described in [Verify readiness for single-cluster
lockdown](#verify-readiness-for-single-cluster-lockdown). It reports conflicting
owned-cluster identities rather than guessing.

## Scope file

The YAML map key is the immutable scope reference:

```yaml
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  clusterType: kubeadm
  scopeTagPolicyVersion: 0
  vpc:
    mode: existing
    id: vpc-0123456789abcdef0
```

Do not create or edit a `scopeRef` manually. Use `scopes add` for a greenfield
configuration or an additional non-default scope:

```bash
dittocloud bootstrap aws scopes add \
  --state terraform.tfstate \
  --scopes-file scopes.yaml \
  --region us-west-2 \
  --cluster-type kubeadm \
  --vpc-mode existing \
  --vpc-id vpc-09e877f9012f52241
```

The command only updates the scope file. It never initializes Terraform or
changes Terraform state. Run a separate normal bootstrap to review and apply
the resulting infrastructure plan.

In scope mode, Cluster API—not Terraform—owns the open-ended shared-VPC tag
namespaces `kubernetes.io/cluster/*`,
`sigs.k8s.io/cluster-api-provider-aws/cluster/*`, and the exact
`sigs.k8s.io/cluster-api-provider-aws/role` key. A subnet or NAT gateway may
therefore retain membership tags for several clusters. Dittocloud continues to
manage the Kubernetes load-balancer role tags, Ditto tags, resource `Name`
tags, and `ditto.live/scope-ref`.

## Convert an existing legacy deployment

Use the guarded migration workflow when the selected state was created with
legacy AWS flags and does not yet contain a scope registry.

### 1. Generate a review-only default scope

The destination must be missing or empty:

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --generate-scopes-file \
  --scopes-file scopes.yaml \
  --state terraform.tfstate
```

Dittocloud derives only unambiguous values from the selected state, prompts for
required missing values in an interactive terminal, generates the persistent
`scopeRef`, and stops without running Terraform. Review the evidence printed by
the command and the generated YAML. The file must contain exactly one
`default: true` scope with `scopeTagPolicyVersion: 0`.

For a Dittocloud-managed legacy VPC, review the current NAT gateways' `Name`
tags. If Cluster API established a stable value that differs from the VPC
module default, retain it independently of cluster membership:

```yaml
vpc:
  mode: dittocloud
  name: ditto-k8s
  cidr: 10.210.0.0/16
  natGatewayName: founding-cluster-nat
```

The first guarded plan verifies that every refreshed NAT gateway has that
exact value. Do not use `clusterName` for this purpose: `clusterName` is the
optional phase-2 IAM-tightening input and is unrelated to how many clusters
share the VPC.

### 2. Validate the registry seed

Run the dedicated migration in dry-run mode:

```bash
dittocloud bootstrap aws scopes migrate seed-registry \
  --scopes-file scopes.yaml \
  --state terraform.tfstate \
  --dry-run
```

The validated target plan must create exactly one built-in Terraform resource:

```text
terraform_data.scope_registry["<scopeRef>"]
```

Dry-run mode does not create a backup and does not change the selected state.

### 3. Seed the immutable default identity

Rerun without `--dry-run` and approve the interactive confirmation:

```bash
dittocloud bootstrap aws scopes migrate seed-registry \
  --scopes-file scopes.yaml \
  --state terraform.tfstate
```

Immediately before applying the target plan, Dittocloud creates a byte-for-byte
`0600` state backup and a JSON manifest beside the selected state. The command
prints both paths. It atomically persists only a validated state containing the
new default registry sentinel; existing outputs and resource instances must be
unchanged.

### 4. Review the first normal scope-mode plan

Run a separate normal bootstrap:

```bash
dittocloud bootstrap aws \
  --scopes=true \
  --scopes-file scopes.yaml \
  --state terraform.tfstate \
  --dry-run
```

When the registry exists but the applied tag-policy marker does not, Dittocloud
saves and inspects the complete refreshed plan. It accepts exactly one
version-`0` marker create, exactly one normalized applied-configuration
snapshot, additive scope tags, and additive scope-aware output fields. It
rejects replacements, deletions, non-tag resource changes, legacy output
mutations, unreviewed drift, NAT `Name` mismatches, and a snapshot that differs
from the reviewed scope. The internal machine-readable plan is suppressed
rather than dumped into command output.

Cluster API membership tags are retained without enumerating cluster names, so
a shared VPC can contain zero, one, or many clusters. After a successful dry
run, rerun without `--dry-run`; Dittocloud applies the exact saved and validated
plan rather than planning again.

Add non-default scopes only after this default-only plan has been reviewed and
successfully applied.

## Reading the plan when you add a scope

Adding a non-default scope produces a plan that appears to rewrite the existing
default scope's live IAM. It does not. Read this section before you approve or
abort one.

A single added scope typically plans as:

```text
Plan: 50 to add, 10 to change, 0 to destroy.
```

The additions are the new scope's own resources, named with the scope reference
suffix and therefore incapable of colliding with the default scope:

```text
ditto-capa-controller-dsc-01m00k3nvmkhr6b5a8yh91j7ff
ditto-capa-nodes-dsc-01m00k3nvmkhr6b5a8yh91j7ff
ditto-cluster-resources-boundary-dsc-01m00k3nvmkhr6b5a8yh91j7ff
/dittocluster/dsc-01m00k3nvmkhr6b5a8yh91j7ff/
```

The changes are the default scope's policies, and each renders as its entire
policy body being removed and replaced with `(known after apply)`. This is a
Terraform artifact, not a real change: introducing the new module marks the data
sources that build those policy documents as "will be read during apply", so
Terraform cannot render the resulting content at plan time. When applied, the
documents are recomputed identically and AWS is never called.

> [!IMPORTANT]
> Every other instruction in this document tells you to stop when a plan touches
> resources you did not intend to change. This is the documented exception. Do
> not abort solely because the default scope's policies appear in the change set
> with unknown content.

Verify rather than trust. Before applying, record the current policy versions:

```bash
for name in \
  control-plane.cluster-api-provider-aws.sigs.k8s.io \
  control-plane-tags.cluster-api-provider-aws.sigs.k8s.io \
  ditto-capa-controller-policy \
  ditto-capa-controller-network-policy \
  nodes.cluster-api-provider-aws.sigs.k8s.io \
  ditto-cluster-resources-boundary-policy \
  ditto-iam-trust-editor-policy
do
  arn=$(aws --profile "$aws_profile" iam list-policies --scope Local \
    --query "Policies[?PolicyName=='$name'].Arn" --output text)
  ver=$(aws --profile "$aws_profile" iam get-policy --policy-arn "$arn" \
    --query 'Policy.DefaultVersionId' --output text)
  aws --profile "$aws_profile" iam get-policy-version \
    --policy-arn "$arn" --version-id "$ver" \
    --query 'PolicyVersion.Document' --output json > "$name.before.json"
  printf '%s\t%s\n' "$ver" "$name"
done
```

Apply, then repeat with `.after.json` and diff. Unchanged policies keep the same
default version ID and produce no diff — a new policy version would appear as an
incremented `v` number. Stop and investigate if any default-scope policy's
version ID advances, or if a diff is not empty.

Still refuse to continue if the plan contains any deletion, any replacement, or
additions that are not suffixed with the new scope reference.

### Known limitation: `dittocloud`-mode VPCs in a scope

A scope with `vpc.mode: dittocloud` cannot currently be planned. Terraform fails
before producing a plan:

```text
Error: Invalid count argument

The "count" value depends on resource attributes that cannot be determined
until apply, so Terraform cannot predict how many instances will be created.
```

The failure repeats for public and private subnets, both route tables, and the
internet gateway. Because the plan covers every scope in the file, one such
scope blocks all subsequent operations on that state, including operations on
healthy scopes.

Until this is fixed, use `vpc.mode: existing` with a VPC you have already
created — see [Bring Your Own VPC](./bring-your-own-vpc.md) — or `vpc.mode:
capi`.

If you have already added an unplannable scope, remove it from the scopes file
and restore the file from state:

```bash
dittocloud bootstrap aws scopes recover \
  --state terraform.tfstate \
  --scopes-file scopes.yaml.recovered
```

`scopes recover` writes only scopes that were successfully applied, so a scope
that never applied is simply absent. Review the recovered file, then use it in
place of the poisoned one.

## Recover a lost scopes file

Every successful scope-mode apply stores the complete normalized configuration
for each scope at:

```text
terraform_data.scope_configuration["<scopeRef>"]
```

These snapshots record the last applied configuration. They do not include
edits that existed only in a lost, unapplied YAML file. If the local scopes file
is lost, recover the applied configuration into a missing or zero-length
destination:

```bash
dittocloud bootstrap aws scopes recover \
  --state terraform.tfstate \
  --scopes-file scopes.yaml
```

Recovery holds the state-operation lock and scopes-file lock, validates every
snapshot against the immutable scope registry and applied tag-policy markers,
reconstructs scopes in lexical `scopeRef` order, and writes the file atomically
with `0600` permissions. It never initializes Terraform, refreshes providers,
plans, applies, imports, or changes state. Review the recovered file and run a
separate normal bootstrap command.

The command refuses to overwrite any non-empty destination. It also fails
closed when a snapshot is missing, malformed, uses an unsupported schema, or
disagrees with registry identity, default-scope identity, or applied policy
version. Registry-backed states created before configuration snapshots were
available require manual recovery followed by a successful normal scope-mode
apply; Dittocloud does not guess missing intent from resource outputs.

## Verify readiness for single-cluster lockdown

Scope tag-policy version `0` is compatibility mode. It supports zero, one, or
multiple clusters in the scope VPC and does not require `clusterName`. Version
`1` is the secure single-cluster mode and requires one exact `clusterName`.

Before a version-0 scope can be considered for version `1`, run the read-only
verification command. When the YAML does not yet persist a cluster name, pass
the candidate explicitly:

```bash
dittocloud bootstrap aws scopes tags verify \
  --state terraform.tfstate \
  --scopes-file scopes.yaml \
  --scope-ref dsc-... \
  --cluster-name iam-test-timc \
  --aws-profile customer-account
```

The verifier derives Dittocloud-managed resources from the selected applied
state and requires their live `ditto.live/scope-ref` tag to match exactly. It
discovers single-cluster resources using the controllers' native identities:

```text
kubernetes.io/cluster/<clusterName> = owned
sigs.k8s.io/cluster-api-provider-aws/cluster/<clusterName> = owned
elbv2.k8s.aws/cluster = <clusterName>
```

These identities are verified where they exist; they are not all prerequisites.
`elbv2.k8s.aws/cluster` is applied by the AWS Load Balancer Controller, so a
cluster that has not yet published a load-balanced Service will not carry it.
Its absence does not block version `1`. The command reports which keys it
verified:

```text
Verified native ownership keys: kubernetes.io/cluster/<clusterName>,
                                sigs.k8s.io/cluster-api-provider-aws/cluster/<clusterName>
```

You do not need to deploy a workload in order to lock down IAM. You do need a
cluster: with no cluster in the scope VPC there are no native identities to
verify, and the scope stays at version `0`.

For an EKS scope it also resolves the exact named EKS cluster. The command
verifies that the active AWS credentials match the account recorded by the
state, queries only the scope Region, rejects conflicting scope or owned-cluster
tags, and reports customer-owned and Region/account singleton exclusions. Its
resource-class catalog and AWS readers are built into Dittocloud; no separate
inventory file is used.

Verification holds the state-operation lock and then the scopes-file lock. It
does not initialize Terraform, refresh or change state, mutate AWS, edit the
scopes file, or enable version `1`. A shared scope can remain at version `0`
indefinitely.

To enable the reviewed single-cluster configuration, repeat the same command
with `--enable`:

```bash
dittocloud bootstrap aws scopes tags verify \
  --state terraform.tfstate \
  --scopes-file scopes.yaml \
  --scope-ref dsc-... \
  --cluster-name iam-test-timc \
  --aws-profile customer-account \
  --enable
```

The command repeats live verification and then atomically updates only that
scope in `scopes.yaml`. It persists the candidate `clusterName` when needed and
changes `scopeTagPolicyVersion` from `0` to `1`; it still runs no Terraform
lifecycle and makes no AWS change. Existing YAML ordering and comments are
preserved.

Review the file, then run normal scope mode with `--dry-run`. Because state
still records applied policy version `0`, Dittocloud repeats the complete live
inventory verification before Terraform planning. The non-dry-run command
verifies once before planning and again after confirmation, immediately before
applying. Either failure stops before the corresponding Terraform operation.
Only a successful apply advances the state marker and tightens IAM.

At version `0`, a recorded `clusterName` does not enable cluster-specific IAM.
At version `1`, IAM receives the exact name and uses the verified Kubernetes,
CAPA, and load-balancer native tags. Downgrading version `1` to `0` and changing
its applied `clusterName` are rejected. Direct Terraform version-1 enablement
is also rejected; the supported transition is the Dittocloud CLI workflow.

One migration bridge is deliberately narrower: when legacy Terraform state
proves that the default deployment already has phase-2 cluster-specific IAM,
the CLI preserves those existing conditions while the newly registered scope
marker is still version `0`. This prevents migration from temporarily
broadening access. The bridge is derived from the existing IAM policy state,
cannot be selected through scope YAML, and is removed naturally when the scope
completes verified version-1 enablement.

## Roll back a registry seed or import

Rollback restores the exact pre-operation state, so it also discards every
successful state change made after that backup. Use a migration backup only
before any later successful normal bootstrap, scope import, or other state
operation. If later work exists, stop and choose the correct newer recovery
point instead.

1. Stop all Dittocloud and Terraform operations using the selected state.
2. Locate the backup and `.manifest.json` paths printed by the failed or
   completed migration.
3. Confirm that the manifest's `canonicalStatePath` is the state being
   recovered and that its `stateSha256` matches the backup.
4. Preserve the current state, then atomically replace it with a `0600` copy of
   the backup:

```bash
state=/absolute/path/to/terraform.tfstate
backup=/absolute/path/to/terraform.tfstate.dittocloud-backup-YYYYMMDDTHHMMSS.NNNNNNNNNZ
manifest="${backup}.manifest.json"
recovery_copy="${state}.before-rollback-$(date -u +%Y%m%dT%H%M%SZ)"
restore_tmp="${state}.rollback-tmp"

jq -r '.canonicalStatePath, .stateBackupPath, .stateSha256' "$manifest"
sha256sum "$backup"
cp -p -- "$state" "$recovery_copy"
install -m 600 -- "$backup" "$restore_tmp"
mv -- "$restore_tmp" "$state"
```

After a registry-seed rollback, the restored state is a legacy state again.
Keep the generated scope file for review, but use the legacy bootstrap flags
until the seed is retried successfully. After an import rollback, rerun the
scope-mode preflight and imports from the restored registry-backed state.

If an apply failed and Dittocloud reported that valid partial state was saved,
retrying from that state is normally safer than rolling back: the retained
scope registry prevents accidental identity reuse and preserves successfully
created resources for reconciliation.
