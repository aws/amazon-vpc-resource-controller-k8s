## Ginkgo Test Suites

All Ginkgo Integration test suites are located in `test/integration` directory.

### Prerequisite
The integration test requires the following setup

**For Security Group for Pods Test**
- Have all Nitro Based Instances in your EKS Cluster.
- Have at least 3 X c5.xlarge instance type or larger in terms of number of ENI/IP allocatable.
- Add the AmazonEKSVPCResourceController managed policy to the cluster role that is associated with your Amazon EKS cluster. Follow Step 2 in  [Deploy security groups for pods](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html)
- Enable the CNI plugin to manage network interfaces for pods by setting the ENABLE_POD_ENI variable to true in the aws-node DaemonSet. Follow Step 3 in  [Deploy security groups for pods](https://docs.aws.amazon.com/eks/latest/userguide/security-groups-for-pods.html)

**For Windows Test**
- Have 1 Linux Operating System worker Node for running coreDNS.
- Have at-least 3 Windows Operating System worker Node (Preferably c5.4xlarge or larger).
- Have all the Windows Node belonging to same nodegroup with same Security Group.

### Available Ginkgo Focus

The Integration test suite provides the following focuses.

- **[LOCAL]** 
   
  These tests can be run when the controller is running on Data Plane. This is idle for CI Setup where the controller runs on Data Plane instead of Control Plane. See `scripts/test/README.md` for details.
  ```
  # Use when running test on the Controller on EKS Control Plane. 
  --skip=LOCAL 
  ```
- **[STRESS]**

  These tests are run to stress the controller by running higher than average workload. To skip these tests, use the following.
  ```
  # Use when running only the Integration test.
  --skip=STRESS
  ```
  
- **[CANARY]**

  Continuous tests to ensure the correctness of the live production environment by testing the bare minimum functionality. Since the Canary runs quite frequently it doesn't contain all set of integration testes which could run for hours.
    ```
    # To run just the canary tests.
    --focus=CANARY
    ```

### How to Run the Integration Tests

#### Running Individual Ginkgo Test Suites

1. Set the environment variables.
   ```
   CLUSTER_NAME=<test-cluster-name>
   KUBE_CONFIG_PATH=<path-to-kube-config>
   AWS_REGION=<test-cluster-region>
   VPC_ID=<cluster-vpc-id>
   OS=<darwin/linux/etc>
   ```
2. Invoke all available test suites.
   ```
   cd test/integration
   echo "Running Validation Webhook Tests"
   (cd webhook && CGO_ENABLED=0 GOOS=$OS ginkgo -v --timeout 10m -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id=$VPC_ID)
   echo "Running Security Group for Pods Integration Tests"
   (cd perpodsg && CGO_ENABLED=0 GOOS=$OS ginkgo -v --timeout 40m -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id=$VPC_ID)
   echo "Running Windows Integration Tests"
   (cd windows && CGO_ENABLED=0 GOOS=$OS ginkgo -v --timeout 40m -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id=$VPC_ID)
   ```

#### Running Integration tests on Controller running on EKS Control Plane

1. Invoke the test script
   ```
   cd test/integration
   CLUSTER_NAME=<test-cluster-name> /
   KUBE_CONFIG_PATH=<path-to-kube-config> /
   AWS_REGION=<test-cluster-region> /
   OS_OVERRIDE=<darwin/linux/etc> /
   run.sh
   ```

#### Running Integration tests on Controller running on Data Plane

This is intended for the purposes of local development, testing and CI Setup. For more details refer the steps are provided in `scripts/test/README.md`

### Running Scale Tests

#### Test Pod startup latency
For each release, verify that pod startup latency is comparable to the previous release. This helps to detect regression issues which impact controller performance in the new release.

To run the test manually: 

##### 1. Create EKS cluster and install Karpenter.

Karpenter provides node lifecycle management for Kubernetes clusters. It automates provisioning and deprovisioning of nodes based on the scheduling needs of pods, allowing efficient scaling and cost optimization. 

The script will provision all required resources for the test: 
1. Deploy CFN stack to set up EKS cluster infrasstructure
2. Create EKS cluster using eksctl 
3. Install Karpenter on the cluster via helm
4. Deploy default NodePool and EC2NodeClass. NodePool sets constraints on the nodes that can be created by Karpenter and the pods that can run on those nodes. EC2NodeClass is used to configure AWS-specific settings such as AMI type, AMI ID, EC2 security groups. 
Refer to the Karpenter documentation for further details.
```
./scripts/test/create-cluster-karpenter.sh
```
The scripts are located in the `scripts/test` directory.

##### 2. Run the scale tests.

The scale tests are located in `test/integration/scale` directory. The test will create a deployment with 1000 pods and measures the pod startup latency. It asserts that all 1000 pods be ready within 5 minutes. The test is run three times on repeat and must pass each time. 
```
KUBE_CONFIG_PATH=<path-to-kube-config> # Update the kube-config path
ginkgo -v --timeout 30m -- --cluster-kubeconfig=$KUBE_CONFIG_PATH --cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id=$VPC_ID
```

##### 3. Delete EKS cluster and other resources.

The below script uninstalls Karpenter on the clusters, deletes the CFN stack, and finally deletes the EKS cluster.
```
./scripts/test/delete-cluster-karpenter.sh
```

References:
1. Karpenter Getting Started Guide: https://karpenter.sh/docs/getting-started/getting-started-with-karpenter/