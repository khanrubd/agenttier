# AgentTier EKS Evaluation Module

> **⚠️ FOR EVALUATION ONLY — NOT FOR PRODUCTION USE**

This Terraform module provisions a minimal EKS cluster with all AgentTier prerequisites for evaluation purposes.

## Cost Estimate

| Resource | Monthly Cost |
|----------|-------------|
| EKS Control Plane | ~$73 |
| 2x m5.xlarge nodes | ~$140 |
| NAT Gateway | ~$32 |
| EBS volumes | ~$10 |
| **Total** | **~$255/month** |

## Prerequisites

- AWS account with admin access
- Terraform >= 1.5
- AWS CLI configured (`aws configure`)
- kubectl installed

## Usage

```bash
# Initialize
terraform init

# Review plan
terraform plan

# Create cluster (~15 minutes)
terraform apply

# Configure kubectl
$(terraform output -raw kubeconfig_command)

# Install AgentTier
helm install agenttier agenttier/agenttier \
  --set auth.oidc.issuerUrl=<your-oidc-issuer> \
  --set auth.oidc.clientId=<your-client-id>

# Verify
kubectl get sandboxtemplates
```

## Teardown

```bash
# Remove AgentTier first
helm uninstall agenttier

# Destroy infrastructure
terraform destroy
```

**Important:** Run `terraform destroy` when done to avoid ongoing charges.

## What's Included

- VPC with public/private subnets (2 AZs)
- EKS cluster v1.30 with managed node group
- EBS CSI driver (gp3 StorageClass)
- IRSA role for AgentTier controller
- Calico CNI for NetworkPolicy support (install separately)

## What's NOT Included (install manually)

- NGINX Ingress Controller: `helm install ingress-nginx ingress-nginx/ingress-nginx`
- cert-manager: `helm install cert-manager jetstack/cert-manager --set installCRDs=true`
- Calico: `kubectl apply -f https://raw.githubusercontent.com/projectcalico/calico/v3.28.0/manifests/calico.yaml`
