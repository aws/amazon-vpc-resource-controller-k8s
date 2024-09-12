#!/usr/bin/env bash

# Add Suffix to each AWS Resource Name if supplied.
# This is done to allow multiple concurrent runs on
# same accounts from modifying same resources
function add_suffix() {
  local __resource=$1
  if [[ $RESOURCE_SUFFIX ]]; then
    echo "$__resource-$RESOURCE_SUFFIX"
  else
    echo "$__resource"
  fi
}

# IAM Role Name for Linux Node Role where VPC Resource Controller Runs. It should
# have the Trunk Association Policy
TRUNK_ASSOC_POLICY_NAME=$(add_suffix "AssociateTrunkInterfacePolicy")
INSTANCE_ROLE_NAME=$(add_suffix "LinuxNodeRole")

# IAM Role and it's Policy Names which have the permission to manage Trunk/Branch
# Interfaces
VPC_RC_POLICY_NAME=$(add_suffix "VPCResourceControllerPolicy")
VPC_RC_ROLE_NAME=$(add_suffix "VPCResourceControllerRole")
