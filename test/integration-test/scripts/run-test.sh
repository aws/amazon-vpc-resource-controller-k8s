#!/usr/bin/env bash

DIR=$(cd "$(dirname "$1")"; pwd)
echo $DIR

export KUBECONFIG=~/.kube/config
if [[ ! -f "$KUBECONFIG" ]];
  then
    echo "$KUBECONFIG DOES NOT exist."
    exit 1
  else
    echo "***** Found k8s config *****"
fi

source $DIR/test/integration-test/scripts/lib/common.sh
source $DIR/test/integration-test/scripts/lib/aws.sh
source $DIR/test/integration-test/scripts/lib/cluster.sh
source $DIR/test/integration-test/scripts/lib/test-helper.sh
OS=$(go env GOOS)
ARCH=$(go env GOARCH)
AWS_REGION=${AWS_REGION:-us-west-2}
K8S_VERSION=${K8S_VERSION:-1.14.6}

# check CNI config
CNI20_CONFIG_PATH="$DIR/test/integration-test/config/aws-k8s-cni.yaml"
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

# login to the ECR registry
aws ecr get-login-password --region $AWS_REGION | docker login \
  --username AWS \
  --password-stdin "$AWS_ECR_REGISTRY" || exit 1
ensure_ecr_repo "$AWS_ACCOUNT_ID" "$AWS_ECR_REPO_NAME"

CLUSTER_NAME=eni-integration
WORKERS_NAME=workers

if aws eks list-clusters | grep -q $CLUSTER_NAME
then
  echo "***** Cluster $CLUSTER_NAME already exists *****"
else
  echo "***** The cluster $CLUSTER_NAME is not existing. Creating the cluster... *****"
  eksctl create cluster \
          --name $CLUSTER_NAME \
          --version 1.16 \
          --region us-west-2 \
          --nodegroup-name "$WORKERS_NAME" \
          --node-type c5.large \
          --nodes 3 \
          --nodes-min 1 \
          --nodes-max 4 \
          --managed || exit 1
fi

SG_NAME=ENITestSecurityGroup
SG_ONE=$(create-security-group $CLUSTER_NAME "$SG_NAME")
echo "***** will use security group $SG_ONE if it is not empty  *****"

ROLE_NAME=$(aws eks describe-nodegroup \
            --cluster-name $CLUSTER_NAME \
            --nodegroup workers | grep nodeRole | cut -d / -f 2 | cut -d '"' -f 1)

ENI_ROLE_POLICY_NAME=Test_Pod_ENI

if aws iam get-policy --policy-arn arn:aws:iam::"$AWS_ACCOUNT_ID":policy/$ENI_ROLE_POLICY_NAME >/dev/null
  then echo "***** required Role Policy for Trunk ENI already exists *****"
  else aws iam create-policy \
        --policy-name $ENI_ROLE_POLICY_NAME \
        --policy-document file://$DIR/test/integration-test/config/trunk-ENI-policy.json
fi
sleep 30

# this way can add a managed policy into the role.
echo "***** adding Test_Pod_ENI policy into the node instance role $ROLE_NAME *****"
ENI_POLICY=arn:aws:iam::$AWS_ACCOUNT_ID:policy/$ENI_ROLE_POLICY_NAME
aws iam attach-role-policy \
      --role-name "$ROLE_NAME" \
      --policy-arn "$ENI_POLICY" | exit 1

# wait for 10s to start update CNI addons to CNI2.0 plugin
sleep 10
echo "***** replacing addon cni plugin *****"
kubectl apply -f "$CNI20_CONFIG_PATH"

# Delay based on 3 nodes, 30s grace period for aws-node pods
echo "***** Sleeping for 30s and confirm ENI enabled in CNI *****"
sleep 30

kubectl describe ds/aws-node -n kube-system | grep ENABLE_POD_ENI || exit 1

# install cert manager
if kubectl apply --validate=false -f https://github.com/jetstack/cert-manager/releases/download/v0.15.1/cert-manager.yaml
then
  echo "***** Cert manager was installed from Github cert-manager repo. *****"
  sleep 30
else
  echo "***** failed installing cert manager from github link, test may fail on VPC controller. *****"
fi

CONTROLLER_IMG=${CONTROLLER_IMG:-"$IMAGE_NAME:$IMAGE_TAG"}
IMG=$CONTROLLER_IMG
echo "***** Start VPC Resource Controller based on $IMG *****"
make docker-build || exit 1
echo "***** finished build and sleep 10s.***** "
sleep 5
docker tag controller:latest $IMG || exit 1
echo "***** starting pushing image *****"
docker push $IMG || exit 1

sed -i '' -e 's/failurePolicy=fail/failurePolicy=ignore/' $DIR/webhook/core/pod_webhook.go || exit 1
sleep 5
make deploy IMG=$IMG || exit 1
sleep 5
sed -i '' -e 's/failurePolicy=ignore/failurePolicy=fail/' $DIR/webhook/core/pod_webhook.go || exit 1

 creating testing SecurityGroupPolicy CRD objects
echo "***** using $SG_ONE to setting up SGP *****"
sed -i '' -e "s/sg-0000000/$SG_ONE/" $DIR/test/integration-test/config/security-group-policy-pod.yaml || exit 1
kubectl apply -f $DIR/test/integration-test/config/security-group-policy-pod.yaml
sed -i '' -e "s/$SG_ONE/sg-0000000/" $DIR/test/integration-test/config/security-group-policy-pod.yaml || exit 1

echo "***** Wait for 60s to let testing environment up *****"
REAL_SG=$(kubectl get sgp | grep sg- | cut -d " " -f 4)
if [[ $REAL_SG == *"sg-0000000"* ]]
  then echo "applying security group to SGP failed, exit..."
  exit 1
fi
sleep 60

echo "*******************************************************************************"
TEST_POD_NAME=eni-pod
echo "***** Testing creating a pod $TEST_POD_NAME to be enabled with pod eni: *****"
echo ""
kubectl apply -f $DIR/test/integration-test/config/pod.yaml || exit 1
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
echo "***** Deleting $TEST_POD_NAME... *****"
kubectl delete -f $DIR/test/integration-test/config/pod.yaml
sleep 30

echo "*******************************************************************************"
TEST_DEPLOY_NAME=hello-world
echo "***** Testing creating a deployment $TEST_DEPLOY_NAME to be ENABLED with pod eni: *****"
echo ""
kubectl apply -f $DIR/test/integration-test/config/deployment.yaml || exit 1
echo "waiting for 60s to let testing pod up"
sleep 60

test-deployment $TEST_DEPLOY_NAME 30

echo "***** Deleting $TEST_DEPLOY_NAME... *****"
kubectl delete -f $DIR/test/integration-test/config/deployment.yaml
sleep 30

echo "*******************************************************************************"
TEST_DEPLOY_NAME=hello-world
echo "***** Testing creating a deployment $TEST_DEPLOY_NAME to be ENABLED with pod eni: *****"
echo ""
kubectl apply -f $DIR/test/integration-test/config/deployment-50.yaml || exit 1
echo "waiting for 60s to let testing pod up"
sleep 60

test-deployment $TEST_DEPLOY_NAME 50

echo "***** Deleting $__test_deployment_name... *****"
kubectl delete -f $DIR/test/integration-test/config/deployment.yaml
sleep 30

echo "*******************************************************************************"
TEST_DEPLOY_NAME=no-eni
echo "***** Testing creating a deployment $TEST_DEPLOY_NAME to be DISABLED with pod eni: *****"
echo ""
kubectl apply -f $DIR/test/integration-test/config/deployment-no-eni.yaml || exit 1
echo "waiting for 60s to let testing pod up"
sleep 60

test-deployment $TEST_DEPLOY_NAME 30

echo "***** Deleting $TEST_DEPLOY_NAME... *****"
kubectl delete -f $DIR/test/integration-test/config/deployment-no-eni.yaml
sleep 30

echo "***** Starting packet verifier test *****"
# get all worker nodes
NODES=$(kubectl get no -o name | cut -d "/" -f 2 | tr '\n' ',')
NODE_ONE=$(echo $NODES | cut -d "," -f 1)
NODE_TWO=$(echo $NODES | cut -d "," -f 2)
NODE_THREE=$(echo $NODES | cut -d "," -f 3)
test-eni-pod-to-eni-pod
sleep 10
test-regular-pod-to-eni-pod "$NODE_ONE" "$NODE_TWO"
sleep 10
test-eni-pod-to-regular-pod "$NODE_ONE" "$NODE_TWO"
sleep 10
test-eni-pod-to-k8s-service "$NODE_ONE"
sleep 10
test-regular-pod-to-eni-pod-service "$NODE_ONE" "ClusterIP"
sleep 10
test-regular-pod-to-eni-pod-service "$NODE_ONE" "NodePort"
sleep 10
test-eni-pod-to-kubelet
sleep 10
test-revoke-security-group-rules
sleep 10
test-regular-pod-to-node-port
sleep 10

echo "***** launching private networking nodegroup to test trunk ENI with nodePort *****"
PRIVATE_NG_NAME=trunk-eni-workers
if ! eksctl get nodegroup --cluster=eni-integration | grep $PRIVATE_NG_NAME;
  then
    echo "Creating private networking NodeGroup"
    eksctl create nodegroup --config-file=$DIR/test/integration-test/config/private-nodegroup.yaml
  else
    echo "Private NodeGroup $PRIVATE_NG_NAME is already available."
fi
test-eni-pod-to-service-on-nodeport
sleep 10

echo "***** launching a node group to test custom networking *****"
test-custom-networking

echo "cleaning up testing resources"
sleep 60
delete-security-group "$SG_ONE"
sleep 5
SGP=$(kubectl get sgp -o name | cut -d "/" -f 2)
kubectl delete sgp "$SGP"
sleep 5
kubectl delete --all deployments --namespace=default
sleep 30
kubectl delete --all pods --namespace=default
echo "$(tput setaf 2)Testing finished successfully.$(tput sgr 0)"