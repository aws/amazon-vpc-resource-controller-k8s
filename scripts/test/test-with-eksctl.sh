#!/usr/bin/env bash

# Builds and installs the controlller on EKS DataPlane. This is for
# testing purposes only and it will not work for accounts that are
# not allowlisted for ENI Trunking feature.

set -eo pipefail

USAGE=$(cat << 'EOM'
Usage: test-with-eksctl.sh -n [cluster-name] -r [user-role-arn]

Builds and installs the controlller on EKS DataPlane. This is for
testing purposes only and it will not work for accounts that are
not allowlisted for ENI Trunking feature.

 Required: [Only one of the flag must be set]
 -n   name of the existing EKS cluster
 Optional:
 -i   IAM role ARN that will be assumed by the controller to manage trunk/branch ENI
 -s   Suffix that will be added to each resource. This is useful when
      running in CI Setup to prevent parallel runs from modifying resources.
 -r   AWS Region where the controller image will be hosted. Defaults to us-west-2
EOM
)

SCRIPTS_DIR=$(cd "$(dirname "$0")" || exit 1; pwd)
TEMPLATE_DIR=$SCRIPTS_DIR/template/rbac
BASHPID=$$

source "$SCRIPTS_DIR/lib/common.sh"

while getopts "n:i:r:s:" o; do
  case "${o}" in
    n) # Name of the EKS Cluster
      CLUSTER_NAME=${OPTARG}
      ;;
    i) # Role ARN that will be assumed by the VPC Resource Controller
      VPC_RC_ROLE_ARN=${OPTARG}
      ;;
    r) # Region where ECR image will be hosted
      AWS_REGION=${OPTARG}
      ;;
    s) # Resource Suffix attached to AWS Resource Names
      RESOURCE_SUFFIX=${OPTARG}
      ;;
    *)
      echoerr "${USAGE}"
      exit 1
      ;;
  esac
done
shift $((OPTIND-1))

if [[ -z "$CLUSTER_NAME" ]]; then
 echoerr "${USAGE}\n\nmissing: -n is a required flag\n"
 exit 1
fi

if [[ -z "$AWS_REGION" ]]; then
  AWS_REGION="us-west-2"
  echo "no regions defined, will fallback to default region $AWS_REGION"
fi

source "$SCRIPTS_DIR/lib/aws.sh"
source "$SCRIPTS_DIR/lib/k8s.sh"
source "$SCRIPTS_DIR/lib/config.sh"

check_is_installed aws
check_is_installed docker
check_is_installed ginkgo
check_is_installed eksctl
check_is_installed kubectl
check_is_installed kustomize

CLUSTER_NAME=$(add_suffix "$CLUSTER_NAME")
AWS_ACCOUNT_ID="${AWS_ACCOUNT_ID:-$(aws sts get-caller-identity | jq .Account -r)}"
VPC_ID=$(aws eks describe-cluster --name $CLUSTER_NAME --region $AWS_REGION | jq -r '.cluster.resourcesVpcConfig.vpcId')
KUBE_CONFIG_PATH=~/.kube/config
CONTROLLER_LOG_FILE=/tmp/$CLUSTER_NAME.logs
TEST_FAILED=false

# If Role name is not provided, use the default role name
if [[ -z "$VPC_RC_ROLE_ARN" ]]; then
  VPC_RC_ROLE_ARN="arn:aws:iam::$AWS_ACCOUNT_ID:role/$VPC_RC_ROLE_NAME"
fi

ECR_URL=${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_REGION}.amazonaws.com
ECR_REPOSITORY=amazon/vpc-resource-controller
ECR_IMAGE_TAG=$(add_suffix "test")

IMAGE=$ECR_URL/$ECR_REPOSITORY:$ECR_IMAGE_TAG

function build_and_push_image() {
  echo "building and pushing controller image to ECR"
  IMAGE=$IMAGE AWS_ACCOUNT=$AWS_ACCOUNT_ID AWS_REGION=$AWS_REGION make docker-build
  IMAGE=$IMAGE AWS_ACCOUNT=$AWS_ACCOUNT_ID AWS_REGION=$AWS_REGION make docker-push
}

function install_controller() {
  echo "installing amazon-vpc-resource-controller-k8s"
  IMAGE=$IMAGE \
  AWS_ACCOUNT=$AWS_ACCOUNT_ID \
  AWS_REGION=$AWS_REGION \
  CLUSTER_NAME=$CLUSTER_NAME \
  USER_ROLE_ARN=$VPC_RC_ROLE_ARN \
  make deploy

  check_deployment_rollout eks-vpc-resource-controller kube-system 2m
}

function disable_eks_controller() {
  echo "disabling the default amazon-vpc-resource-controller-k8s controller"

  # Delete Mutating Webhook Configuration
  kubectl delete mutatingwebhookconfigurations.admissionregistration.k8s.io vpc-resource-mutating-webhook
  # Delete the Validating Webhook Configuration
  kubectl delete validatingwebhookconfigurations.admissionregistration.k8s.io vpc-resource-validating-webhook

  # Remove the patch/update permission on ConfigMap from the EKS VPC RC Leader election Role
  kubectl patch roles -n kube-system vpc-resource-controller-leader-election-role \
  --patch "$(cat "$TEMPLATE_DIR/cp-vpc-leader-election-role-patch.yaml")"
}

function set_pod_eni_flag_on_ipamd() {
  local flag=$1
  echo "Setting Pod ENI on aws-node/ipamd to $flag"
  kubectl set env daemonset aws-node -n kube-system ENABLE_POD_ENI=$flag

  kubectl rollout status daemonset aws-node -n kube-system
}

function run_inegration_test() {
  local additional_gingko_params=$1

  # SGP Tests
  (cd test/integration/perpodsg && \
  CGO_ENABLED=0 ginkgo "$additional_gingko_params" \
  -v -timeout 40m -- \
  -cluster-kubeconfig=$KUBE_CONFIG_PATH \
  -cluster-name=$CLUSTER_NAME \
  --aws-region=$AWS_REGION \
  --aws-vpc-id $VPC_ID) || TEST_FAILED=true

  # Windows Test
  (cd test/integration/windows && \
  CGO_ENABLED=0 ginkgo "$additional_gingko_params" -v -timeout 60m -- \
  -cluster-kubeconfig=$KUBE_CONFIG_PATH \
  -cluster-name=$CLUSTER_NAME \
  --aws-region=$AWS_REGION \
  --aws-vpc-id $VPC_ID) || TEST_FAILED=true

  # SGP + Windows Webhook Test
  (cd test/integration/webhook && \
  CGO_ENABLED=0 ginkgo "$additional_gingko_params" -v -timeout 10m -- \
  -cluster-kubeconfig=$KUBE_CONFIG_PATH \
  -cluster-name=$CLUSTER_NAME \
  --aws-region=$AWS_REGION \
  --aws-vpc-id $VPC_ID) || TEST_FAILED=true
}

function verify_controller_has_lease() {
  # Get the name of the VPC Resource Controller Pod
  local controller_pod_names="$(kubectl get pods -n kube-system -l app=eks-vpc-resource-controller \
  --no-headers -o custom-columns=":metadata.name")"

  # Wait till the new controller has acquired the leader lease
  i=0
  while :
  do
    local lease_holder="$(kubectl get configmap -n kube-system cp-vpc-resource-controller -o json \
    | jq '.metadata.annotations."control-plane.alpha.kubernetes.io/leader"' --raw-output \
    | jq .holderIdentity --raw-output)"

    for controller_pod_name in $controller_pod_names
    do
       if [[ $lease_holder == $controller_pod_name* ]]; then
          echo "one of the new controller has the lease: $lease_holder"
          break 2
       fi
    done
    if  [[ i -ge 20 ]]; then
      echo "new controller failed to accquire leader lease in $i attempts"
      exit 1
    else
      echo "new controller doesn't have the leader lease yet, will retry"
      sleep 10
      i=$((i+1))
    fi
  done

  # Get the lease transition count at the time the new controller acquired lease
  LEADER_TRANSITION_BEFORE_TEST=$(get_leader_lease_transistion_count)
}

function verify_leader_lease_didnt_change() {
  # Get the leader lease transition count after running all integration tests
  LEADER_TRANSITION_AFTER_TEST=$(get_leader_lease_transistion_count)

  # If the leader lease count increased between the time when the test was running, then
  # we will assume the tests failed as it could mean either the controller failed to hold
  # the lease or it restarted during test execution which should not happen ideally
  if [[ $LEADER_TRANSITION_BEFORE_TEST != "$LEADER_TRANSITION_AFTER_TEST" ]]; then
    echo "leader transitioned during the tests, the new controller failed to keep the leader lease"
    exit 1
  fi
}

# Get the number of times the leader lease transition between different owners
function get_leader_lease_transistion_count() {
  kubectl get configmap -n kube-system cp-vpc-resource-controller -o json \
  | jq '.metadata.annotations."control-plane.alpha.kubernetes.io/leader"' --raw-output \
  | jq .leaderTransitions
}

function output_logs() {
  cat $CONTROLLER_LOG_FILE
}

function clean_up() {
  echo "cleaning up..."
  delete_ecr_image "$ECR_REPOSITORY" "$ECR_IMAGE_TAG"
  output_logs
}

function redirect_vpc_controller_logs() {
  # Till the time the bash process keeps running, keep on redirecting the logs to the file
  # The parent process will log the output of the file on exit
  while ps -p $BASHPID > /dev/null
  do
    kubectl logs -n kube-system -l app=eks-vpc-resource-controller \
    --tail -1 -f >> $CONTROLLER_LOG_FILE || echo "LOG COLLECTOR:existing controller killed, will retry"
    # Allow for the new controller to come up
    sleep 10
  done

  echo "LOG COLLECTOR: exiting the process"
}

# Delete the IAM Policies, Roles and the EKS Cluster
trap 'clean_up' EXIT

# Cordon the Windows Nodes as cert manager and other future 3rd pary
# dependency may not have nodeselectors to schedule pods on linux
kubectl cordon -l kubernetes.io/os=windows

# Install the stable version of VPC CNI
sh "$SCRIPTS_DIR/install-vpc-cni.sh" "v1.7.10"

# Install Cert Manager which is used for generating the
# certificates for the Webhooks
sh "$SCRIPTS_DIR/install-cert-manager.sh"

# Login to ECR to push the controller image
ecr_login "$AWS_REGION" "$ECR_URL"

# Create the repository but don't delete it as it can hold
# test images from other GitHub runs
create_repository "$ECR_REPOSITORY"

# Push the image to ECR
build_and_push_image

# Install the CRD and the Controller on the Data Plane
install_controller

# Uncordon the Windows Node, all new deployment/pods fromt this
# point must have nodeSelecotrs to be scheduled on the right OS
kubectl uncordon -l kubernetes.io/os=windows

# Start redirecting the logs to the log file which will be outputted at
# when the script exits. This log should be run in background and exit
# when the bash script exits
redirect_vpc_controller_logs &

# Disable the Controller on EKS Control Plane
disable_eks_controller

# Verify the Controller on Data Plane has the leader lease
verify_controller_has_lease

# Enables the SGP feature on IPAMD, which lables the node
# with a fetaure flag used by controller to start managing
# the node for ENI Trunking/Branching
set_pod_eni_flag_on_ipamd "true"

# Allow for IPAMD to label the node after startup
# TODO: Handle this in the Test Suite in more concrete manner
sleep 60

# Run Ginko Test for Security Group for Pods and skip all the local tests as
# they require restarts and it will lead to leader lease being switched and the
# next validation step failing
run_inegration_test "--skip=LOCAL"

# Verify the leader lease didn't transition during the execution of test cases
verify_leader_lease_didnt_change

# Run Local Ginko Test that require multiple restarts of controller for negative
# scenarios testing
run_inegration_test "--focus=LOCAL"

# Revert back to initial state after the test
set_pod_eni_flag_on_ipamd "false"

# If any of the test failed, exit with non zero exit code
if [ $TEST_FAILED = true ]; then
  exit 1
fi
