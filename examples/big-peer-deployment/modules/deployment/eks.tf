# Discover current partition/account for partition-safe ARNs.
data "aws_partition" "current" {}
data "aws_caller_identity" "current" {}

# EKS cluster and managed node groups. The cluster control plane is created in
# the deployment's region and placed into the deployment's VPC subnets.
module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "21.25.0"

  name               = var.deployment.cluster.name
  kubernetes_version = var.deployment.cluster.version

  vpc_id     = local.vpc_id
  subnet_ids = concat(local.private_subnet_ids, local.public_subnet_ids)

  endpoint_public_access  = true
  endpoint_private_access = true

  enable_irsa = true

  # Core add-ons required for a functional cluster. vpc-cni and kube-proxy must
  # land before compute: without a CNI, nodes never reach Ready and the managed
  # node group fails with NodeCreationFailure. coredns needs a node to schedule
  # on, so it stays after.
  addons = {
    coredns = {}
    kube-proxy = {
      before_compute = true
    }
    vpc-cni = {
      before_compute = true
    }
    # Pod Identity must exist before any addon that consumes it.
    eks-pod-identity-agent = {
      before_compute = true
    }
    # Strimzi/Kafka and Big Peer both need PVCs, and k8s >= 1.31 has no in-tree
    # EBS provisioner, so without this every PVC stays Pending.
    aws-ebs-csi-driver = {
      pod_identity_association = [{
        role_arn        = aws_iam_role.ebs_csi.arn
        service_account = "ebs-csi-controller-sa"
      }]
    }
  }

  eks_managed_node_groups = {
    for name, ng in var.deployment.cluster.managed_node_groups : name => {
      instance_types = ng.instance_types
      min_size       = ng.min_size
      max_size       = ng.max_size
      desired_size   = ng.desired_size
      subnet_ids     = local.private_subnet_ids
    }
  }

  # Admin access entries for human operators (e.g. SSO roles). The upstream
  # module's cluster-creator permissions don't resolve for assumed-role
  # sessions (SSO), so grant cluster admin explicitly.
  access_entries = {
    for idx, arn in var.deployment.cluster.admin_principal_arns : "admin_${idx}" => {
      principal_arn = arn
      policy_associations = {
        admin = {
          policy_arn = "arn:${data.aws_partition.current.partition}:eks::aws:cluster-access-policy/AmazonEKSClusterAdminPolicy"
          access_scope = {
            type = "cluster"
          }
        }
      }
    }
  }

  # Explicit KMS key admins/owners prevent the upstream module from using
  # aws_iam_session_context, which can resolve to an invalid assumed-role
  # principal (e.g. an SSO session) and cause MalformedPolicyDocumentException.
  kms_key_administrators = ["arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:root"]
  kms_key_owners         = ["arn:${data.aws_partition.current.partition}:iam::${data.aws_caller_identity.current.account_id}:root"]

  tags = var.tags
}

# Pod Identity role for the EBS CSI driver controller.
data "aws_iam_policy_document" "ebs_csi_trust" {
  statement {
    actions = ["sts:AssumeRole", "sts:TagSession"]
    principals {
      type        = "Service"
      identifiers = ["pods.eks.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "ebs_csi" {
  name               = "${var.deployment.cluster.name}-ebs-csi-driver"
  assume_role_policy = data.aws_iam_policy_document.ebs_csi_trust.json
  tags               = var.tags
}

resource "aws_iam_role_policy_attachment" "ebs_csi" {
  role       = aws_iam_role.ebs_csi.name
  policy_arn = "arn:${data.aws_partition.current.partition}:iam::aws:policy/AmazonEBSCSIDriverPolicyV2"
}
