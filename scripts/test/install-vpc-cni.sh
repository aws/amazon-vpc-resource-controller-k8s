#!/usr/bin/env bash

# Installs a stable version of vpc-cni

set -eo pipefail

SCRIPTS_DIR=$(cd "$(dirname "$0")" || exit 1; pwd)
DEFAULT_VPC_CNI_VERSION="1.11"

source "$SCRIPTS_DIR/lib/k8s.sh"
source "$SCRIPTS_DIR/lib/common.sh"

check_is_installed kubectl

__vpc_cni_version="$1"
if [ -z "$__vpc_cni_version" ]; then
    __vpc_cni_version=${VPC_CNI_VERSION:-$DEFAULT_VPC_CNI_VERSION}
fi

install() {

  echo "installing vpc cni $__vpc_cni_version"
  # install the latest patch version for the release
  __vpc_cni_url="https://raw.githubusercontent.com/aws/amazon-vpc-cni-k8s/release-$__vpc_cni_version/config/master/aws-k8s-cni.yaml"
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