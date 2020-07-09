#!/usr/bin/env bash

function test-eni-pod-to-eni-pod() {
  echo "***** Starting testing trunk eni pod to trunk eni pod *****"
  local pod_name=eni-pod-$RANDOM
  sed -i '' -e "s/POD_NAME/$pod_name/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$NODE_ONE/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f $DIR/test/integration-test/config/pod.yaml
  sleep 30
  # collecting info for packets verifier
  local annotation=$(kubectl get pods $pod_name -o yaml | grep eniId)
  local pod_IP=$(echo "$annotation" | cut -d "," -f 3 | cut -d '"' -f 4)
  local vlan_ID=$(echo "$annotation" | cut -d "," -f 4 | cut -d ":" -f 2)

  echo "Will test pod $pod_name with ip $pod_IP, will ping 100 packets, and verify vlanId $vlan_ID on target pod/verifier node $NODE_ONE and pinger node $NODE_TWO"
  # update yaml file accordingly
  sed -i '' -e "s/IP_ADDRESS/$pod_IP/g" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/vlanid-to-monitor=V_LAN_ID/vlanid-to-monitor=$vlan_ID/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/SOURCE_NODE/$NODE_TWO/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/VERIFIER_NODE/$NODE_ONE/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 5
  kubectl apply -f $DIR/test/integration-test/config/ping-and-packetverifier.yaml
  sleep 60

  local exitcode=$(kubectl get pod packetverifier --output=yaml | grep exitCode | cut -d ":" -f 2 | awk '{$1=$1};1')
  if [[ $exitcode != 0 ]]; then
    echo "$(tput setaf 1)Packet verifier failed! Exit code is $exitcode.$(tput sgr 0)"
    kubectl delete -f $DIR/test/integration-test/config/pod.yaml -f $DIR/test/integration-test/config/ping-and-packetverifier.yaml
#    git reset HEAD --hard
    return 1
  else
    echo "$(tput setaf 2)Packet verifier passed!$(tput sgr 0)"
  fi
  # change yaml file back
  sed -i '' -e "s/$pod_IP/IP_ADDRESS/g" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/vlanid-to-monitor=$vlan_ID/vlanid-to-monitor=V_LAN_ID/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$NODE_TWO/SOURCE_NODE/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$NODE_ONE/VERIFIER_NODE/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  kubectl delete -f $DIR/test/integration-test/config/ping-and-packetverifier.yaml -f $DIR/test/integration-test/config/pod.yaml
  sleep 5
  sed -i '' -e "s/$pod_name/POD_NAME/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  sed -i '' -e "s/$NODE_ONE/TARGET_NODE/g" $DIR/test/integration-test/config/pod.yaml || exit 1
}

function test-regular-pod-to-eni-pod() {
  echo "***** Starting testing regular pod to trunk eni pod *****"
  local pod_name=eni-pod-$RANDOM
  sed -i '' -e "s/POD_NAME/$pod_name/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$1/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f $DIR/test/integration-test/config/pod.yaml
  sleep 30
  # collecting info for packets verifier
  local annotation=$(kubectl get pods $pod_name -o yaml | grep eniId)
  local pod_IP=$(echo "$annotation" | cut -d "," -f 3 | cut -d '"' -f 4)
  local vlan_ID=$(echo "$annotation" | cut -d "," -f 4 | cut -d ":" -f 2)

  echo "Will test pod $pod_name with ip $pod_IP, will ping 100 packets, and verify vlanId $vlan_ID on target pod/verifier node $1 and pinger node $2"
  # update yaml file accordingly
  sed -i '' -e "s/IP_ADDRESS/$pod_IP/g" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/vlanid-to-monitor=V_LAN_ID/vlanid-to-monitor=$vlan_ID/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/SOURCE_NODE/$2/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/VERIFIER_NODE/$1/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/qa/debug/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3

  # deploy source pod and packet verifier
  kubectl apply -f $DIR/test/integration-test/config/ping-and-packetverifier.yaml
  sleep 60

  local exitcode=$(kubectl get pod packetverifier --output=yaml | grep exitCode | cut -d ":" -f 2 | awk '{$1=$1};1')
  if [[ $exitcode != 0 ]]; then
    echo "Packet verifier failed! Exit code is $exitcode"
    kubectl delete -f $DIR/test/integration-test/config/pod.yaml -f $DIR/test/integration-test/config/ping-and-packetverifier.yaml
    exit 1
  else
    echo "Packet verifier passed!"
  fi

  # change yaml file back
  sed -i '' -e "s/$pod_IP/IP_ADDRESS/g" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/vlanid-to-monitor=$vlan_ID/vlanid-to-monitor=V_LAN_ID/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$2/SOURCE_NODE/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$1/VERIFIER_NODE/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/debug/qa/" $DIR/test/integration-test/config/ping-and-packetverifier.yaml || exit 1
  sleep 3
  kubectl delete -f $DIR/test/integration-test/config/ping-and-packetverifier.yaml -f $DIR/test/integration-test/config/pod.yaml
  sleep 5
  sed -i '' -e "s/$pod_name/POD_NAME/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$1/TARGET_NODE/g" $DIR/test/integration-test/config/pod.yaml || exit 1
}

function test-eni-pod-to-regular-pod() {
  echo "***** Starting testing trunk ENI pod to regular pod *****"
  local pod_name=eni-pod-$RANDOM
  sed -i '' -e "s/qa/debug/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/POD_NAME/$pod_name/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$1/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f $DIR/test/integration-test/config/pod.yaml
  sleep 30

  # setting up ping pod as traffic source
  local target_pod_IP=$(kubectl get pods eni-1 -o yaml | grep podIP: | cut -d ":" -f 2 | awk '{$1=$1};1')
  sed -i '' -e "s/IP_ADDRESS/$target_pod_IP/" $DIR/test/integration-test/config/ping-pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/SOURCE_NODE/$2/" $DIR/test/integration-test/config/ping-pod.yaml || exit 1
  sleep 5
  kubectl apply -f $DIR/test/integration-test/config/ping-pod.yaml
  sleep 15

  local annotation=$(kubectl get pods pinger -o yaml | grep eniId)
  local ping_pod_IP=$(echo "$annotation" | cut -d "," -f 3 | cut -d '"' -f 4)
  local vlan_ID=$(echo "$annotation" | cut -d "," -f 4 | cut -d ":" -f 2)

  echo "Will target pod $pod_name with ip $target_pod_IP, source ping pod with ip $ping_pod_IP and verify vlanId $vlan_ID"
  echo "Target pod in on Node $1, source ping pod and verifier pod are on Node $2"
  # setting up verifier pod
  sed -i '' -e "s/IP_ADDRESS/$ping_pod_IP/" $DIR/test/integration-test/config/packet-verifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/vlanid-to-monitor=V_LAN_ID/vlanid-to-monitor=$vlan_ID/" $DIR/test/integration-test/config/packet-verifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/VERIFIER_NODE/$2/" $DIR/test/integration-test/config/packet-verifier.yaml || exit 1
  sleep 5

  # deploy source pod and packet verifier
  kubectl apply -f $DIR/test/integration-test/config/packet-verifier.yaml
  sleep 60

  exitcode=$(kubectl get pod packetverifier --output=yaml | grep exitCode | cut -d ":" -f 2 | awk '{$1=$1};1')
  if [[ $exitcode != 0 ]]; then
    echo "Packet verifier failed! Exit code is $exitcode"
    kubectl delete -f $DIR/test/integration-test/config/ping-pod.yaml -f $DIR/test/integration-test/config/packet-verifier.yaml
    exit 1
  else
    echo "Packet verifier passed!"
  fi

  # delete testing pods
  echo "Deleting testing pods..."
  kubectl delete -f $DIR/test/integration-test/config/ping-pod.yaml -f $DIR/test/integration-test/config/pod.yaml -f $DIR/test/integration-test/config/packet-verifier.yaml
  sleep 30
  # change yaml file back
  sed -i '' -e "s/debug/qa/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$pod_name/POD_NAME/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$1/TARGET_NODE/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$target_pod_IP/IP_ADDRESS/" $DIR/test/integration-test/config/ping-pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$2/SOURCE_NODE/" $DIR/test/integration-test/config/ping-pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$ping_pod_IP/IP_ADDRESS/" $DIR/test/integration-test/config/packet-verifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/vlanid-to-monitor=$vlan_ID/vlanid-to-monitor=V_LAN_ID/" $DIR/test/integration-test/config/packet-verifier.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$2/VERIFIER_NODE/" $DIR/test/integration-test/config/packet-verifier.yaml || exit 1
  sleep 5
}

function test-eni-pod-to-k8s-service() {
  echo "***** Starting testing trunk ENI pod to regular k8s service through ClusterIP *****"
  local pod_name=eni-pod-$RANDOM
  sed -i '' -e "s/POD_NAME/$pod_name/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$1/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f $DIR/test/integration-test/config/pod.yaml
  sleep 30

  # start a service
  local service_name=test-service
  kubectl apply -f https://k8s.io/examples/service/access/hello-application.yaml
  kubectl expose deployment hello-world --type=ClusterIP --name=$service_name
  sleep 30

  local clusterIP=$(kubectl get svc $service_name -o yaml | grep clusterIP | cut -d ":" -f 2 | awk '{$1=$1};1')
  local port=$(kubectl get svc $service_name  -o yaml | grep port: | cut -d ":" -f 2 | awk '{$1=$1};1')
  echo "***** testing service $service_name at $clusterIP:$port from ENI pod $pod_name *****"
  if kubectl exec -it $pod_name -- wget --spider http://"$clusterIP":"$port"
    then
      echo "Trunk ENI pod can access to service successfully."
    else
      echo "Trunk ENI pod can not access to service."
  fi
  kubectl delete -f $DIR/test/integration-test/config/pod.yaml
  sleep 5
  sed -i '' -e "s/$pod_name/POD_NAME/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$1/TARGET_NODE/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  kubectl delete svc test-service
  kubectl delete -f https://k8s.io/examples/service/access/hello-application.yaml
}

function test-regular-pod-to-eni-pod-service() {
  echo "***** Starting testing regular pod to trunk ENI pod service, service type $2 *****"
  local pod_name=eni-pod-$RANDOM
  sed -i '' -e "s/qa/debug/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/POD_NAME/$pod_name/g" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$1/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f "$DIR"/test/integration-test/config/pod.yaml
  sleep 30

  # start a service
  local service_name=test-service
  kubectl apply -f "$DIR"/test/integration-test/config/eni-pod-service.yaml
  kubectl expose deployment hello-world --type="$2" --name=$service_name
  sleep 30

  local clusterIP=$(kubectl get svc $service_name -o yaml | grep clusterIP | cut -d ":" -f 2 | awk '{$1=$1};1')
  local port=$(kubectl get svc $service_name  -o yaml | grep port: | cut -d ":" -f 2 | awk '{$1=$1};1')
  echo "***** testing service $service_name at $clusterIP:$port from ENI pod $pod_name *****"
  if kubectl exec -it $pod_name -- wget --spider http://"$clusterIP":"$port"
    then
      echo "Trunk ENI pod can access to service successfully."
    else
      echo "Trunk ENI pod can not access to service."
  fi

  kubectl delete svc test-service
  kubectl delete -f $DIR/test/integration-test/config/pod.yaml -f $DIR/test/integration-test/config/eni-pod-service.yaml

  sed -i '' -e "s/debug/qa/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$pod_name/POD_NAME/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$1/TARGET_NODE/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 5
}

function test-eni-pod-to-kubelet() {
  local node_ip_one=$(kubectl get no -owide | grep "$NODE_ONE"  | tr -s ' ' | cut -d ' ' -f 6)
  local node_ip_two=$(kubectl get no -owide | grep "$NODE_TWO"  | tr -s ' ' | cut -d ' ' -f 6)
  local node_ip_three=$(kubectl get no -owide | grep "$NODE_THREE"  | tr -s ' ' | cut -d ' ' -f 6)
  echo "***** Testing connecting to kubelet at $node_ip_one, $node_ip_two, $node_ip_three *****"

  echo "Creating and testing an ENI pod..."
  local pod_name=eni-pod-$RANDOM
  sed -i '' -e "s/POD_NAME/$pod_name/g" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$NODE_ONE/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f "$DIR"/test/integration-test/config/pod.yaml
  sleep 30

  for ip in $node_ip_one $node_ip_two $node_ip_three; do
    if kubectl exec -it $pod_name -- ping "$ip" -c5
      then echo "$(tput setaf 2)Successfully pinged $ip$(tput sgr 0)"
      else echo "$(tput setaf 1)Failed pinging $ip$(tput sgr 0)"
    fi
  done

  kubectl delete -f "$DIR"/test/integration-test/config/pod.yaml
  sleep 30
  sed -i '' -e "s/$pod_name/POD_NAME/g" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$NODE_ONE/TARGET_NODE/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 5

  echo "Creating and testing a Regular pod..."
  local pod_name=regular-pod-$RANDOM
  sed -i '' -e "s/qa/debug/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/POD_NAME/$pod_name/g" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$NODE_ONE/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f "$DIR"/test/integration-test/config/pod.yaml
  sleep 30

  for ip in $node_ip_one $node_ip_two $node_ip_three; do
    if kubectl exec -it $pod_name -- ping "$ip" -c5
      then echo "$(tput setaf 2)Successfully pinged $ip$(tput sgr 0)"
      else echo "$(tput setaf 1)Failed pinging $ip$(tput sgr 0)"
    fi
  done

  kubectl delete -f "$DIR"/test/integration-test/config/pod.yaml
  sleep 30
  sed -i '' -e "s/debug/qa/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$pod_name/POD_NAME/g" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$NODE_ONE/TARGET_NODE/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
}

# TODO: need more investigation, revoking SG rules can mess up the CNI PlugIn
function test-revoke-security-group-rules() {
  echo "***** First we want to confirm the eni to eni works *****"
  test-eni-pod-to-eni-pod
  sleep 5
  echo "Testing revoking rule from the security group $SG_ONE"
  revoke-security-group-rule "$SG_ONE"
  sleep 30

  if test-eni-pod-to-eni-pod;
    then
      echo $?
      echo "$(tput setaf 1)Testing revoking security group rule failed.$(tput sgr 0)"
      set-security-groups "$SG_ONE"
      # TODO: return 1
      exit 1
  else
    echo "$(tput setaf 2)Testing revoking security group rule succeed.$(tput sgr 0)"
    set-security-groups "$SG_ONE"
  fi
}

function test-regular-pod-to-node-port() {
  # start a service
  local pod_name=regular-pod-$RANDOM
  sed -i '' -e "s/qa/debug/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/POD_NAME/$pod_name/g" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/TARGET_NODE/$NODE_ONE/" "$DIR"/test/integration-test/config/pod.yaml || exit 1
  sleep 5
  kubectl apply -f "$DIR"/test/integration-test/config/pod.yaml
  sleep 30

  echo "***** starting testing regular pod $pod_name to regular pod service on NodePort *****"
  test-service $pod_name "Regular"
  kubectl delete -f $DIR/test/integration-test/config/pod.yaml

  sed -i '' -e "s/debug/qa/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$pod_name/POD_NAME/g" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 3
  sed -i '' -e "s/$NODE_ONE/TARGET_NODE/" $DIR/test/integration-test/config/pod.yaml || exit 1
  sleep 5
}

function test-eni-pod-to-service-on-nodeport() {
  kubectl apply -f "$DIR"/test/integration-test/config/eni-pod-private.yaml
  sleep 30

  local pod_name=eni-private
  echo "***** starting testing trunk ENI pod to regular pod service on NodePort *****"
  test-service $pod_name "Trunk ENI"
  kubectl delete -f "$DIR"/test/integration-test/config/eni-pod-private.yaml
}

function test-service() {
  local service_name=test-service
  kubectl apply -f $DIR/test/integration-test/config/service-test-pod.yaml
  kubectl expose deployment hello-world --type=NodePort --name=$service_name
  local node_port=$(kubectl get svc $service_name -o yaml | grep nodePort | cut -d ":" -f 2 | awk '{$1=$1};1')
  local node_ip_one=$(kubectl get no -owide | grep "$NODE_ONE"  | tr -s ' ' | cut -d ' ' -f 7)
  if kubectl exec -it "$1" -- wget --spider http://"$node_ip_one":"$node_port";
    then
      echo "$(tput setaf 2)$2 pod can connect to service by regular pod at $node_ip_one:$node_port.$(tput sgr 0)"
    else
      echo "$(tput setaf 1)$2 pod can NOT connect to service by regular pod at $node_ip_one:$node_port.$(tput sgr 0)"
  fi

  sleep 10
  echo "***** starting testing trunk ENI pod to Trunk ENI pod service on NodePort *****"
  local service_name_eni=test-service-eni
  kubectl expose deployment hello-world-eni --type=NodePort --name=$service_name_eni
  local node_port=$(kubectl get svc $service_name_eni -o yaml | grep nodePort | cut -d ":" -f 2 | awk '{$1=$1};1')
  local node_ip_one=$(kubectl get no -owide -l role!=trunk-eni | grep "$NODE_ONE"  | tr -s ' ' | cut -d ' ' -f 7)
  if kubectl exec -it "$1" -- wget --spider http://"$node_ip_one":"$node_port";
    then
      echo "$(tput setaf 2)$2 pod can connect to service by Trunk ENI pod at $node_ip_one:$node_port.$(tput sgr 0)"
    else
      echo "$(tput setaf 1)$2 pod can NOT connect to service by Trunk ENI pod at $node_ip_one:$node_port.$(tput sgr 0)"
  fi
  sleep 10
  kubectl delete deploy hello-world hello-world-eni
  kubectl delete svc $service_name_eni $service_name
}

test-custom-networking() {
  echo "Turning off trunk ENI feature, aws-node will take seconds to be recreated..."
  kubectl set env ds aws-node -n kube-system ENABLE_POD_ENI=false
  sleep 30
  local vpc_id=$(aws eks describe-cluster --name "$CLUSTER_NAME" | grep -iwo 'vpc-[a-zA-z0-9]*' | xargs)

  local subnet_id=$(aws ec2 describe-subnets \
        --filters "Name=tag:aws:cloudformation:logical-id,Values=SubnetPrivateUSWEST2A" "Name=vpc-id,Values=$vpc_id" \
        --query 'Subnets[0].SubnetId' --output text)
  echo "using subnet $subnet_id in VPC $vpc_id"
  local ng_name=custom-networking-workers
  local node_id=$(aws ec2 describe-instances \
        --filter "Name=tag:eks:nodegroup-name,Values=$ng_name" "Name=instance-state-code,Values=16" "Name=tag:eks:cluster-name,Values=$CLUSTER_NAME" \
        --query 'Reservations[*].Instances[*].InstanceId' --output text)
  echo "using node $node_id"
  if [ -z "$node_id" ];
  then
    echo "Creating private networking NodeGroup"
    eksctl create nodegroup --config-file=$DIR/test/integration-test/config/public-nodegroup.yaml || exit 1
    sleep 60
  else
    if aws ec2 terminate-instances --instance-ids "$node_id";
      then
        echo "Terminate instance $node_id successfully. Wait 300s to let ASG create a new instance."
        sleep 300
      else
        echo "Failed terminating instance $node_id."
        exit 1
    fi
  fi
  local node_sg=$(aws ec2 describe-instances \
    --filter "Name=tag:eks:nodegroup-name,Values=$ng_name" "Name=tag:eks:cluster-name,Values=$CLUSTER_NAME" \
    --query 'Reservations[*].Instances[*].NetworkInterfaces[*].Groups[1].GroupId' --output text)
  echo "if there is previous CRD eniconfig, delete it..."
  kubectl delete eniconfig trunk-eni-test
  sleep 10
  sed -i '' -e "s/PRIVATE_SUBNET/$subnet_id/g" "$DIR"/test/integration-test/config/eniconfig.yaml
  sleep 3
  sed -i '' -e "s/SG_ONE/$SG_ONE/g" "$DIR"/test/integration-test/config/eniconfig.yaml
  sleep 3
  sed -i '' -e "s/SG_TWO/$node_sg/g" "$DIR"/test/integration-test/config/eniconfig.yaml
  sleep 3
  echo "creating a new crd for eniconfig"
  kubectl apply -f "$DIR"/test/integration-test/config/eniconfig.yaml
  sleep 5
  local node_name=$(kubectl get no -o name -l role=custom-networking | cut -d "/" -f 2)
  echo "labeling node $node_name"
  kubectl label nodes "$node_name" k8s.amazonaws.com/eniConfig=trunk-eni-test
  sleep 5
  echo "turning on trunk ENI feature, aws-node will take seconds to be recreated..."
  kubectl set env ds aws-node -n kube-system ENABLE_POD_ENI=true
  echo "waiting trunk ENI pod to be created"
  sleep 120
  kubectl apply -f "$DIR"/test/integration-test/config/eni-pod-custom-networking.yaml
  sleep 60
  if kubectl exec -it eni-custom -- ping google.com -c5;
  then
    echo "$(tput setaf 2)Custom networking with ENI pod passed.$(tput sgr 0)"
  else
    echo "$(tput setaf 1)Custom networking with ENI pod failed.$(tput sgr 0)"
  fi

  if test-eni-pod-to-k8s-service "$node_name";
  then
    echo "$(tput setaf 2)Custom networking with ENI pod service test passed.$(tput sgr 0)"
  else
    echo "$(tput setaf 1)Custom networking with ENI pod service test failed.$(tput sgr 0)"
  fi

  kubectl delete -f "$DIR"/test/integration-test/config/eni-pod-custom-networking.yaml
  sed -i '' -e "s/$subnet_id/PRIVATE_SUBNET/" "$DIR"/test/integration-test/config/eniconfig.yaml
  sleep 3
  sed -i '' -e "s/$SG_ONE/SG_ONE/" "$DIR"/test/integration-test/config/eniconfig.yaml
  sleep 3
  sed -i '' -e "s/$node_sg/SG_TWO/" "$DIR"/test/integration-test/config/eniconfig.yaml
  sleep 3

  echo "deleting testing custom setting nodegroup $ng_name..."
#  eksctl delete nodegroup --cluster="$CLUSTER_NAME" --name=$ng_name
#  sleep 60
}