#!/usr/bin/env bash

# Installs a stable version of vpc-cni

set -eo pipefail

SCRIPTS_DIR=$(cd "$(dirname "$0")" || exit 1; pwd)
DEFAULT_VPC_CNI_VERSION="v1.7.10"

source "$SCRIPTS_DIR/lib/k8s.sh"
source "$SCRIPTS_DIR/lib/common.sh"

check_is_installed kubectl

__vpc_cni_version="$1"
if [ -z "$__vpc_cni_version" ]; then
    __vpc_cni_version=${VPC_CNI_VERSION:-$DEFAULT_VPC_CNI_VERSION}
fi

install() {

  echo "installing vpc cni $__vpc_cni_version"
  __minor_version=$(echo $__vpc_cni_version | cut -d\. -f1-2)
  __vpc_cni_url="https://raw.githubusercontent.com/aws/amazon-vpc-cni-k8s/$__vpc_cni_version/config/$__minor_version/aws-k8s-cni.yaml"

  kubectl apply -f $__vpc_cni_url
  echo "ok.."
}

check() {
  echo "checking vpc cni version has rolled out"
  check_ds_rollout "aws-node" "kube-system"
  echo "ok.."
}

install
check