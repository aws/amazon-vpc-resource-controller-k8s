# Developer Guide

## Setup

```sh
make toolchain # Install required to develop the project
```

## Testing a code change

Deploy your changes to a local development cluster and run the tests against it. You will need to allowlist your account for ENI trunking before the deployment.

If you are testing on EKS beta cluster, set
```sh
BETA_CLUSTER=true
```

```sh
make apply-dependencies # install the cert manager and certificate
make apply # Apply your changes
make test-e2e # Run the integration test suite
```

In another terminal, you can tail the logs with stern
```sh
stern -l app=vpc-resource-controller -n kube-system
```

## Submitting a PR
Run the presubmit target and check in all generated code before submitting a PR.

```sh
make presubmit
```

## Troubleshooting

### Invalid value 'trunk' for InterfaceType

The following error means that must be allowlisted for EC2 Networking
```
{"level":"error","timestamp":"2023-06-09T21:53:00.705Z","logger":"branch eni provider","msg":"failed to create trunk interface","node name":"ip-192-168-60-153.us-west-2.compute.internal","request":"initialize","instance ID":"i-0d892c7fa08bf7bbd","error":"InvalidParameterValue: Invalid value 'trunk' for InterfaceType. Allowed values are ('EFA')\n\tstatus code: 400, request id: 7b94401f-686f-46a4-a5e9-3cfda8e12cd6","stacktrace":"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk.(*trunkENI).InitTrunk\n\tgithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk/trunk.go:194\ngithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch.(*branchENIProvider).InitResource\n\tgithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/provider.go:154\ngithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/node.(*node).InitResources\n\tgithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/node.go:156\ngithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager.(*manager).performAsyncOperation\n\tgithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager/manager.go:316\ngithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker.(*worker).processNextItem\n\tgithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker/worker.go:162\ngithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker.(*worker).runWorker\n\tgithub.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker/worker.go:147"}
```