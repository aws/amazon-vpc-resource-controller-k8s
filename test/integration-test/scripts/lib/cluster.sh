#!/usr/bin/env bash

function test-deployment() {
  local __started=0
  local __failed=0
  local __test_deployment_name="$1"
  local __count=$2
  while read -ra line;
    do
      echo "the pod $line is failed to start.";
      (( __failed++ ))
    done < <(kubectl get pods --field-selector=status.phase!=Running | grep $__test_deployment_name | cut -d " " -f 1)

  while read -ra line;
    do
      echo "the pod $line is started successfully and running.";
      (( __started++ ))
    done < <(kubectl get pods --field-selector=status.phase=Running | grep $__test_deployment_name | cut -d " " -f 1)
  echo "***** $__started pods started successfully; $__failed pods failed. *****"

  sleep 3
  echo "***** Checking if pod-eni was successfully injected into pods *****"
  kubectl get pods | grep $__test_deployment_name | cut -d " " -f 1 | while read -ra line;
    do
      if kubectl describe pods/$line | grep -q "pod-eni"
        then echo "$line: *** OK ***";
        else echo "$line: !!! NO !!!";
      fi
    done
}

function get-security-groups() {
  aws eks describe-cluster --name "$1" | grep -iwo 'sg-[a-zA-z0-9]*' | xargs | cut -d " " -f 1
}

function create-security-group() {
  local vpcid=$(aws eks describe-cluster --name "$1" | grep -iwo 'vpc-[a-zA-z0-9]*' | xargs)
  local output=$(aws ec2 create-security-group --group-name "$2" --description "Trunk ENI Integration Test" --vpc-id "$vpcid")
  local sg=$(echo "$output" | grep sg- | cut -d '"' -f 4)
  sleep 3
  set-security-groups "$sg"
  sleep 5
  echo "$sg"
}

function delete-security-group() {
  if aws ec2 delete-security-group --group-id "$1"
  then
    echo "$(tput setaf 2)Security Group $1 was successfully deleted$(tput sgr 0)"
  else
    echo "$(tput setaf 1)Security Group $1 failed to be deleted$(tput sgr 0)"
  fi
}

function set-security-groups() {
  # adding ingress rule into selected security group.
  aws ec2 authorize-security-group-ingress \
      --group-id "$1" \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0

  aws ec2 authorize-security-group-egress \
      --group-id "$1" \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0
}

# TODO: may be okay to remove port and cidr above and below to avoid rule mismatch error.
function revoke-security-group-rule() {
  aws ec2 revoke-security-group-ingress \
      --group-id "$1" \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0

  aws ec2 revoke-security-group-ingress \
      --group-id "$1" \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0
}
