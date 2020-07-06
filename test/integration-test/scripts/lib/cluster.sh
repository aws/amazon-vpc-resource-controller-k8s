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
  aws eks describe-cluster --name "$1" | grep -iwo 'sg-[a-zA-z0-9]*' | xargs
}

function set-security-groups() {
#  SGS=$(aws eks describe-cluster --name "$1" | grep -iwo 'sg-[a-zA-z0-9]*' | xargs)
#    IFS=' '
#    read -ra ARRAY <<<"$SGS"
#    SG_ONE=$ARRAY
  SGS=$(get-security-groups "$1")
  local sg1=$(echo $SGS | cut -d " " -f 1)
  local sg2=$(echo $SGS | cut -d " " -f 2)

  # adding ingress rule into selected security group.
  aws ec2 authorize-security-group-ingress \
      --group-id $sg1 \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0

  aws ec2 authorize-security-group-egress \
      --group-id $sg1 \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0

  aws ec2 authorize-security-group-ingress \
      --group-id $sg2 \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0

  aws ec2 authorize-security-group-egress \
      --group-id $sg2 \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0

  echo $SGS
}

function revoke-security-group-rule() {
  SGS=$(get-security-groups "$1")
  local sg1=$(echo $SGS | cut -d " " -f 1)
  local sg2=$(echo $SGS | cut -d " " -f 2)
  aws ec2 revoke-security-group-ingress \
      --group-id $sg1 \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0

  aws ec2 revoke-security-group-ingress \
      --group-id $sg2 \
      --protocol all \
      --port 0-60000 \
      --cidr 0.0.0.0/0
}
