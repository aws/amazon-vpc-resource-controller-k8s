#!/usr/bin/env bash

# Cretes or Deletes the IAM Roles and IAM Policies required by
# VPC Resource Controller for running the Integration test

set -eo pipefail

USAGE=$(cat << 'EOM'
Usage: create-iam-resources.sh -n [cluster-name] -o [create/delete]

Creates the IAM Policies required by the VPC Resource
Controller to Run.

 Required:
 -n   name of cluster for creating IAM Roles and Policies
 -o   [Only one of the value must be set]
      create: To create the IAM Roles required by vpc-resource-controller
      delete: To delete the IAM Roles required by vpc-resource-controller
 Optional:
 -s   Suffix that will be added to each resource. This is useful when
      running in CI Setup to prevent parallel runs from modifying resources.
EOM
)

SCRIPTS_DIR=$(cd "$(dirname "$0")" || exit 1; pwd)
TEMPLATE_DIR="$SCRIPTS_DIR/template/iam"
source "$SCRIPTS_DIR/lib/common.sh"

while getopts "o:n:s:" o; do
  case "${o}" in
    o) # creates the IAM Roles and Policies required by the Controller
      OPERATION=${OPTARG}
      ;;
    n) # Cluster name for which IRSA will be created
      CLUSTER_NAME=${OPTARG}
      ;;
    s) # suffix attached to each IAM Resource
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
  echoerr "$USAGE\n\n cluster name must be provided"
  exit  1
fi

if [[ -z "$OPERATION" ]]; then
  echoerr "$USAGE\n\n must provide either create or delete operation"
  exit  1
fi

source "$SCRIPTS_DIR/lib/config.sh"
source "$SCRIPTS_DIR/lib/aws.sh"

check_is_installed aws

WORKSPACE="$SCRIPTS_DIR/build"
mkdir -p "$WORKSPACE"

AWS_ACCOUNT_ID="${AWS_ACCOUNT_ID:-$(aws sts get-caller-identity | jq .Account -r)}"

TRUNK_ASSOC_POLICY_ARN="arn:aws:iam::$AWS_ACCOUNT_ID:policy/$TRUNK_ASSOC_POLICY_NAME"
INSTANCE_ROLE_ARN="arn:aws:iam::$AWS_ACCOUNT_ID:role/$INSTANCE_ROLE_NAME"

VPC_RC_POLICY_ARN="arn:aws:iam::$AWS_ACCOUNT_ID:policy/$VPC_RC_POLICY_NAME"
VPC_RC_ROLE_ARN="arn:aws:iam::$AWS_ACCOUNT_ID:role/$VPC_RC_ROLE_NAME"

create_iam_resources() {
  echo "creating IAM resources"

  # Create Policy with permission to associate Trunk to Branch Interface.
  # This Policy is attached to the Instance Node Role where VPC RC Runs
  create_policy "$TRUNK_ASSOC_POLICY_NAME" "$TEMPLATE_DIR/associate-trunk-policy.json"
  attach_policy "$TRUNK_ASSOC_POLICY_ARN" "$INSTANCE_ROLE_NAME"

  # Policy document that allows VPC RC Instance Role to assume the Role
  # to manage Trunk/Branch Interfaces on user's behalf
  local policy_document="$WORKSPACE/aws-trust-policy.json"
  sed "s|AWS_ROLE_ARN|$INSTANCE_ROLE_ARN|g" \
  "$TEMPLATE_DIR/aws-trust-relationship.json" > "$policy_document"
  create_role "$VPC_RC_ROLE_NAME" "$policy_document"

  # Finally, attach the policy with the permission to manage Trunk/Branch
  # Interface to the above role
  create_policy "$VPC_RC_POLICY_NAME" "$TEMPLATE_DIR/vpc-resource-controller-policy.json"
  attach_policy "$VPC_RC_POLICY_ARN" "$VPC_RC_ROLE_NAME"
}

delete_iam_resources() {
  echo "deleting IAM resources"

  detach_policy "$VPC_RC_POLICY_ARN" "$VPC_RC_ROLE_NAME"
  delete_policy "$VPC_RC_POLICY_ARN"
  delete_role "$VPC_RC_ROLE_NAME"

  detach_policy "$TRUNK_ASSOC_POLICY_ARN" "$INSTANCE_ROLE_NAME"
  delete_policy "$TRUNK_ASSOC_POLICY_ARN"
  # The Instance Node Role is not deleted here as it's lifecycle
  # is managed by eksctl
}

if [[ "$OPERATION" == "create" ]]; then
  create_iam_resources
elif [ "$OPERATION" == "delete" ]; then
  delete_iam_resources
else
  echoerr "$USAGE\n\n operation can be create or delete only"
  exit 1
fi
