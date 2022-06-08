#!/usr/bin/env bash

# Script to run vpc-resource-controller release tests: webhook, perpodsg, windows integration tests
# This script does not install any addons nor update vpc-resource-controller. Please install all 
# required versions to be tests prior to running the script.

# Parameters: 
# CLUSTER_NAME: name of the cluster
# KUBE_CONFIG_PATH: path to the kubeconfig file, default ~/.kube/config
# REGION: default us-west-2

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
INTEGRATION_TEST_DIR="$SCRIPT_DIR/../../test/integration"
SECONDS=0

source "$SCRIPT_DIR"/lib/cluster.sh

function run_integration_tests(){
  TEST_RESULT=success
  (cd $INTEGRATION_TEST_DIR/perpodsg && CGO_ENABLED=0 ginkgo --skip=LOCAL -v -timeout=40m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID) || TEST_RESULT=fail
  (cd $INTEGRATION_TEST_DIR/windows && CGO_ENABLED=0 ginkgo --skip=LOCAL -v -timeout=80m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID) || TEST_RESULT=fail
  (cd $INTEGRATION_TEST_DIR/webhook && CGO_ENABLED=0 ginkgo --skip=LOCAL -v -timeout=10m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID) || TEST_RESULT=fail

  if [[ "$TEST_RESULT" == fail ]]; then
      echo "Integration tests failed."
      exit 1
  fi
}

echo "Running VPC Resource Controller integration test with the following variables
KUBE CONFIG: $KUBE_CONFIG_PATH
CLUSTER_NAME: $CLUSTER_NAME
REGION: $REGION"

load_cluster_details
attach_controller_policy_cluster_role
set_env_aws_node "ENABLE_POD_ENI" "true"
run_integration_tests
set_env_aws_node "ENABLE_POD_ENI" "false"
detach_controller_policy_cluster_role

echo "Successfully ran all tests in $(($SECONDS / 60)) minutes and $(($SECONDS % 60)) seconds"
