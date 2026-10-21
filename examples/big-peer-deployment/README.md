# Big Peer on Amazon EKS

A complete, runnable example of standing up a Ditto **Big Peer** on AWS, in two
parts:

| | What it does | Tool |
|---|---|---|
| **Part 1 — Infrastructure** | VPC, EKS cluster, node groups, core EKS add-ons | `terraform` |
| **Part 2 — Deployment** | cert-manager, Envoy Gateway, Strimzi, Ditto Operator, Big Peer | `make` |

Part 1 hands off to Part 2 through a generated `.env`, so the cluster name and
region are configured in exactly one place: `config.yaml`.

> This example is intended for an isolated AWS account. It is not yet wired into
> the `dittocloud` CLI.

---

## Quick start

```bash
cd examples/big-peer-deployment

# Part 1 - infrastructure
terraform init
terraform apply                      # writes .env

# Part 2 - deployment
cp .env.example .env.local           # set AWS_PROFILE, then edit to taste
make all
```

`make all` runs `kubeconfig → bootstrap → bigpeer → app → smoke` and finishes by
inserting and reading back a document through the gateway.

## Requirements

`terraform` ≥ 1.5, `awscli` v2, `kubectl`, `helm` ≥ 3, `jq`.

Part 2 also needs a YAML parser **only if you run `make` before
`terraform apply`** — `yq`, Python with PyYAML, or Ruby (ships with macOS).
After an apply, `.env` already exists and no parser is needed.

---

# Part 1 — Infrastructure (Terraform)

Edit `config.yaml`. Each top-level key under `deployments` is a complete
VPC + EKS stack.

```yaml
deployments:
  big-peer-dev:
    region: us-east-1
    vpc:
      create: true
      cidr: 10.0.0.0/16
      availability_zones: ["us-east-1a", "us-east-1b", "us-east-1c"]
      private_subnets: ["10.0.1.0/24", "10.0.2.0/24", "10.0.3.0/24"]
      public_subnets: ["10.0.101.0/24", "10.0.102.0/24", "10.0.103.0/24"]
    cluster:
      name: big-peer-dev
      version: "1.30"
      # SSO/IAM roles granted cluster-admin via EKS access entries. Use the
      # full ARN including the aws-reserved path - EKS rejects the
      # path-stripped form as "invalid principal".
      admin_principal_arns:
        - arn:aws:iam::123456789012:role/aws-reserved/sso.amazonaws.com/us-east-1/AWSReservedSSO_Admin_0123456789abcdef
      managed_node_groups:
        default:
          instance_types: ["m6i.large"]
          min_size: 3
          max_size: 8
          desired_size: 3
```

### Existing VPC

Set `vpc.create: false` and supply `vpc_id`, `private_subnet_ids` and
`public_subnet_ids`. No VPC resources are created or modified. See
[`docs/bring-your-own-vpc.md`](../../docs/bring-your-own-vpc.md); at minimum the
VPC must have subnets in three AZs, `enableDnsSupport` + `enableDnsHostnames`,
`kubernetes.io/role/internal-elb=1` on private subnets and
`kubernetes.io/role/elb=1` on public ones, and a route to AWS APIs and
container registries.

### Add-ons installed

| Add-on | Notes |
|---|---|
| `vpc-cni`, `kube-proxy` | `before_compute = true` |
| `coredns` | after compute — it needs a node to schedule on |
| `eks-pod-identity-agent` | prerequisite for the EBS CSI driver's IAM |
| `aws-ebs-csi-driver` | pod identity + `AmazonEBSCSIDriverPolicyV2` |

**Why `before_compute` matters.** The upstream module gives `aws_eks_addon` a
`depends_on` covering the node groups, so by default add-ons install *after*
compute. A node cannot reach `Ready` without a CNI, so the node group fails with
`NodeCreationFailure` and the apply dies before the CNI is ever installed. Marking
`vpc-cni` and `kube-proxy` as `before_compute` breaks that deadlock.

**Why the EBS CSI driver is not optional.** Strimzi/Kafka and Big Peer both need
PersistentVolumes, and Kubernetes ≥ 1.31 has no in-tree EBS provisioner. Without
it every PVC stays `Pending` and Part 2 stalls at Strimzi.

### Multiple deployments

`.env` describes one cluster. With more than one deployment defined, pick which:

```bash
terraform apply -var primary_deployment=big-peer-dev
```

To target a different deployment without re-running Terraform, override the
Makefile's cluster selection on the command line:

```bash
make DEPLOYMENT=big-peer-prod CLUSTER=big-peer-prod REGION=us-west-2 all
```

---

# Part 2 — Deployment (Make)

```bash
make            # list targets and show resolved config
make all        # everything, end to end
```

### Targets

| Target | |
|---|---|
| `kubeconfig` | Write `./.kube/config` for the cluster |
| `preflight` | Verify tooling, reachability, node capacity, EBS CSI |
| `storage` | Default `gp3` StorageClass |
| `cert-manager` | cert-manager + self-signed `ClusterIssuer` |
| `envoy` | Envoy Gateway + internal (and optional external) gateways |
| `strimzi` | Strimzi Kafka operator |
| `operator` | Ditto Operator |
| `bootstrap` | All of the above |
| `bigpeer` | BigPeer CR + its HTTPRoutes |
| `portal` | Portal UI behind the internal gateway (`PORTAL=1`) |
| `app` | Create an app + API key → `.local/app.env` |
| `smoke` | Insert and query a document through the gateway |
| `status` / `endpoint` | Show component state / gateway addresses |
| `teardown` | Remove everything Make installed (leaves Terraform alone) |

### Configuration

Resolved lowest-precedence first:

1. Defaults in the `Makefile`
2. **`.env`** — generated by `terraform apply`. Holds `DEPLOYMENT`, `CLUSTER`,
   `REGION` only. Overwritten on every apply, so never edit it. Before the first
   apply, `make` derives the same three values from `config.yaml`.
3. **`.env.local`** — yours. Copy from `.env.example`. Terraform never touches it.
4. The command line — `make EXTERNAL=1 envoy`

Notable settings (all documented in `.env.example`): `AWS_PROFILE`,
`BIGPEER_NAME`, `BIGPEER_VERSION`, `SHARED_TOKEN`, `EXTERNAL`,
`ENVOY_REPLICAS`, `KAFKA_EXTERNAL_LISTENER`, and the four pinned chart versions.

```bash
make config     # print everything as resolved
```

### kubeconfig is self-contained

Every target writes and reads `./.kube/config` and exports `KUBECONFIG` to it.
Nothing here touches `~/.kube/config`, so running this example cannot disturb
whatever cluster you have selected globally. Every `kubectl` call also pins
`--context` explicitly.

### Re-running is cheap

Each install target checks whether its component is already deployed *and
healthy*, and skips if so:

```
 --> cert-manager v1.21.1 (already installed, skipping)
```

`make FORCE=1 bootstrap` reinstalls regardless. Toggling `EXTERNAL` is always
honoured — the Envoy target compares the installed gateways against the flag
rather than skipping blindly.

### Networking

Envoy Gateway, following the pattern used in `cloud-infra-apps`: an
`EnvoyProxy` → `GatewayClass` → `Gateway` triple per exposure.

- **Internal** (`int-envoy-proxy`) — always created, internal NLB.
- **External** (`ext-envoy-proxy`) — only with `EXTERNAL=1`. Off by default:
  without an ACM certificate it serves plaintext HTTP. `EXTERNAL=0` actively
  removes it, so it is a real off switch.

NLBs use the legacy cloud-controller-manager annotations
(`aws-load-balancer-type: nlb`), which EKS honours natively — the AWS Load
Balancer Controller is **not** required and is not installed by this example.

Every service this example creates is either `ClusterIP` or an **internal** NLB.
`int-envoy-proxy` is the only load balancer at `EXTERNAL=0`, and Big Peer, the
portal and the operator API all share it. Nothing is reachable from the
internet unless you explicitly set `EXTERNAL=1`.

Big Peer is reached through three `HTTPRoute` rules, ordered by longest path
prefix:

| Path | Backend |
|---|---|
| `/_ditto/auth` | `ditto-<name>-auth-server:7070` |
| `/_ditto` | `ditto-<name>-subscription:7070` |
| `/` | `ditto-<name>-api:8080` |

> The quickstart's `spec.network.ingress.host` makes the operator create a
> Kubernetes `Ingress`. Envoy Gateway implements the Gateway API and does not
> serve `Ingress`, so that field is omitted and routes are authored directly.
> Operator `0.17.2` serves only `v1alpha1`; the `spec.network.mode: GatewayApi`
> field referenced in the CRD docs arrives in `v1alpha2`.

### Portal (optional)

The self-managed portal UI ships inside the `ditto-operator` chart
(`portal.enabled`, default `false`) as
`quay.io/ditto-external/portal-self-managed`. It is **not** covered by the
operator quickstart. Enable it with:

```bash
PORTAL=1 make portal          # or: PORTAL=1 make bootstrap
```

Like everything else here it sits behind the **internal** gateway and gets no
load balancer of its own — the portal, operator API and Big Peer API are all
served from a single hostname on the existing internal NLB:

| Path on `$PORTAL_HOST` | Backend |
|---|---|
| `/operator-api` | `ditto-operator:80` (prefix stripped) |
| `/big-peer` | `ditto-<name>-api:8080` (prefix stripped) |
| `/` | `ditto-operator-portal:80` |

One hostname is deliberate. The chart's default topology puts the portal and
the operator API on separate hosts via two `Ingress` objects, which makes every
API call cross-origin and needs a CORS policy on the API. Serving both from one
origin avoids that entirely. Both chart `Ingress` templates are left disabled,
since Envoy Gateway implements the Gateway API and does not serve `Ingress`.

Nothing registers `$PORTAL_HOST` in DNS. Reach it from inside the VPC, or
locally by port-forwarding the Envoy service and supplying the `Host` header
(`make portal` prints the exact commands).

> **The operator API is unauthenticated by default.** The chart ships
> `operator.api.authMode: disabled` — *"API is open to all requests"* — and the
> portal's rendered config carries `AUTH_MODE: "disabled"`. That API can create
> apps and mint API keys. It is only reachable inside the VPC here, but anyone
> on the network can use it. The chart documents `k8s-token` as the alternative;
> the portal templates also branch on a `better-auth` mode that the values file
> does not document. Set an auth mode before treating this as anything but a
> lab.

### Kafka and the Ingress trap

Big Peer defaults to an **`ingress`-type external listener** for Kafka
(`spec.cdc.network.kafkaExternalListener.enabled` defaults to `true`). Strimzi
then blocks its entire reconcile waiting for that Ingress to become addressable.
Envoy Gateway implements the Gateway API and does **not** serve `Ingress`, so
nothing ever assigns an address: the Kafka broker is never created, its PVC sits
on `WaitForFirstConsumer`, and every Big Peer pod stays `0/1` waiting for Kafka.
The symptom looks like a storage or capacity problem, but is neither.

`KAFKA_EXTERNAL_LISTENER=0` (the default here) disables it. The listener is only
needed for **Kafka Data Bridges**; set it to `1` only if you have also installed
an Ingress controller.

### Credentials

`make app` writes `.local/app.env` (mode `0600`, gitignored) with the app ID and
API key. `make smoke` reads it. Neither is printed in full.

`SHARED_TOKEN` defaults to `abc123` from the quickstart and grants full
read/write. Change it for anything that is not a throwaway cluster.

---

## Sizing and autoscaling

**This cluster does not autoscale.** `max_size` is only the ceiling of the node
group's Auto Scaling Group — nothing raises the desired count when pods go
Pending. EKS managed node groups do not include an autoscaler, and this example
installs neither Cluster Autoscaler nor Karpenter.

**`desired_size` applies only at creation.** The upstream EKS module sets
`ignore_changes = [scaling_config[0].desired_size]` so an autoscaler can own the
count without Terraform reverting it. With no autoscaler installed, editing
`desired_size` in `config.yaml` after the node group exists produces
`No changes` on plan — resize the node group through the EKS API or console.
`config.yaml` still governs the size of a newly created node group.

The full stack — cert-manager, Envoy, Strimzi, Kafka, the operator and Big Peer —
is a tight fit on a single 2 vCPU node. `make preflight` warns below three Ready
nodes. If pods do not come up, `make bigpeer` distinguishes the three causes
(Kafka blocked, PVC Pending, insufficient capacity) rather than guessing.

## Troubleshooting

| Symptom | Cause |
|---|---|
| Nodes `NotReady`, node group `CREATE_FAILED` | No CNI. Check `vpc-cni` is installed and marked `before_compute`. |
| PVCs stuck `Pending` | EBS CSI driver missing, or no default StorageClass — run `make storage`. |
| `kubectl` returns 401 | Your IAM principal has no access entry. Add it to `admin_principal_arns` and re-apply. |
| Pods `Pending`, no PVC issue | Not enough capacity. Raise `desired_size` — there is no autoscaler. |
| Kafka never starts; `Exceeded timeout … waiting for Ingress … to be addressable` | Big Peer's Kafka external listener is on. See below. |
| Gateway address `<pending>` | NLB still provisioning, or subnets missing their `kubernetes.io/role/*elb` tags. |

## Cleanup

```bash
make teardown       # in-cluster software (prompts for the cluster name)
terraform destroy   # infrastructure
make clean          # local .env, .kube/, .local/
```

## Layout

```
big-peer-deployment/
├── config.yaml            # deployment definitions - the single source of truth
├── main.tf                # parses config.yaml, orchestrates deployment modules
├── env.tf                 # writes .env for the Makefile
├── providers.tf variables.tf outputs.tf
├── modules/deployment/    # one VPC + EKS stack per deployment
│   ├── vpc.tf eks.tf vpc-endpoints.tf outputs.tf variables.tf
├── Makefile               # Part 2 entry point
├── .env.example           # template for .env.local
└── scripts/
    ├── lib.sh             # shared helpers, skip checks, waits
    ├── config.sh          # derive .env from config.yaml (pre-apply fallback)
    ├── preflight.sh storage.sh cert-manager.sh envoy-gateway.sh
    ├── strimzi.sh ditto-operator.sh bigpeer.sh
    └── app.sh smoke.sh status.sh teardown.sh
```
