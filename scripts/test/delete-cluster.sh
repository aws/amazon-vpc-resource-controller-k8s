#!/usr/bin/env bash

# Deletes an EKS Cluster and it's nodegroup created using
# eksctl. The script requires the cluster config path to delete
# all resources associated with the cluster.

set -eo pipefail

USAGE=$(cat << 'EOM'
Usage: delete-cluster.sh -c [cluster-config]

Deletes an EKS Cluster along with the Nodegroups. Takes in
the cluster config as an optional argument. If no arugment is
provided then it will use the cluster config in the default
path i.e /build/eks-cluster.yaml

 Optional:
 -c   eksctl Cluster config path. Defaults to build/eks-cluster.yaml
EOM
)

SCRIPTS_DIR=$(cd "$(dirname "$0")" || exit 1; pwd)
CLUSTER_CONFIG_DIR="$SCRIPTS_DIR/build"
CLUSTER_CONFIG_PATH="$CLUSTER_CONFIG_DIR/eks-cluster.yaml"

source "$SCRIPTS_DIR/lib/common.sh"

check_is_installed eksctl

while getopts "c:" o; do
  case "${o}" in
    c) # eksctl cluster config file path
      CLUSTER_CONFIG_PATH=${OPTARG}
      ;;
    *)
      echoerr "${USAGE}"
      exit 1
      ;;
  esac
done
shift $((OPTIND-1))

eksctl delete cluster -f "$CLUSTER_CONFIG_PATH" --wait
