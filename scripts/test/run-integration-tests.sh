#!/usr/bin/env bash

# Script to run vpc-resource-controller release tests: webhook, perpodsg, windows integration tests
# This script does not install any addons nor update vpc-resource-controller. Please install all
# required versions to be tests prior to running the script.

# Parameters:
# CLUSTER_NAME: name of the cluster
# KUBE_CONFIG_PATH: path to the kubeconfig file, default ~/.kube/config
# REGION: default us-west-2
# RUN_DEVEKS_TEST: false

set -euoE pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
INTEGRATION_TEST_DIR="$SCRIPT_DIR/../../test/integration"
SECONDS=0

: "${RUN_DEVEKS_TEST:=false}"
: "${ENDPOINT:=""}"
: "${SKIP_WINDOWS_TEST:=""}"
: "${EXTRA_GINKGO_FLAGS:=""}"

source "$SCRIPT_DIR"/lib/cluster.sh

cleanup(){

  if [[ $? == 0 ]]; then
    echo "Successfully ran all tests in $(($SECONDS / 60)) minutes and $(($SECONDS % 60)) seconds"
  else
    echo "[Error] Integration tests failed"
  fi

  echo "Cleaning up the setup"
  set_env_aws_node "ENABLE_POD_ENI" "false"
  detach_controller_policy_cluster_role
}

trap cleanup EXIT

function run_integration_tests(){
  TEST_RESULT=success
  (cd $INTEGRATION_TEST_DIR/perpodsg && CGO_ENABLED=0 ginkgo --skip=LOCAL $EXTRA_GINKGO_FLAGS -v -timeout=35m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID) || TEST_RESULT=fail
  if [[ -z "${SKIP_WINDOWS_TEST}" ]]; then
    (cd $INTEGRATION_TEST_DIR/windows && CGO_ENABLED=0 ginkgo --skip=LOCAL $EXTRA_GINKGO_FLAGS -v -timeout=150m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID) || TEST_RESULT=fail
  else
    echo "skipping Windows tests"
  fi
  (cd $INTEGRATION_TEST_DIR/webhook && CGO_ENABLED=0 ginkgo --skip=LOCAL $EXTRA_GINKGO_FLAGS -v -timeout=5m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID) || TEST_RESULT=fail
  # (cd $INTEGRATION_TEST_DIR/cninode && CGO_ENABLED=0 ginkgo --skip=LOCAL $EXTRA_GINKGO_FLAGS -v -timeout=10m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID) || TEST_RESULT=fail

  if [[ "$TEST_RESULT" == fail ]]; then
      exit 1
  fi
}

echo "Running VPC Resource Controller integration test with the following variables
KUBE CONFIG: $KUBE_CONFIG_PATH
CLUSTER_NAME: $CLUSTER_NAME
REGION: $REGION"

if [[ "${RUN_DEVEKS_TEST}" == "true" ]];then
  load_deveks_cluster_details
else
  load_cluster_details
fi

attach_controller_policy_cluster_role
set_env_aws_node "ENABLE_POD_ENI" "true"
run_integration_tests
