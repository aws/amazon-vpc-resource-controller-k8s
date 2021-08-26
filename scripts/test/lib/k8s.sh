#!/usr/bin/env bash

# resource_exists returns 0 when the supplied resource can be found, 1
# otherwise. An optional second parameter overrides the Kubernetes namespace
# argument
k8s_resource_exists() {
    local __res_name=${1:-}
    local __namespace=${2:-}
    local __args=""
    if [ -n "$__namespace" ]; then
        __args="$__args-n $__namespace"
    fi
    kubectl get $__args "$__res_name" >/dev/null 2>&1
}


# check_deployment_rollout watches the status of the latest rollout
# until it's done or until the timeout. Namespace and timeout are optional
# parameters
check_deployment_rollout() {
    local __dep_name=${1:-}
    local __namespace=${2:-}
    local __timeout=${3:-"2m"}
    local __args=""
    if [ -n "$__namespace" ]; then
        __args="$__args-n $__namespace"
    fi
    kubectl rollout status deployment/"$__dep_name" $__args --timeout=$__timeout
}

# check_ds_rollout watches the status of the latest rollout until it's done or
# until the timeout. Namespace and timeout are optional parameters
check_ds_rollout() {
    local __ds_name=${1:-}
    local __namespace=${2:-}
    local __timeout=${3:-"2m"}
    local __args=""
    if [ -n "$__namespace" ]; then
        __args="$__args-n $__namespace"
    fi
    kubectl rollout status ds/"$__ds_name" $__args --timeout=$__timeout
}