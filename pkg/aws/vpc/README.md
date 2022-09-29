# supported EC2 instance types by amazon-vpc-resource-controller-k8s

The limit.go file in release branch provides the current supported EC2 instance types by the controller for [Security Group for Pods feature](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html). In this file, you will find if your EC2 instance is supported (`IsTrunkingCompatible`) and how many pods using the feature (`BranchInterface`) can be created per instance. 

If you want to check EC2 instance types that will be supported in the next release version of the controller, you can check the limit.go file in the master branch instead. Thank you!