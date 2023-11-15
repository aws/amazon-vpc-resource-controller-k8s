# Troubleshooting Guide

## Table of Contents
- [Troubleshooting Windows](#troubleshooting-windows)
  - [Verify if your EKS Cluster is on the required Platform Version](#verify-if-your-eks-cluster-is-on-the-required-platform-version)
  - [Verify Windows IPAM is enabled in the ConfigMap](#verify-windows-ipam-is-enabled-in-the-configmap)
  - [Verify Node has the Resource Capacity](#verify-node-has-the-resource-capacity)
  - [Verify Pod has the resource limits](#verify-pod-has-the-resource-limit)
  - [Verify Pod has the IPv4 Address Annotation](#verify-pod-has-the-ipv4-address-annotation)
  - [Look for Issues on the Windows Host](#look-for-issues-on-the-windows-host)
- [Troubleshooting Security Group for Pods](#troubleshooting-security-group-for-pods)
  - [Verify ENI Trunking is Enabled](#verify-eni-trunking-is-enabled)
  - [Verify Trunk ENI is created](#verify-trunk-eni-is-created)
  - [Verify Pod has the resource limit](#verify-pod-has-the-resource-limit)
  - [Verify Pod has the pod-eni annotation](#verify-pod-has-the-pod-eni-annotation)
  - [Check Issues with VPC CNI](#check-issues-with-vpc-cni)
- [Troubleshooting Prefix Delegation for Windows](#troubleshooting-prefix-delegation-for-windows)
  - [Verify Windows prefix delegation is enabled in the ConfigMap](#verify-windows-prefix-delegation-is-enabled-in-the-configmap)
  - [Check both pod events and node events for any specific error](#check-both-pod-events-and-node-events-for-any-specific-error)
  - [Verify Node has the required Resource Capacity](#verify-node-has-the-required-resource-capacity)
  - [Verify Pod has the required resource limits](#verify-pod-has-the-required-resource-limits)
  - [Verify Pod has the required IPv4 Address Annotation](#verify-pod-has-the-required-ipv4-address-annotation)
  - [Verify the configuration options set for windows prefix delegation](#verify-the-configuration-options-set-for-windows-prefix-delegation)
  - [Look for networking issues on the Windows Host](#look-for-networking-issues-on-the-windows-host)
- [List of Common Issues](#list-of-common-issues)
  - [PSP Blocking Controller Annotations](#psp-blocking-controller-annotations)
  - [Missing IAM Permissions on the Cluster Role](#missing-iam-permissions-on-the-cluster-role)
  - [ENI/IP Exhaustion](#eniip-exhaustion)
  - [Disable prefix delegation feature for Windows](#disable-prefix-delegation-feature-for-windows)

## Troubleshooting Windows

Please follow the troubleshooting guide in the chronological order to debug issues with Windows Node and Pods.

### Verify if your EKS Cluster is on the required Platform Version
To get the Platform Version of your EKS cluster
```bash
aws eks describe-cluster --name cluster-name --region us-west-2 | jq .cluster.platformVersion
```

Your Platform Version should be equal to or greater than Platform Version [specified here](https://github.com/aws/amazon-vpc-resource-controller-k8s/releases/tag/v1.1.0).

**Resolution**

If your Platform Version is lower, you can
- Create a new EKS Cluster *or*
- Update to the new K8s Version if possible *or*
- Enable legacy controller support on your EKS Cluster using this [guide](https://docs.aws.amazon.com/eks/latest/userguide/windows-support.html).

### Verify Windows IPAM is enabled in the ConfigMap.

To get the ConfigMap and the data field

```bash
kubectl get configmaps -n kube-system amazon-vpc-cni -o custom-columns=":data"
```

You should have the ConfigMap with the following data, 
```
enable-windows-ipam:true
```

**Resolution**

If the ConfigMap is missing or doesn't have the above field, you can
- Create or Update ConfigMap with the required fields by following this [guide](https://docs.aws.amazon.com/eks/latest/userguide/windows-support.html).


### Verify Node has the Resource Capacity
Describe the Windows Node,

```
kubectl describe node node-name
```
You should see a non-zero capacity for resource `vpc.amazonaws.com/PrivateIPv4Address`
```
Capacity:
  vpc.amazonaws.com/PrivateIPv4Address:  9
Allocatable:
  vpc.amazonaws.com/PrivateIPv4Address:  9
```

**Resolution**

If the node doesn't have the resource capacity validate the following,
- Windows Node has label `kubernetes.io/os: windows` or `beta.kubernetes.io/os: windows`.
- There are [Sufficient ENI/IP](#eniip-exhaustion).
- Sufficient permissions in the [Cluster Role](#missing-iam-permissions-on-the-cluster-role).

### Verify Pod has the resource limits
Describe the Windows Pod,
```
kubectl describe pod windows-pod
```
You should see 1 limit and request for the resource `vpc.amazonaws.com/PrivateIPv4Address`
```
Limits:
  vpc.amazonaws.com/PrivateIPv4Address:  1
Requests:
  vpc.amazonaws.com/PrivateIPv4Address:  1
```
**Resolution**

If limit/request is missing,

1. Validate Pod has nodeSelector.
   ```
   nodeSelector:
     kubernetes.io/os: windows
   ```
2. Validate Mutating Webhook Configuration is not accidentally deleted.
   ```
   kubectl get mutatingwebhookconfigurations.admissionregistration.k8s.io vpc-resource-mutating-webhook
   NAME                            WEBHOOKS   AGE
   vpc-resource-mutating-webhook   1          59d
   ```

### Verify Pod has the IPv4 Address Annotation.

Describe the Windows Pod,
```
kubectl describe pod windows-pod
```
The Pod should have the similar annotation.
```
Annotations:    vpc.amazonaws.com/PrivateIPv4Address: 192.168.25.15/19
```

**Resolution**

If the Annotation is missing, 

- Check the Pod Events for errors emitted by the vpc-resource-controller
- There are no [PSP Blocking the annotation](#psp-blocking-controller-annotations).
- There are [Sufficient ENI/IP](#eniip-exhaustion).
- Sufficient permissions in the [Cluster Role](#missing-iam-permissions-on-the-cluster-role).

### Look for Issues on the Windows Host 

**Resolution**

If the Pod is still stuck in `ContainerCreating` you can,
- Fetch more detailed logs on the Host using the [EKS Log collector script](https://github.com/awslabs/amazon-eks-ami/blob/master/log-collector-script/windows/README.md) 
- Check the CNI Logs from the collected logs.
- Open an Issue if no intuitive logs are present [Issue](https://github.com/aws/amazon-vpc-resource-controller-k8s/issues/new/choose) in this repository.

## Troubleshooting Security Group for Pods

Please follow the troubleshooting guide in the chronological order to debug issues with Security Group for Pods.

### Verify ENI Trunking is Enabled
Describe the aws-node daemonset
```
kubectl get ds -n kube-system aws-node -o yaml
```
The following environment variable must be set.
```
containers:
  name: aws-node
  env:
  - name: ENABLE_POD_ENI
    value: "true" 
```

**Resolution**
If the environment variable is not set,

- Follow the guide to [enable SGP feature](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html).

### Verify Trunk ENI is created

Describe the Node,
```
kubectl describe node node-name
```

The following label will be set if Trunk ENI is created,
```
Labels:             vpc.amazonaws.com/has-trunk-attached=true
```

**Resolution**

If the label is missing or set to false check for,

- Instance type supports ENI Trunking. Only Nitro instance supports this feature. [See](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html#supported-instance-types) for supported instance types. 

On nodes created before feature was enabled,
- Check if there's [capacity](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/using-eni.html) to create one more ENI.
  ```
  aws ec2 describe-network-interfaces --filters Name=attachment.instance-id,Values=instance-id
  ```
On nodes created after feature was enabled,
- There are [Sufficient ENI/IP](#eniip-exhaustion).
- Sufficient permissions in the [Cluster Role](#missing-iam-permissions-on-the-cluster-role).

### Verify Pod has the resource limit
Describe the SGP Pod
```
kubectl describe pod sgp-pod
```
You should see 1 limit and request for the resource `vpc.amazonaws.com/pod-eni`
```
Limits:
  vpc.amazonaws.com/pod-eni:  1
Requests:
  vpc.amazonaws.com/pod-eni:  1
```

**Resolution**

If limit/request is missing,

1. Validate you have Security Group Policy that matches labels/service account with the Pod.
2. Validate the RBAC Role and RoleBindings are not accidentally deleted.
   ```
   kubectl get rolebindings.rbac.authorization.k8s.io -n kube-system  eks-vpc-resource-controller-rolebinding
   kubectl get roles.rbac.authorization.k8s.io -n kube-system eks-vpc-resource-controller-role
   
   NAME                                      ROLE                                    AGE
   eks-vpc-resource-controller-rolebinding   Role/eks-vpc-resource-controller-role   59d
   NAME                               CREATED AT
   eks-vpc-resource-controller-role   2021-11-08T07:40:41Z
   ```
3. Validate Mutating Webhook Configuration is not accidentally deleted.
   ```
   kubectl get mutatingwebhookconfigurations.admissionregistration.k8s.io vpc-resource-mutating-webhook
   NAME                            WEBHOOKS   AGE
   vpc-resource-mutating-webhook   1          59d
   ```

### Verify Pod has the pod-eni annotation
Describe the SGP Pod,
```
kubectl describe pod sgp-pod
```
The Pod should have the following annotation.
```
Annotations:    vpc.amazonaws.com/pod-eni: [Branch ENI Details]
```

**Resolution**

If the Annotation is missing,

- Check the Pod Events for errors emitted by the vpc-resource-controller
- There are no [PSP Blocking the annotation](#psp-blocking-controller-annotations).
- There are [Sufficient ENI/IP](#eniip-exhaustion).
- Sufficient permissions in the [Cluster Role](#missing-iam-permissions-on-the-cluster-role).

### Check Issues with VPC CNI

**Resolution**

If the Pod is still stuck in `ContainerCreating` you can,
- Fetch more detailed logs on the Host using the [EKS Log collector script](https://github.com/awslabs/amazon-eks-ami/tree/master/log-collector-script/linux#readme)
- Check the CNI Logs from the collected logs.
- Open an [Issue](https://github.com/aws/amazon-vpc-resource-controller-k8s/issues/new/choose) in this repository if the problem still persists.

## Troubleshooting Prefix Delegation for Windows
Please follow the troubleshooting steps here for issues with Windows Node and Pods when using `prefix delegation` mode.

The following steps should be checked in chronological order to find out any issues with the workflow.
### Verify Windows prefix delegation is enabled in the ConfigMap

To get the ConfigMap and the data field

```bash
kubectl get configmaps -n kube-system amazon-vpc-cni -o custom-columns=":data"
```

You should have the ConfigMap with the following data in the string,
```
enable-windows-ipam:true enable-windows-prefix-delegation:true
```

**Resolution**

If the ConfigMap is missing or doesn't have the above field, you can create or update the `amazon-vpc-cni` ConfigMap with the required fields-
```
enable-windows-ipam: "true"
enable-windows-prefix-delegation: "true"
```

**Note**: Windows IPAM needs to be enabled in order to use windows prefix delegation feature.

### Check both pod events and node events for any specific error
In case the controller encounters any error during it's prefix delegation workflow which needs to be acted upon by the customer, it will emit the errors as pod events and/or node events. Therefore, checking the same can be a good starting point to root cause the issue.

You can obtain the pod events using the following command.
```bash
kubectl get events --all-namespaces
```

In case there is any explicit error, the same needs to be looked into.

For example, if the error states that there are insufficient space in the subnet to carve a /28 prefix, then the subnet needs to be looked into to ensure that /28 ranges are available which can be allocated as prefixes.

### Verify Node has the required Resource Capacity
Same as [Verify Node has the Resource Capacity](#verify-node-has-the-resource-capacity)

### Verify Pod has the required resource limits
Same as [Verify Pod has the resource limits](#verify-pod-has-the-resource-limit)

### Verify Pod has the required IPv4 Address Annotation
Same as [Verify Pod has the IPv4 Address Annotation](#verify-pod-has-the-ipv4-address-annotation)

### Verify the configuration options set for windows prefix delegation
Configuration options can be used to fine-tune the behaviour of prefix delegation on Windows. The details about the options are available [here](windows/prefix_delegation_config_options.md).

To get the ConfigMap and the data field

```bash
kubectl get configmaps -n kube-system amazon-vpc-cni -o custom-columns=":data"
```

If you see any of the following keys in the data-
```
minimum-ip-target
warm-ip-target
warm-prefix-target
```
Then the configuration options have been set.

**Resolution**

Verify if the configuration is correct as mentioned in the [documentation](windows/prefix_delegation_config_options.md).

Alternatively, to isolate the issue, try removing the above keys from the config map.

### Look for networking issues on the Windows Host
Same as [Look for Issues on the Windows Host](#look-for-issues-on-the-windows-host)

## List of Common Issues  

### PSP Blocking Controller Annotations
If you have a PSP that blocks annotation to Pod, you will have to allow annotation from the following User `eks:vpc-resource-controller`
```
subjects:
  - kind: Group
    apiGroup: rbac.authorization.k8s.io
    name: system:authenticated
  - kind: User
    name: eks:vpc-resource-controller
    apiGroup: rbac.authorization.k8s.io
  - kind: ServiceAccount
    name: eks-vpc-resource-controller
```

### Missing IAM Permissions on the Cluster Role
To get cluster role for your EKS Cluster
```
aws eks describe-cluster --name cluster-name --region us-west-2  | j
q .cluster.roleArn
```
To find the policies attached to the cluster role
```
aws iam list-attached-role-policies --role-name role-name-from-above
```
The Policy `arn:aws:iam::aws:policy/AmazonEKSVPCResourceController` must be present for the Windows/SGP feature to work. If it's missing, please add the policy.

### ENI/IP Exhaustion
New ENI Creation or Assigning Secondary IPv4 Address can fail if you don't have sufficient IPv4 Address in your Subnet. 

To find the list of IPv4 address available

```
aws ec2 describe-subnets --subnet-id subnet-id-here
```

From the response you can look for how many IPv4 address are available in the Subnet from the field `AvailableIpAddressCount`

### Disable prefix delegation feature for Windows

You should check if the feature is enabled via ConfigMap. To get the ConfigMap and the data field

```bash
kubectl get configmaps -n kube-system amazon-vpc-cni -o custom-columns=":data"
```

If have the ConfigMap with the following data in the string,
```
enable-windows-prefix-delegation:true
```
then the feature is enabled.

**Resolution**

You can disable the feature by editing your config map and setting `enable-windows-prefix-delegation` as `"false"`.
