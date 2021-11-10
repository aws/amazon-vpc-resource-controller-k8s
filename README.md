# amazon-vpc-resource-controller-k8s

![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/aws/amazon-vpc-resource-controller-k8s)
[![Go Report Card](https://goreportcard.com/badge/github.com/aws/amazon-vpc-resource-controller-k8s)](https://goreportcard.com/report/github.com/aws/amazon-vpc-resource-controller-k8s)
![GitHub](https://img.shields.io/github/license/aws/amazon-vpc-resource-controller-k8s?style=flat)

## Usage

Controller running on EKS Control Plane for managing Branch & Trunk Network Interface for [Kubernetes Pod](https://kubernetes.io/docs/concepts/workloads/pods/) using the [Security Group for Pod](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) feature and IPv4 Address Management(IPAM) of [Windows Nodes](https://docs.aws.amazon.com/eks/latest/userguide/windows-support.html).

## Security Group for Pods

The controller only manages the Trunk/Branch Network Interface for EKS Cluster using the Security Group for Pods feature. The Networking on the host is setup by [amazon-vpc-cni-k8s](https://github.com/aws/amazon-vpc-cni-k8s) plugin.

ENI Trunking is a private feature even though the APIs are publicly accessible using AWS SDK. Hence, attempting to run the controller on your worker node for enabling [Security Group for Pod](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) for managing Trunk and Branch Network Interface will result in failure of the API calls.

Please follow the [guide](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html) for enabling Security Group for Pods on your EKS Cluster. 

## Windows IPv4 Address Management

The controller manages the IPv4 Addresses for all the Windows Node in EKS Cluster and allocates IPv4 Address to Windows Pods. The Networking on the host is setup by [amazon-vpc-cni-plugins](https://github.com/aws/amazon-vpc-cni-plugins).

Please follow this [guide](https://docs.aws.amazon.com/eks/latest/userguide/windows-support.html) for enabling Windows Support on your EKS cluster.

## License

This library is licensed under the Apache 2.0 License. 

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md)

We would appreciate your feedback and suggestions to improve the project and your experience with EKS and Kubernetes.
