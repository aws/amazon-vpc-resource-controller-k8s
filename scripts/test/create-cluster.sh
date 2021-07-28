#!/usr/bin/env bash

# The Scripts creates an eksctl cluster using a predefined template.
# This cluster should have the required nodegroups for running the
# Security Group for Pods and Windows IPAM integration tests.

set -eo pipefail

SCRIPTS_DIR=$(cd "$(dirname "$0")" || exit 1; pwd)
TEMPLATE_FILE="$SCRIPTS_DIR/template/eksctl/eks-cluster.yaml"

USAGE=$(cat << 'EOM'
Usage: create-cluster.sh -n [cluster-name] -v [k8s-version]

Creates an EKS Cluster with Nodegroups. The Cluster is
created using eksctl with a pre-defined eksctl template.

 Required:
 -n   Name of the EKS cluster
 -v   K8s Version
 Optional:
 -r   Region of the EKS Cluster. Defaults to us-west-2
 -c   eksctl Cluster config path. Defaults to build/eks-cluster.yaml
 -s   Suffix that will be added to each resource. This is useful when
      running in CI Setup to prevent parallel runs from modifying same
      resources
EOM
)

source "$SCRIPTS_DIR/lib/common.sh"

while getopts "n:v:r:c:s:" o; do
  case "${o}" in
    n) # Name of the EKS Cluster
      CLUSTER_NAME=${OPTARG}
      ;;
    v) # K8s Version of the EKS Cluster
      K8S_VERSION=${OPTARG}
      ;;
    r) # Region where EKS Cluster will be created
      AWS_REGION=${OPTARG}
      ;;
    c) # eksctl cluster config file path
      CLUSTER_CONFIG_PATH=${OPTARG}
      ;;
    s) # Suffix that will be attached to each AWS Resource
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

if [[ -z "$K8S_VERSION" ]]; then
 echoerr "${USAGE}\n\nmissing: -v is a required flag\n"
 exit 1
fi

if [[ -z "$AWS_REGION" ]]; then
  AWS_REGION="us-west-2"
  echo "no regions defined, will fallback to default region $AWS_REGION"
fi

if [[ -z "$CLUSTER_CONFIG_PATH" ]]; then
  CLUSTER_CONFIG_DIR="$SCRIPTS_DIR/build"
  CLUSTER_CONFIG_PATH="$CLUSTER_CONFIG_DIR/eks-cluster.yaml"

  echo "cluster config path not defined, will generate it at default path $CLUSTER_CONFIG_PATH"

  mkdir -p "$CLUSTER_CONFIG_DIR"
fi

source "$SCRIPTS_DIR/lib/config.sh"

check_is_installed eksctl

CLUSTER_NAME=$(add_suffix "$CLUSTER_NAME")

function create_eksctl_cluster() {
  echo "creating a $K8S_VERSION cluster named $CLUSTER_NAME using eksctl"

  # Using a static Instance Role Name so IAM Policies
  # can be attached to it later without having to retrieve
  # the auto generated eksctl name.
  sed "s/CLUSTER_NAME/$CLUSTER_NAME/g;
  s/CLUSTER_REGION/$AWS_REGION/g;
  s/K8S_VERSION/$K8S_VERSION/g;
  s/INSTANCE_ROLE_NAME/$INSTANCE_ROLE_NAME/g" \
  "$TEMPLATE_FILE" > "$CLUSTER_CONFIG_PATH"

  eksctl create cluster -f "$CLUSTER_CONFIG_PATH"
}

create_eksctl_cluster
