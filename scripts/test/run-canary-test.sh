#!/bin/bash

# This script run integration tests on the EKS VPC Resource Controller
# This is not intended to run integration tests when controller is running
# on the data plane for development and testing purposes. The script expects
# an EKS Cluster with atlest 3 Windows and Linux Nodes to be pre-created to
# run the Canary tests. This scrip is invoked from external sources.

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
GINKGO_TEST_BUILD_DIR="$SCRIPT_DIR/../../build"
SECONDS=0
VPC_CNI_ADDON_NAME="vpc-cni"

source "$SCRIPT_DIR"/lib/cluster.sh

echo "Running VPC Resource Controller integration test with the following variables
KUBE CONFIG: $KUBE_CONFIG_PATH
CLUSTER_NAME: $CLUSTER_NAME
REGION: $REGION"

# Default Proxy is not allowed in China Region
if [[ $REGION == "cn-north-1" || $REGION == "cn-northwest-1" ]]; then
  go env -w GOPROXY=https://goproxy.cn,direct
  go env -w GOSUMDB=sum.golang.google.cn
fi

function load_addon_details() {
  echo "loading $VPC_CNI_ADDON_NAME addon details"
  DESCRIBE_ADDON_VERSIONS=$(aws eks describe-addon-versions $ENDPOINT_FLAG --addon-name $VPC_CNI_ADDON_NAME --kubernetes-version "$K8S_VERSION")

  LATEST_ADDON_VERSION=$(echo "$DESCRIBE_ADDON_VERSIONS" | jq '.addons[0].addonVersions[0].addonVersion' -r)
  DEFAULT_ADDON_VERSION=$(echo "$DESCRIBE_ADDON_VERSIONS" | jq -r '.addons[].addonVersions[] | select(.compatibilities[0].defaultVersion == true) | .addonVersion')
}

function wait_for_addon_status() {
  local expected_status=$1
  local retry_attempt=0
  if [ "$expected_status" =  "DELETED" ]; then
    while $(aws eks describe-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name $VPC_CNI_ADDON_NAME --region "$REGION"); do
      if [ $retry_attempt -ge 30 ]; then
        echo "failed to delete addon, qutting after too many attempts"
        exit 1
      fi
      echo "addon is still not deleted"
      sleep 5
      ((retry_attempt=retry_attempt+1))
    done
    echo "addon deleted"
    # Even after the addon API Returns an error, if you immediately try to create a new addon it sometimes fails
    sleep 10
    return
  fi

  retry_attempt=0
  while true
  do
    STATUS=$(aws eks describe-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name $VPC_CNI_ADDON_NAME --region "$REGION" | jq -r '.addon.status')
    if [ "$STATUS" = "$expected_status" ]; then
      echo "addon status matches expected status"
      return
    fi
    # We have seen the canary test get stuck, we don't know what causes this but most likely suspect is
    # the addon update/delete attempts. So adding limited retries.
    if [ $retry_attempt -ge 30 ]; then
      echo "failed to get desired add-on status: $STATUS, qutting after too many attempts"
      exit 1
    fi
    echo "addon status is not equal to $expected_status"
    sleep 10
    ((retry_attempt=retry_attempt+1))
  done
}

function install_add_on() {
  local new_addon_version=$1
  if [[ -z "$new_addon_version" || "$new_addon_version" == "null" ]]; then
    echo "addon information for $VPC_CNI_ADDON_NAME not available, skipping EKS-managed addon installation. Tests will run against self-managed addon."
    return
  fi

  if DESCRIBE_ADDON=$(aws eks describe-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name $VPC_CNI_ADDON_NAME --region $REGION); then
    local current_addon_version=$(echo "$DESCRIBE_ADDON" | jq '.addon.addonVersion' -r)
    if [ "$new_addon_version" != "$current_addon_version" ]; then
      echo "deleting the $current_addon_version to install $new_addon_version"
      aws eks delete-addon $ENDPOINT_FLAG --cluster-name "$CLUSTER_NAME" --addon-name "$VPC_CNI_ADDON_NAME" --region $REGION
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

function run_canary_tests() {
  if [[ -z "${SKIP_MAKE_TEST_BINARIES}" ]]; then
    echo "making ginkgo test binaries"
    (cd $SCRIPT_DIR/../.. && make build-test-binaries)
  else
    echo "skipping making ginkgo test binaries"
  fi

  # For each component, we want to cover the most important test cases. We also don't want to take more than 30 minutes
  # per repository as these tests are run sequentially along with tests from other repositories
  # Currently the overall execution time is ~50 minutes and we will reduce it in future
  (CGO_ENABLED=0 ginkgo --no-color --focus="CANARY" $EXTRA_GINKGO_FLAGS -v --timeout 10m $GINKGO_TEST_BUILD_DIR/perpodsg.test -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id=$VPC_ID)
  if [[ -z "${SKIP_WINDOWS_TEST}" ]]; then
    (CGO_ENABLED=0 ginkgo --no-color --focus="CANARY" $EXTRA_GINKGO_FLAGS -v --timeout 35m $GINKGO_TEST_BUILD_DIR/windows.test -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id=$VPC_ID)
  else
    echo "skipping Windows tests"
  fi
  (CGO_ENABLED=0 ginkgo --no-color --focus="CANARY" $EXTRA_GINKGO_FLAGS -v --timeout 5m $GINKGO_TEST_BUILD_DIR/webhook.test -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id=$VPC_ID)
  (CGO_ENABLED=0 ginkgo --no-color --focus="CANARY" $EXTRA_GINKGO_FLAGS -v --timeout 10m $GINKGO_TEST_BUILD_DIR/cninode.test -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id=$VPC_ID)
}

echo "Starting the ginkgo test suite"
load_cluster_details

# Addons is supported from 1.18 onwards
load_addon_details
install_add_on "$LATEST_ADDON_VERSION"

attach_controller_policy_cluster_role
set_env_aws_node "ENABLE_POD_ENI" "true"
run_canary_tests
set_env_aws_node "ENABLE_POD_ENI" "false"
detach_controller_policy_cluster_role

echo "Successfully ran all tests in $(($SECONDS / 60)) minutes and $(($SECONDS % 60)) seconds"
