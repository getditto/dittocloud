# EKS managed control plane service role.
#
# CAPA runs with EKSEnableIAM=false, so it does NOT create the EKS control plane
# role itself - it expects a role named exactly
# "eks-controlplane.cluster-api-provider-aws.sigs.k8s.io" to already exist, trusting
# eks.amazonaws.com with AmazonEKSClusterPolicy attached. The kubeadm control plane
# role (control-plane.*, trusts ec2.amazonaws.com) is not a substitute. Only created
# when enable_eks is set.
resource "aws_iam_role" "capa_eks_control_plane" {
  count = var.enable_eks ? 1 : 0
  name  = local.iam_names.eks_control_plane_role
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Principal = { Service = "eks.amazonaws.com" }
        Action    = "sts:AssumeRole"
      }
    ]
  })
  tags = local.tags
}

resource "aws_iam_role_policy_attachment" "capa_eks_control_plane" {
  count      = var.enable_eks ? 1 : 0
  role       = aws_iam_role.capa_eks_control_plane[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"
}
