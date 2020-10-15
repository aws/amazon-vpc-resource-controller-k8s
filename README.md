# amazon-vpc-resource-controller-k8s

Controller running on EKS Control Plane for managing Branch & Trunk Network Interface for [Kubernetes Pod](https://kubernetes.io/docs/concepts/workloads/pods/) using the [Security Group for Pod](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) feature. 

## Usage

The ENI Trunking API's are not yet publicly accessible. Attempting to run the controller on your worker node for enabling [Security Group for Pod](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) or use of aws-sdk-go from the internal directory for managing Trunk and Branch Network Interface will result in failure of the API calls. 

Please follow the [guide](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) for enabling Security Group for Pods on your EKS Cluster. 

## Limitation

The controller uses the same name as the controller that manages IPv4 Address for [Windows Pods](https://docs.aws.amazon.com/eks/latest/userguide/windows-support.html) running on EKS Worker Nodes. However, currently the older controller still does IP Address Management for Windows Pods. We plan to deprecate the older controller soon and use this controller instead.

## License

This library is licensed under the Apache 2.0 License. 

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md)