#!/bin/bash

# This script run integration tests on the EKS VPC Resource Controller
# This is not intended to run integration tests when controller is running
# on the data plane for development and testing purposes. The script expects
# an EKS Cluster with atlest 3 Windows and Linux Nodes to be pre-created to
# run the Canary tests. This scrip is invoked from external sources.

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
INTEGRATION_TEST_DIR="$SCRIPT_DIR/../../test/integration"
SECONDS=0
VPC_CNI_ADDON_NAME="vpc-cni"

echo "Running VPC Resource Controller integration test with the following variables
KUBE CONFIG: $KUBE_CONFIG_PATH
CLUSTER_NAME: $CLUSTER_NAME
REGION: $REGION"

if [[ -n "${ENDPOINT}" ]]; then
  ENDPOINT_FLAG="--endpoint $ENDPOINT"
fi

# Default Proxy is not allowed in China Region
if [[ $REGION == "cn-north-1" || $REGION == "cn-northwest-1" ]]; then
  go env -w GOPROXY=https://goproxy.cn,direct
  go env -w GOSUMDB=sum.golang.google.cn
fi

function load_addon_details() {
  echo "loading $VPC_CNI_ADDON_NAME addon details"
  DESCRIBE_ADDON_VERSIONS=$(aws eks describe-addon-versions --addon-name $VPC_CNI_ADDON_NAME --kubernetes-version "$K8S_VERSION")

  LATEST_ADDON_VERSION=$(echo "$DESCRIBE_ADDON_VERSIONS" | jq '.addons[0].addonVersions[0].addonVersion' -r)
  DEFAULT_ADDON_VERSION=$(echo "$DESCRIBE_ADDON_VERSIONS" | jq -r '.addons[].addonVersions[] | select(.compatibilities[0].defaultVersion == true) | .addonVersion')
}

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

function wait_for_addon_status() {
  local expected_status=$1

  if [ "$expected_status" =  "DELETED" ]; then
    while $(aws eks describe-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name $VPC_CNI_ADDON_NAME --region $REGION); do
      echo "addon is still not deleted"
      sleep 5
    done
    echo "addon deleted"
    return
  fi

  while true
  do
    STATUS=$(aws eks describe-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name $VPC_CNI_ADDON_NAME --region $REGION | jq -r '.addon.status')
    if [ "$STATUS" = "$expected_status" ]; then
      echo "addon status matches expected status"
      return
    fi
    echo "addon status is not equal to $expected_status"
    sleep 5
  done
}

function install_add_on() {
  local new_addon_version=$1

  if DESCRIBE_ADDON=$(aws eks describe-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name $VPC_CNI_ADDON_NAME --region $REGION); then
    local current_addon_version=$(echo "$DESCRIBE_ADDON" | jq '.addon.addonVersion' -r)
    if [ "$new_addon_version" != "$current_addon_version" ]; then
      echo "deleting the $current_addon_version to install $new_addon_version"
      aws eks delete-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name "$VPC_CNI_ADDON_NAME" --region $$REGION
      wait_for_addon_status "DELETED"
    else
      echo "addon version $current_addon_version already installed"
      return
    fi
  fi

  echo "installing addon $new_addon_version"
  aws eks create-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name $VPC_CNI_ADDON_NAME --resolve-conflicts OVERWRITE --addon-version $new_addon_version --region $REGION
  wait_for_addon_status "ACTIVE"
}

function set_env_aws_node() {
  local KEY=$1
  local VAL=$2

  echo "Setting environment variable $KEY to $VAL on aws-node"
  kubectl set env daemonset aws-node -n kube-system $KEY=$VAL
  kubectl rollout status ds -n kube-system aws-node
}

function run_canary_tests() {
  # For each component, we want to cover the most important test cases. We also don't want to take more than 30 minutes
  # per repository as these tests are run sequentially along with tests from other repositories
  # Currently the overall execution time is ~50 minutes and we will reduce it in future
  (cd $INTEGRATION_TEST_DIR/perpodsg && CGO_ENABLED=0 ginkgo --focus="CANARY" -v -timeout 15m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID)
  (cd $INTEGRATION_TEST_DIR/windows && CGO_ENABLED=0 ginkgo --focus="CANARY" -v -timeout 27m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID)
  (cd $INTEGRATION_TEST_DIR/webhook && CGO_ENABLED=0 ginkgo --focus="CANARY" -v -timeout 5m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID)
}

echo "Starting the ginkgo test suite"
load_cluster_details

if [[ "$K8S_VERSION" == "1.17" ]]; then
  kubectl apply -f https://raw.githubusercontent.com/aws/amazon-vpc-cni-k8s/release-1.10/config/master/aws-k8s-cni.yaml
else
  # Addons is supported from 1.18 onwards
  load_addon_details
  # TODO: v1.7.5 (current default) restarts continiously if IMDS goes out of sync,
  # the issue is mitigated from v.1.8.0 onwards, once the default addon is updated
  # to v1.8.0+ we can start using default version.
  # See: https://github.com/aws/amazon-vpc-cni-k8s/issues/1340
  install_add_on "$LATEST_ADDON_VERSION"
fi

attach_controller_policy_cluster_role
set_env_aws_node "ENABLE_POD_ENI" "true"
run_canary_tests
set_env_aws_node "ENABLE_POD_ENI" "false"
detach_controller_policy_cluster_role

echo "Successfully ran all tests in $(($SECONDS / 60)) minutes and $(($SECONDS % 60)) seconds"
