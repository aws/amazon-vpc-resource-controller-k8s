# supported EC2 instance types by amazon-vpc-resource-controller-k8s

The limit.go file in master branch provides the latest supported EC2 instance types by the controller for [Security Group for Pods feature](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html). In this file, you will find if your EC2 instance is supported (`IsTrunkingCompatible`) and how many pods using the feature (`BranchInterface`) can be created per instance when the next version of the controller is released. 

Note: If you want to check EC2 instance types currently supported by the controller, you should check the limit.go file in the release branch instead. Thank you!