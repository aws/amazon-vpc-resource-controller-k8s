# amazon-vpc-resource-controller-k8s

![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/aws/amazon-vpc-resource-controller-k8s)
[![Go Report Card](https://goreportcard.com/badge/github.com/aws/amazon-vpc-resource-controller-k8s)](https://goreportcard.com/report/github.com/aws/amazon-vpc-resource-controller-k8s)
![GitHub](https://img.shields.io/github/license/aws/amazon-vpc-resource-controller-k8s?style=flat)

Controller running on EKS Control Plane for managing Branch & Trunk Network Interface for [Kubernetes Pod](https://kubernetes.io/docs/concepts/workloads/pods/) using the [Security Group for Pod](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) feature.

This is a new open source project and we are actively working on enhancing the project by adding Continuous Integration, detailed documentation and new features to the controller itself. We would appreciate your feedback and suggestions to improve the project and your experience with EKS and Kubernetes. 

## Usage

The ENI Trunking APIs are not yet publicly accessible. Attempting to run the controller on your worker node for enabling [Security Group for Pod](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) for managing Trunk and Branch Network Interface will result in failure of the API calls.

Please follow the [guide](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) for enabling Security Group for Pods on your EKS Cluster. 

## Windows VPC Resource Controller

The controller uses the same name as the controller that manages IPv4 Address for [Windows Pods](https://docs.aws.amazon.com/eks/latest/userguide/windows-support.html) running on EKS Worker Nodes. However, currently the older controller still does IP Address Management for Windows Pods. We plan to deprecate the older controller soon and use this controller instead.

## License

This library is licensed under the Apache 2.0 License. 

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md)
