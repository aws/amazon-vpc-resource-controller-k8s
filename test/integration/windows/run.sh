#!/bin/sh

set -e

echo "Running the windows vpc-resource-controller integraiton test
KUBE_CONFIG_PATH: $KUBE_CONFIG_PATH
CLUSTER_NAME: $CLUSTER_NAME
AWS_REGION: $AWS_REGION
"

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

curl -s -o vpc-resource-controller.yaml https://s3.us-west-2.amazonaws.com/amazon-eks/manifests/$AWS_REGION/vpc-resource-controller/latest/vpc-resource-controller.yaml  > /dev/null
curl -s -o webhook-create-signed-cert.sh https://s3.us-west-2.amazonaws.com/amazon-eks/manifests/$AWS_REGION/vpc-admission-webhook/latest/webhook-create-signed-cert.sh  > /dev/null
curl -s -o webhook-patch-ca-bundle.sh https://s3.us-west-2.amazonaws.com/amazon-eks/manifests/$AWS_REGION/vpc-admission-webhook/latest/webhook-patch-ca-bundle.sh  > /dev/null
curl -s -o vpc-admission-webhook-deployment.yaml https://s3.us-west-2.amazonaws.com/amazon-eks/manifests/$AWS_REGION/vpc-admission-webhook/latest/vpc-admission-webhook-deployment.yaml  > /dev/null

# Install the windows vpc-resource-controller and vpc-admission-webhook
kubectl apply -f vpc-resource-controller.yaml
chmod +x webhook-create-signed-cert.sh webhook-patch-ca-bundle.sh
./webhook-create-signed-cert.sh
cat ./vpc-admission-webhook-deployment.yaml | ./webhook-patch-ca-bundle.sh > vpc-admission-webhook.yaml
kubectl apply -f vpc-admission-webhook.yaml

#Start the test
echo "Starting the ginkgo test suite"
(cd $SCRIPT_DIR && CGO_ENABLED=0 GOOS=darwin ginkgo -v -timeout 15m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id "not-required")

# Unisntall the windows vpc-resource-controller and vpc-admission-webhook
kubectl delete -f vpc-resource-controller.yaml
kubectl delete -f vpc-admission-webhook.yaml

rm vpc-resource-controller.yaml
rm webhook-create-signed-cert.sh
rm webhook-patch-ca-bundle.sh
rm vpc-admission-webhook-deployment.yaml