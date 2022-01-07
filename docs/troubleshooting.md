## Troubleshooting Windows

Please follow the troubleshooting guide in the chronological order to debug issues with Windows Node and Pods.

### Verify if your EKS Cluster is on the required Platform Version
To get the Platform Version of your EKS cluster
```bash
aws eks describe-cluster --name cluster-name --region us-west-2 | jq .cluster.platformVersion
```

Your Platform Version should be equal to or greater than Platfrom Version [specified here](https://github.com/aws/amazon-vpc-resource-controller-k8s/releases/tag/v1.1.0).

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

## List of Common Issues  

### PSP Blocking Controller Annotations
If you have a PSP that blocks annotation to Pod, you will have to allow annotation from the following Service Account
```
subjects:
  - kind: Group
    apiGroup: rbac.authorization.k8s.io
    name: system:authenticated
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
