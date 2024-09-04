#!/usr/bin/env bash

# Delete EKS cluster & related resources created via script create-cluster-karpenter.sh
set -eo pipefail

SCRIPTS_DIR=$(cd "$(dirname "$0")" || exit 1; pwd)
source "$SCRIPTS_DIR/lib/common.sh"
check_is_installed helm
check_is_installed eksctl
check_is_installed jq
check_is_installed aws

export KARPENTER_NAMESPACE="kube-system"
export CLUSTER_NAME="${USER}-sgp-scaletest" # Update cluster name if it is different
echo "Uninstalling Karpenter"
helm uninstall karpenter --namespace "${KARPENTER_NAMESPACE}"
echo "Deleting Karpenter CFN stack"
aws cloudformation delete-stack --stack-name "Karpenter-${CLUSTER_NAME}"
aws ec2 describe-launch-templates --filters "Name=tag:karpenter.k8s.aws/cluster,Values=${CLUSTER_NAME}" |
    jq -r ".LaunchTemplates[].LaunchTemplateName" |
    xargs -I{} aws ec2 delete-launch-template --launch-template-name {}
echo "Deleting EKS cluster"
eksctl delete cluster --name "${CLUSTER_NAME}"