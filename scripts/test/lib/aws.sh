#!/usr/bin/env bash

AWS_CLI_VERSION=$(aws --version 2>&1 | cut -d/ -f2 | cut -d. -f1)

ecr_login() {
    echo -n "ecr login ... "
    local __aws_region=$1
    local __ecr_url=$2
    local __err_msg="Failed ECR login. Please make sure you have IAM permissions to access ECR."
    if [ $AWS_CLI_VERSION -gt 1 ]; then
        ( aws ecr get-login-password --region $__aws_region | \
                docker login --username AWS --password-stdin $__ecr_url ) ||
                ( echo "\n$__err_msg" && exit 1 )
    else
        $( aws ecr get-login --no-include-email ) || ( echo "\n$__err_msg" && exit 1 )
    fi
    echo "ok."
}

create_repository() {
  local __repository_name=$1

  if ! (aws ecr describe-repositories --repository-name "$__repository_name"); then
    echo "creating new repository, $__repository_name"
    aws ecr create-repository --repository-name "$__repository_name"
  else
    echo "repository already exists $__repository_name"
  fi
}

delete_ecr_image() {
  local __ecr_repository=$1
  local __image_tag=$2

  aws ecr batch-delete-image \
     --repository-name "$__ecr_repository" \
     --image-ids imageTag="$__image_tag"
}

create_policy() {
  local __policy_name=$1
  local __policy_document=$2

  echo "creating IAM policy $__policy_arn"
  aws iam create-policy --policy-name "$__policy_name" --policy-document "file:///$__policy_document"
}

delete_policy() {
  local __policy_arn=$1

  echo "deleting IAM policy $__policy_arn"
  aws iam delete-policy --policy-arn "$__policy_arn"
}

create_role() {
  local __role_name=$1
  local __trust_policy_doc=$2

  if [[ -z $__trust_policy_doc ]]; then
    echo "creating IAM role $__role_name"
    aws iam create-role --role-name "$__role_name"
  else
    echo "creating IAM role $__role_name with trust policy $__trust_policy_doc"
    aws iam create-role --role-name "$__role_name" --assume-role-policy-document "file:///$__trust_policy_doc"
  fi
}

delete_role() {
  local __role_name=$1

  echo "deleting IAM role $__role_name"
  aws iam delete-role --role-name "$__role_name"
}

attach_policy() {
  local __policy_arn=$1
  local __role_name=$2

  echo "attaching policy $__policy_arn to role $__role_name"
  aws iam attach-role-policy --policy-arn "$__policy_arn" --role-name "$__role_name"
}

detach_policy() {
  local __policy_arn=$1
  local __role_name=$2

  echo "detaching policy $__policy_arn to role $__role_name"
  aws iam detach-role-policy --policy-arn "$__policy_arn" --role-name "$__role_name"
}
