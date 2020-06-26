#!/usr/bin/env bash

DIR=$(cd "$(dirname "$1")"; pwd)
echo $DIR

source $DIR/test/integration/scripts/lib/common.sh
source $DIR/test/integration/scripts/lib/aws.sh
source $DIR/test/integration/scripts/lib/cluster.sh

OS=$(go env GOOS)
ARCH=$(go env GOARCH)
AWS_REGION=${AWS_REGION:-us-west-2}
#K8S_VERSION=${K8S_VERSION:-1.14.6}
#PROVISION=${PROVISION:-true}
#DEPROVISION=${DEPROVISION:-true}
#BUILD=${BUILD:-true}
#RUN_CONFORMANCE=${RUN_CONFORMANCE:-false}
#
#__cluster_created=0
#__cluster_deprovisioned=0
#
#on_error() {
#    # Make sure we destroy any cluster that was created if we hit run into an
#    # error when attempting to run tests against the cluster
#    if [[ $__cluster_created -eq 1 && $__cluster_deprovisioned -eq 0 && "$DEPROVISION" == true ]]; then
#        # prevent double-deprovisioning with ctrl-c during deprovisioning...
#        __cluster_deprovisioned=1
#        echo "Cluster was provisioned already. Deprovisioning it..."
#        down-test-cluster
#    fi
#    exit 1
#}

# test cluster config location
# Pass in CLUSTER_ID to reuse a test cluster
#CLUSTER_ID=${CLUSTER_ID:-$RANDOM}
#CLUSTER_NAME=cni-test-$CLUSTER_ID
#TEST_CLUSTER_DIR=/tmp/cni-test/cluster-$CLUSTER_NAME
#CLUSTER_MANAGE_LOG_PATH=$TEST_CLUSTER_DIR/cluster-manage.log
#CLUSTER_CONFIG=${CLUSTER_CONFIG:-${TEST_CLUSTER_DIR}/${CLUSTER_NAME}.yaml}
#KUBECONFIG_PATH=${KUBECONFIG_PATH:-${TEST_CLUSTER_DIR}/kubeconfig}
#
## shared binaries
#TESTER_DIR=${TESTER_DIR:-/tmp/aws-k8s-tester}
#TESTER_PATH=${TESTER_PATH:-$TESTER_DIR/aws-k8s-tester}
#KUBECTL_PATH=${KUBECTL_PATH:-$TESTER_DIR/kubectl}

# check CNI config
CNI20_CONFIG_PATH="$DIR/test/integration/config/aws-k8s-cni.yaml"
if [[ ! -f "$CNI20_CONFIG_PATH" ]]; then
    echo "$CNI20_CONFIG_PATH DOES NOT exist. Set \$CNI_TEMPLATE_VERSION to an existing directory in ./config/"
    exit
fi

# double-check all our preconditions and requirements have been met
check_is_installed docker
check_is_installed aws
check_is_installed kubectl
check_is_installed eksctl
check_aws_credentials
#ensure_aws_k8s_tester

# prepare AWS ECR for VPC Resource Controller image
AWS_ACCOUNT_ID=${AWS_ACCOUNT_ID:-$(aws sts get-caller-identity --query Account --output text)}
echo "use AWS accunt ID: $AWS_ACCOUNT_ID"
AWS_ECR_REGISTRY=${AWS_ECR_REGISTRY:-"$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"}
echo "use AWS ECR: $AWS_ECR_REGISTRY"
AWS_ECR_REPO_NAME=${AWS_ECR_REPO_NAME:-"next-cni"}
echo "use ECR repo: $AWS_ECR_REPO_NAME"
IMAGE_NAME=${IMAGE_NAME:-"$AWS_ECR_REGISTRY/$AWS_ECR_REPO_NAME"}
echo "use ECR image name: $IMAGE_NAME"
IMAGE_TAG="latest"
echo "use image tag: $IMAGE_TAG"

# `aws ec2 get-login` returns a docker login string, which we eval here to
# login to the ECR registry
aws ecr get-login-password --region us-west-2 | docker login \
  --username AWS \
  --password-stdin $AWS_ECR_REGISTRY
ensure_ecr_repo "$AWS_ACCOUNT_ID" "$AWS_ECR_REPO_NAME"
#
#mkdir -p $TEST_DIR
#mkdir -p $REPORT_DIR
#mkdir -p $TEST_CLUSTER_DIR
#mkdir -p $TEST_CONFIG_DIR

#echo "***** Starting provisioning testing cluster and run k8s-tester in 5s *****"
#sleep 5
#if [[ "$PROVISION" == true ]]; then
#    up-test-cluster
#    __cluster_created=1
#fi
CLUSTER_NAME=eni-integration


if aws eks list-clusters | grep -q $CLUSTER_NAME
then
  echo "Cluster $CLUSTER_NAME already exists"
else
  eksctl create cluster $CLUSTER_NAME \
        --name  \
        --version 1.16 \
        --region us-west-2 \
        --nodegroup-name workers \
        --node-type c5.large \
        --nodes 3 \
        --nodes-min 1 \
        --nodes-max 4 \
        --managed || exit 1;
fi

SGS=$(aws eks describe-cluster --name $CLUSTER_NAME | grep -iwo 'sg-[a-zA-z0-9]*' | xargs)
IFS=' '
read -ra ARRAY <<<"$SGS"
SG_ONE=$ARRAY
echo "using the security group: $SG_ONE"


# wait for 10s to start update CNI addons to CNI2.0 IPAMD
sleep 10

kubectl apply -f "$CNI20_CONFIG_PATH"

# Delay based on 3 nodes, 30s grace period per CNI pod
echo "Sleeping for 30s and confirm ENI enabled in CNI"
sleep 30

kubectl describe ds/aws-node -n kube-system | grep ENABLE_POD_ENI

# install cert manager
kubectl apply --validate=false -f https://github.com/jetstack/cert-manager/releases/download/v0.15.1/cert-manager.yaml
sleep 30

CONTROLLER_IMG=${CONTROLLER_IMG:-"$IMAGE_NAME:$IMAGE_TAG"}
IMG=$CONTROLLER_IMG
echo "start VPC Resource Controller based on $IMG"
# 744053100597.dkr.ecr.us-west-2.amazonaws.com/next-cni:latest
make docker-build || exit 1
echo "*****finished build and sleep 10s.*****"
sleep 5
docker tag controller:latest $IMG || exit 1
echo "*****starting pushing image*****"
docker push $IMG || exit 1

sed -i '' -e 's/failurePolicy=fail/failurePolicy=ignore/' $DIR/webhook/core/pod_webhook.go || exit 1
sleep 5
make deploy IMG=$IMG || exit 1
sleep 5
sed -i '' -e 's/failurePolicy=ignore/failurePolicy=fail/' $DIR/webhook/core/pod_webhook.go || exit 1

# creating testing SecurityGroupPolicy CRD objects
sed -i '' -e "s/sg-0000000/$SG_ONE/" $DIR/test/integration/config/security-group-policy-pod.yaml || exit 1
kubectl apply -f $DIR/test/integration/config/security-group-policy-pod.yaml
sed -i '' -e "s/$SG_ONE/sg-0000000/" $DIR/test/integration/config/security-group-policy-pod.yaml || exit 1

echo "***** Wait for 60s to let testing environment up *****"
# TODO: check if VPC controller and new CRD object are ready.
sleep 60

echo "*******************************************************************************"
TEST_POD_NAME=label-demo
echo "Testing creating a pod $TEST_POD_NAME to be enabled with pod eni:"
echo ""
kubectl apply -f $DIR/test/integration/config/pod.yaml || exit 1
echo "waiting for 60s to let testing pod up"
sleep 60
if kubectl get pods --field-selector=status.phase=Running | grep $TEST_POD_NAME
  then echo "the pod $TEST_POD_NAME is successfully up and running.";
  else echo "the pod $TEST_POD_NAME is failed to start."; sleep 30;
fi
sleep 3
if kubectl describe pods/$TEST_POD_NAME | grep "pod-eni"
  then echo "the pod $TEST_POD_NAME has been injected ENI.";
  else echo "the pod $TEST_POD_NAME does NOT have expected pod-eni injected!";
fi
echo "Deleting $TEST_POD_NAME..."
kubectl delete -f $DIR/test/integration/config/pod.yaml
sleep 30

echo "*******************************************************************************"
TEST_DEPLOY_NAME=hello-world
echo "Testing creating a deployment $TEST_DEPLOY_NAME to be ENABLED with pod eni:"
echo ""
kubectl apply -f $DIR/test/integration/config/deployment.yaml || exit 1
echo "waiting for 60s to let testing pod up"
sleep 60
if kubectl get pods --field-selector=status.phase!=Running | grep $TEST_DEPLOY_NAME
  then echo "the pods $TEST_DEPLOY_NAME is failed to start."; sleep 30;
  else echo "the pods $TEST_DEPLOY_NAME is successfully up and running.";
fi
sleep 3
echo "***** Checking if pod-eni was successfully injected into pods *****"
kubectl get pods | grep $TEST_DEPLOY_NAME | cut -d " " -f 1 | while read -ra line;
  do
    if kubectl describe pods/$line | grep -q "pod-eni"
      then echo "$line: *** OK ***";
      else echo "$line: !!! NO !!!";
    fi
  done
echo "Deleting $TEST_DEPLOY_NAME..."
kubectl delete -f $DIR/test/integration/config/deployment.yaml
sleep 30

echo "*******************************************************************************"
TEST_DEPLOY_NAME=hello-world
echo "Testing creating a deployment $TEST_DEPLOY_NAME to be ENABLED with pod eni:"
echo ""
kubectl apply -f $DIR/test/integration/config/deployment.yaml || exit 1
echo "waiting for 60s to let testing pod up"
sleep 60
if kubectl get pods --field-selector=status.phase!=Running | grep $TEST_DEPLOY_NAME
  then echo "the pods $TEST_DEPLOY_NAME is failed to start."; sleep 30;
  else echo "the pods $TEST_DEPLOY_NAME is successfully up and running.";
fi
sleep 3
echo "***** Checking if pod-eni was successfully injected into pods *****"
kubectl get pods | grep $TEST_DEPLOY_NAME | cut -d " " -f 1 | while read -ra line;
  do
    if kubectl describe pods/$line | grep -q "pod-eni"
      then echo "$line: *** OK ***";
      else echo "$line: !!! NO !!!";
    fi
  done
echo "Deleting $TEST_DEPLOY_NAME..."
kubectl delete -f $DIR/test/integration/config/deployment.yaml
sleep 30

echo "*******************************************************************************"
TEST_DEPLOY_NAME=no-eni
echo "Testing creating a deployment $TEST_DEPLOY_NAME to be DISABLED with pod eni:"
echo ""
kubectl apply -f $DIR/test/integration/config/deployment-no-eni.yaml || exit 1
echo "waiting for 60s to let testing pod up"
sleep 60
if kubectl get pods --field-selector=status.phase!=Running | grep $TEST_DEPLOY_NAME
  then echo "the pods $TEST_DEPLOY_NAME is failed to start."; sleep 30;
  else echo "the pods $TEST_DEPLOY_NAME is successfully up and running.";
fi
sleep 3
echo "***** Checking if pod-eni was successfully injected into pods *****"
kubectl get pods | grep $TEST_DEPLOY_NAME | cut -d " " -f 1 | while read -ra line;
  do
    if kubectl describe pods/$line | grep -q "pod-eni"
      then echo "$line: *** OK ***";
      else echo "$line: !!! NO !!!";
    fi
  done

echo "Deleting $TEST_DEPLOY_NAME..."
kubectl delete -f $DIR/test/integration/config/deployment-no-eni.yaml
sleep 30

echo "*******************************************************************************"
echo "Running integration tests on current image:"
echo ""
#TEST_PKG=$DIR/test/integration/test
#if [[ ! -d $TEST_PKG ]]; then
#    echo "$TEST_PKG DOES NOT exist."
#    exit
#fi
#pushd $TEST_PKG
#GO111MODULE=on go test -v -timeout 0 ./... --kubeconfig=$KUBECONFIG --ginkgo.focus="\[cni-integration\]" --ginkgo.skip="\[Disruptive\]" \
#    --assets=./assets
#TEST_PASS=$?
#popd