#!/bin/bash

# This script run integration tests on the EKS VPC Resource Controller
# This is not intended to run integration tests when controller is running
# on the data plane for development and testing purposes.

set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
INTEGRATION_TEST_DIR="$SCRIPT_DIR/../../test/integration"

echo "Running VPC Resource Controller integration test with the following variables
KUBE CONFIG: $KUBE_CONFIG_PATH
CLUSTER_NAME: $CLUSTER_NAME
REGION: $REGION
OS_OVERRIDE: $OS_OVERRIDE"

if [[ -z "${OS_OVERRIDE}" ]]; then
  OS_OVERRIDE=linux
fi

GET_CLUSTER_INFO_CMD="aws eks describe-cluster --name $CLUSTER_NAME --region $REGION"

if [[ -z "${ENDPOINT}" ]]; then
  CLUSTER_INFO=$($GET_CLUSTER_INFO_CMD)
else
  CLUSTER_INFO=$($GET_CLUSTER_INFO_CMD --endpoint $ENDPOINT)
fi

VPC_ID=$(echo $CLUSTER_INFO | jq -r '.cluster.resourcesVpcConfig.vpcId')
SERVICE_ROLE_ARN=$(echo $CLUSTER_INFO | jq -r '.cluster.roleArn')
ROLE_NAME=${SERVICE_ROLE_ARN##*/}
 
echo "VPC ID: $VPC_ID, Service Role ARN: $SERVICE_ROLE_ARN, Role Name: $ROLE_NAME"

# Set up local resources
echo "Attaching IAM Policy to Cluster Service Role"
aws iam attach-role-policy \
    --policy-arn arn:aws:iam::aws:policy/AmazonEKSVPCResourceController \
    --role-name "$ROLE_NAME" > /dev/null

echo "Enabling Pod ENI on aws-node"
kubectl set env daemonset aws-node -n kube-system ENABLE_POD_ENI=true

#Start the test
echo "Starting the ginkgo test suite" 

# running the tests on data plane
(cd $INTEGRATION_TEST_DIR/perpodsg && CGO_ENABLED=0 GOOS=$OS_OVERRIDE ginkgo --focus="CANARY" -v -timeout 15m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID)
(cd $INTEGRATION_TEST_DIR/windows && CGO_ENABLED=0 GOOS=$OS_OVERRIDE ginkgo --focus="CANARY" -v -timeout 20m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID)
(cd $INTEGRATION_TEST_DIR/webhook && CGO_ENABLED=0 GOOS=$OS_OVERRIDE ginkgo --focus="CANARY" -v -timeout 5m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$REGION --aws-vpc-id $VPC_ID)

#Tear down local resources
echo "Detaching the IAM Policy from Cluster Service Role"
aws iam detach-role-policy \
    --policy-arn arn:aws:iam::aws:policy/AmazonEKSVPCResourceController \
    --role-name $ROLE_NAME > /dev/null

echo "Disabling Pod ENI on aws-node"
kubectl set env daemonset aws-node -n kube-system ENABLE_POD_ENI=false

echo "Successfully finished the test suite"
