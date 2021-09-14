## Ginkgo Test Suites

All Ginkgo Integration test suites are located in `test/integration` directory.

### Prerequisite
The integration test requires the following setup

**For Security Group for Pods Test**
- Have all Nitro Based Instances in your EKS Cluster.
- Have at least 3 X c5.xlarge instance type or larger in terms of number of ENI/IP allocatable.

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
   (cd webhook && CGO_ENABLED=0 GOOS=$OS ginkgo -v -timeout 10m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id $VPC_ID)
   echo "Running Security Group for Pods Integration Tests"
   (cd perpodsg && CGO_ENABLED=0 GOOS=$OS ginkgo -v -timeout 40m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id $VPC_ID)
   echo "Running Windows Integration Tests"
   (cd windows && CGO_ENABLED=0 GOOS=$OS ginkgo -v -timeout 40m -- -cluster-kubeconfig=$KUBE_CONFIG_PATH -cluster-name=$CLUSTER_NAME --aws-region=$AWS_REGION --aws-vpc-id $VPC_ID)
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

### Future Work
- Once we have more test suites, we can provide a script instead of invoking each suite manually.
- Add Windows tests to the list once the support is enabled.
- Move the script based tests in `integration-test` to Ginkgo Based integration/e2e test.