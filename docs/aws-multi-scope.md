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

A Dittocloud-managed VPC carries its own subnet sizing and, optionally, the
secondary block that holds every workload tier:

```yaml
dsc-01k2m8g7n4p6q9r3t5v8x1y2z4:
  region: us-west-2
  clusterType: eks
  clusterName: valet-dev
  scopeTagPolicyVersion: 0
  vpc:
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    secondaryCidr: 100.64.0.0/16
    publicSubnetNetmask: 24
    privateSubnetNetmask: 23
```

`cidr` is a DMZ: it carries load balancers, NAT gateways, and explicitly placed
EC2, and it is the only surface a peered VPC sees. `secondaryCidr` carries pod,
node, and database capacity and is the same block on every VPC, because it is
never routed or advertised outside its own VPC so identical blocks never meet.
Two VPCs that share it cannot be peered at all — AWS rejects a peering connection
on any overlapping CIDR, secondary blocks included, and checks the whole set at
creation time — so cross-VPC connectivity is PrivateLink or VPC Lattice, or a
transit gateway propagating only the primaries. It must be one of the 64 `/16`
blocks inside `100.64.0.0/10`. Three of those blocks are not allocatable because Valet clusters
already use them for in-cluster pod and Service addressing: `100.66.0.0/16` is the
kubeadm cluster range, and `100.80.0.0/16` and `100.81.0.0/16` are the pod and
Service CIDRs every self-managed AWS cluster is built with.

`publicSubnetNetmask` and `privateSubnetNetmask` default to `24` and `23`.
**Every subnet CIDR is derived from them, so changing either renumbers live
subnets** and recreates the NAT gateways, nodes, and load balancers with them. A
VPC created before the DMZ split must pin `publicSubnetNetmask: 22` and
`privateSubnetNetmask: 18`; `scopes generate` reads those values back from the
subnets already in state, and `scopes recover` pins them when it reads a
configuration snapshot written before the split.

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

A greenfield Dittocloud-managed VPC takes `--default` and the workload block:

```bash
dittocloud bootstrap aws scopes add \
  --scopes-file scopes.yaml \
  --default \
  --region us-east-1 \
  --cluster-type eks \
  --vpc-mode dittocloud \
  --vpc-name valet \
  --vpc-cidr 10.217.0.0/20 \
  --vpc-secondary-cidr 100.64.0.0/16
```

`--vpc-secondary-cidr` is only valid for `dittocloud` mode; `existing` and `capi`
scopes reject it, because Dittocloud does not own their address space. The same
applies to `--vpc-karpenter-discovery-tag`, which sets the `karpenter.sh/discovery`
value on the node subnets.

That tag is how Karpenter finds where to launch nodes, and Terraform has to own it
because the CAPA controller boundary does not permit the `karpenter.sh` namespace.
Left unset, it falls back to the scope's `clusterName`, and a scope with neither
gets **no discovery tag at all** — Karpenter then falls back to whatever its
`EC2NodeClass` matches next, which for a Valet cluster is the
`kubernetes.io/cluster/*` tag CAPA applies to the DMZ subnets, not the node tier.
Set it explicitly when `scopeTagPolicyVersion` is `0` and the scope holds more than
one cluster, because a single `clusterName` is not meaningful there.

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
