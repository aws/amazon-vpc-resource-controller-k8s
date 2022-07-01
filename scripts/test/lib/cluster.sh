#!/usr/bin/env bash

LIB_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

source "$LIB_DIR"/k8s.sh

if [[ -n "${ENDPOINT}" ]]; then
  ENDPOINT_FLAG="--endpoint $ENDPOINT"
fi

function load_cluster_details() {
  CLUSTER_INFO=$(aws eks describe-cluster --name $CLUSTER_NAME --region $REGION $ENDPOINT_FLAG)
  VPC_ID=$(echo $CLUSTER_INFO | jq -r '.cluster.resourcesVpcConfig.vpcId')
  SERVICE_ROLE_ARN=$(echo $CLUSTER_INFO | jq -r '.cluster.roleArn')
  K8S_VERSION=$(echo $CLUSTER_INFO | jq -r '.cluster.version')
  ROLE_NAME=${SERVICE_ROLE_ARN##*/}

  echo "VPC ID: $VPC_ID, Service Role ARN: $SERVICE_ROLE_ARN, Role Name: $ROLE_NAME"
}

# This operation fails with rate limit exceeded when test is running for multiple K8s
# version at same time, hence we increase the exponential retries on the aws call
function attach_controller_policy_cluster_role() {
  echo "Attaching IAM Policy to Cluster Service Role"
  AWS_MAX_ATTEMPTS=10 aws iam attach-role-policy \
    --policy-arn arn:aws:iam::aws:policy/AmazonEKSVPCResourceController \
    --role-name "$ROLE_NAME" > /dev/null
}

function detach_controller_policy_cluster_role() {
  echo "Detaching the IAM Policy from Cluster Service Role"
  aws iam detach-role-policy \
    --policy-arn arn:aws:iam::aws:policy/AmazonEKSVPCResourceController \
    --role-name $ROLE_NAME > /dev/null
}

function set_env_aws_node() {
  local KEY=$1
  local VAL=$2

  echo "Setting environment variable $KEY to $VAL on aws-node"
  kubectl set env daemonset aws-node -n kube-system $KEY=$VAL
  check_ds_rollout "aws-node" "kube-system"
}