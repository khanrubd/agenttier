# AWS Load Balancer Controller.
#
# Installs the controller that reconciles Kubernetes Ingress/Service objects
# into AWS ALBs/NLBs. AgentTier's web-ui Ingress (and any port-forward preview
# Ingresses) depend on this when running with the ALB ingress class.
#
# The controller's ServiceAccount is annotated with the IRSA role from irsa.tf
# so it can call the ELBv2/EC2 APIs without static credentials.

resource "helm_release" "aws_load_balancer_controller" {
  count = var.install_aws_load_balancer_controller ? 1 : 0

  name       = "aws-load-balancer-controller"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"
  version    = var.aws_load_balancer_controller_chart_version
  namespace  = "kube-system"

  set {
    name  = "clusterName"
    value = module.eks.cluster_name
  }

  set {
    name  = "region"
    value = var.region
  }

  set {
    name  = "vpcId"
    value = module.vpc.vpc_id
  }

  set {
    name  = "serviceAccount.create"
    value = "true"
  }

  set {
    name  = "serviceAccount.name"
    value = "aws-load-balancer-controller"
  }

  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = module.lb_controller_irsa.iam_role_arn
  }

  depends_on = [
    module.eks,
    aws_eks_addon.ebs_csi,
  ]
}
