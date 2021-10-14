// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package windows_test

import (
	"fmt"

	"math/rand"
	"strconv"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/controller"
	testUtils "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsV1 "k8s.io/api/apps/v1"
	batchV1 "k8s.io/api/batch/v1"
	coreV1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// TODO: Add similar testing for Security Group for Pods

// Tests intended to try breaking the controller by testing the system
// under abnormal conditions and above average windows container workloads
// See test/README.md for more details
var _ = Describe("Windows Integration Stress Tests", func() {
	var (
		namespace string
		// targetedNodeLabels to target the same set of nodes for creating
		// jobs and deployments
		targetedNodeLabels map[string]string
		// number of nodes on which the test will be executed
		testNodeCount int
		// the server pod serving http request to client pods
		serverPod *coreV1.Pod
		// the number of retries that each client will perform to the server Pod,
		// if any of the retry fails then the Pod will error out indicating issues
		// with networking
		clientToServerTries         int
		clientToServerRetryInterval int
		clientDeployment            *appsV1.Deployment
		clientJob                   *batchV1.Job
		// label key-vals for deployment and Jobs, these are used to get the Pod object
		// belonging to the deployment/job
		podRoleLabelKey             string
		clientDeploymentPodLabelVal string
		clientJobPodLabelVal        string
		// the maximum number of Pods that can be created on the set of targeted nodes
		maxPodCount int
		// The resource limit which will be injected to the Pod, this is added by
		// the WebHook running in the controller Pod. However, since we are running
		// scenarios where we are killing both controllers (for simplicity sake)
		// we manually inject resource limit for such scenarios. With HA setup in Prod,
		// the running controller will inject the limits
		resourceLimits map[coreV1.ResourceName]resource.Quantity
		err            error
	)

	BeforeEach(func() {
		// For each test, we will create a label with unique ID for the targeted nodes
		// and create test jobs/deployment on these targeted nodes
		targetedNodeLabels = map[string]string{
			"test-node-selector-id": uuid.New().String(),
		}

		maxPodCount = 0
		namespace = "windows-test"
		clientToServerTries = 5
		clientToServerRetryInterval = 2
		testNodeCount = 3

		podRoleLabelKey = "app"
		clientDeploymentPodLabelVal = "windows-deployment"
		clientJobPodLabelVal = "windows-job"

		// For ideal cases where we are not killing both controllers, pass an empty map
		resourceLimits = map[coreV1.ResourceName]resource.Quantity{}
	})

	JustBeforeEach(func() {
		Expect(len(windowsNodeList.Items)).To(BeNumerically(">=", testNodeCount))
		for i := 0; i < testNodeCount; i++ {
			node := windowsNodeList.Items[i]

			capacity, found := node.Status.Allocatable[config.ResourceNameIPAddress]
			Expect(found).To(BeTrue())

			maxPodCount += int(capacity.Value())
		}

		By("adding labels to selected nodes for testing")
		err = frameWork.NodeManager.AddLabels(windowsNodeList.Items[:testNodeCount],
			targetedNodeLabels)
		Expect(err).ToNot(HaveOccurred())

		By("creating the namespace")
		err = frameWork.NSManager.CreateNamespace(ctx, namespace)
		Expect(err).ToNot(HaveOccurred())

		serverContainer := manifest.NewWindowsContainerBuilder().
			Args([]string{GetCommandToStartHttpServer()}).
			Build()

		By("creating and waiting till Windows server Pod runs")
		serverPod, err = manifest.NewWindowsPodBuilder().
			TerminationGracePeriod(0).
			Namespace(namespace).
			Container(serverContainer).
			Name("server").
			Build()
		Expect(err).ToNot(HaveOccurred())

		serverPod, err = frameWork.PodManager.
			CreateAndWaitTillPodIsRunning(ctx, serverPod, testUtils.ResourceCreationTimeout)
		Expect(err).ToNot(HaveOccurred())

		clientContainer := manifest.NewWindowsContainerBuilder().
			Args([]string{GetCommandToContinuouslyTestHostConnectivity(
				serverPod.Status.PodIP, clientToServerTries, clientToServerRetryInterval)}).
			Resources(coreV1.ResourceRequirements{
				Limits:   resourceLimits,
				Requests: resourceLimits,
			}).Build()

		// Append the Node Label Selector to the Windows Pod, this is required
		// for WebHook to inject limits to the Pod
		nodeSelector := utils.CopyMap(targetedNodeLabels)
		nodeSelector[config.NodeLabelOS] = config.OSWindows

		maxPodCount = maxPodCount - 1 // -1 if server Pod is on the test Node

		clientJob = manifest.NewWindowsJob().
			PodLabels("app", "windows-client").
			Parallelism(maxPodCount/2).
			Namespace(namespace).
			Name("client-job").
			PodLabels(podRoleLabelKey, clientJobPodLabelVal).
			Container(clientContainer).
			TerminationGracePeriod(0).
			NodeSelector(nodeSelector).
			Build()

		By("creating the client deployment configuration")
		clientDeployment = manifest.NewWindowsDeploymentBuilder().
			Namespace(namespace).
			Replicas(maxPodCount/2).
			TerminationGracePeriod(0).
			PodLabel(podRoleLabelKey, clientDeploymentPodLabelVal).
			Container(clientContainer).
			Name("client-deployment").
			NodeSelector(nodeSelector).
			Build()
	})

	JustAfterEach(func() {
		By("removing labels on selected nodes for testing")
		err = frameWork.NodeManager.RemoveLabels(windowsNodeList.Items[:testNodeCount],
			targetedNodeLabels)
		Expect(err).ToNot(HaveOccurred())

		By("deleting the namespace")
		err = frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)
		Expect(err).ToNot(HaveOccurred())
	})

	// Negative test to reinforce the positive test are working as intended
	Describe("[CANARY] when connecting to the a non existent server", func() {
		It("all job should fail", func() {
			By("deleting the server Pod")
			err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, serverPod)
			Expect(err).ToNot(HaveOccurred())

			By("creating job to connect to server and verify it fails")
			_, err = frameWork.JobManager.CreateAndWaitForJobToComplete(ctx, clientJob)
			Expect(err).To(HaveOccurred())
		})
	})

	// These set of tests have the following pattern.
	// 1. Create Jobs on given Node for a fixed interval. These are long running
	//    jobs that continuously test networking connectivity to a Windows Server.
	//    The Job fails if connection to server fails. The purpose of the Job is to
	//    Verify that existing Pod networking is not removed when constant operations
	//    are being performed on the same set of Nodes.
	// 2. Create short lived deployment Pods on the same set of Nodes. The purpose of
	//    the deployment is to create continuous operation on the Nodes in the hopes
	//    catching a regression that removes networking of the long lived Jobs.
	// 3. [Optional] Restart the Controller Deployment either forcefully or gracefully
	//    while the test jobs are running deployments are being scaled up and down.
	Describe("when deployment/jobs are continuously scaled up and down", func() {
		// testFinishedChan will be notified when the main test completes, requesting
		// the parallel routines to stop
		var testFinishedChan chan struct{}

		BeforeEach(func() {
			clientToServerTries = 100 // test the client->server request 100 times
			testFinishedChan = make(chan struct{})
		})

		JustBeforeEach(func() {
			// Create and delete deployments at random interval while the long running
			// test runs in the It block
			go CreateAndDeleteDeploymentAtRandomInterval(30, clientDeployment,
				podRoleLabelKey, clientDeploymentPodLabelVal, testFinishedChan)
		})

		JustAfterEach(func() {
			// Signal the chanel to stop running the continuous deployment creation/deletion
			testFinishedChan <- struct{}{}
		})

		Context("[LOCAL] when controller is restarted when deployments are being created", func() {
			// restartForcefully when set would kill the controller forcefully without
			// respecting the grace period
			var restartForcefully bool

			BeforeEach(func() {
				restartForcefully = false
				resourceLimits = map[coreV1.ResourceName]resource.Quantity{
					config.ResourceNameIPAddress: resource.MustParse("1"),
				}
			})

			JustBeforeEach(func() {
				go RestartControllerAtRandomIntervals(30, restartForcefully, testFinishedChan)
			})

			JustAfterEach(func() {
				testFinishedChan <- struct{}{}
			})

			Context("when controller is gracefully restarted", func() {
				It("all Pod should run without error", func() {
					err = CreateJobAndWaitTillCompleted(clientJob, 1)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context(" when controller is forcefully restarted in between", func() {
				BeforeEach(func() {
					restartForcefully = true
				})

				It("all Pod should run without error", func() {
					err = CreateJobAndWaitTillCompleted(clientJob, 1)
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})

		Context("[CANARY] when controller is not restarted in between", func() {
			It("all pod should run without error", func() {
				err = CreateJobAndWaitTillCompleted(clientJob, 2)
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})

	Describe("when user manually un-assign secondary IPv4 address", func() {
		BeforeEach(func() {
			testNodeCount = 1
			clientToServerTries = 15
		})

		It("controller should auto recover", func() {
			By("creating a deployment")
			deployment := clientDeployment.DeepCopy()

			By("getting the instance ID of the test Node")
			nodeList := windowsNodeList.Items[:testNodeCount]
			testNode := nodeList[0]
			instanceID := manager.GetNodeInstanceID(&testNode)
			Expect(instanceID).ToNot(Equal(""))

			By("creating a windows deployment")
			deployment, err := frameWork.DeploymentManager.
				CreateAndWaitUntilDeploymentReady(ctx, deployment)
			Expect(err).ToNot(HaveOccurred())

			By("getting the pod on the deployment")
			pods, err := frameWork.PodManager.
				GetPodsWithLabel(ctx, namespace, podRoleLabelKey, clientDeploymentPodLabelVal)
			Expect(err).ToNot(HaveOccurred())

			By("removing secondary IPv4 on a used Pod")
			numIPToRemove := 5
			Expect(len(pods)).To(BeNumerically(">=", numIPToRemove))
			var ipsToRemove []string
			for i := 0; i < numIPToRemove; i++ {
				ipsToRemove = append(ipsToRemove, pods[i].Status.PodIP)
			}
			err = frameWork.EC2Manager.UnAssignSecondaryIPv4Address(instanceID, ipsToRemove)
			Expect(err).ToNot(HaveOccurred())

			By("delete the deployment")
			err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)
			Expect(err).ToNot(HaveOccurred())

			By("creating job that test networking")
			clientJob.Spec.Parallelism = aws.Int32(int32(maxPodCount))
			_, err = frameWork.JobManager.CreateAndWaitForJobToComplete(ctx, clientJob)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("[LOCAL] when the RBAC policy of the Service Account is modified by user", func() {
		BeforeEach(func() {
			resourceLimits = map[coreV1.ResourceName]resource.Quantity{
				config.ResourceNameIPAddress: resource.MustParse("1"),
			}
			// Enough retries so the Job runs till the controller restarts
			clientToServerTries = 100
		})

		JustAfterEach(func() {
			controller.RevertClusterRoleChanges(frameWork.RBACManager)
		})

		It("controller should not impact existing Pods", func() {

			By("creating a job and waiting it to run")
			_, err = frameWork.JobManager.CreateJobAndWaitForJobToRun(ctx, clientJob)
			Expect(err).ToNot(HaveOccurred())

			controller.PatchClusterRole(frameWork.RBACManager,
				"pods", []string{"get", "watch", "patch"})

			controller.RestartController(ctx, frameWork.ControllerManager,
				frameWork.DeploymentManager)

			// If the networking for the Pod would have been removed the Job would fail
			verify.ExpectPodHaveDesiredPhase(namespace, podRoleLabelKey,
				clientJobPodLabelVal, []coreV1.PodPhase{coreV1.PodSucceeded, coreV1.PodRunning})
		})
	})
})

// RestartControllerAtFixedInterval restarts controller at particular interval, if force
// option is set the controller Pods are restarted forcefully
func RestartControllerAtRandomIntervals(interval int, force bool, done chan struct{}) {
	defer GinkgoRecover() // Since we are using go routines

	By("starting continuous controller restarts")

	for {
		sleepDuration := rand.Intn(interval)
		ticker := time.NewTicker(1 + time.Duration(sleepDuration)*time.Second)

		select {
		case <-ticker.C:
			if force {
				By("restarting the controller forcefully")
				leader := controller.ForcefullyKillControllerPods(ctx, frameWork.ControllerManager,
					frameWork.PodManager)
				Expect(err).ToNot(HaveOccurred())
				By(fmt.Sprintf("new leader pod is %s", leader))
			} else {
				By("restarting the controller with grace period")
				leader := controller.RestartController(ctx, frameWork.ControllerManager, frameWork.DeploymentManager)
				By(fmt.Sprintf("new leader pod is %s", leader))
			}
		case <-done:
			By("stopping continuous controller restarts")
			return
		}
	}
}

func CreateAndDeleteDeploymentAtRandomInterval(maxInterval int, deployment *appsV1.Deployment,
	podLabelKey string, podLabelVal string, done chan struct{}) {
	defer GinkgoRecover()

	By("starting continuous deployment creation and deletion")

	for {
		sleepDuration := rand.Intn(maxInterval)
		ticker := time.NewTicker(1 + time.Duration(sleepDuration)*time.Second)
		select {

		case <-ticker.C:
			newDeployment := deployment.DeepCopy()
			newDeployment, err = frameWork.DeploymentManager.CreateAndWaitUntilDeploymentReady(ctx, newDeployment)
			Expect(err).ToNot(HaveOccurred())

			verify.WindowsPodsHaveIPv4Address(deployment.Namespace, podLabelKey, podLabelVal)
			verify.ExpectPodHaveDesiredPhase(deployment.Namespace, podLabelKey, podLabelVal,
				[]coreV1.PodPhase{coreV1.PodRunning, coreV1.PodSucceeded})

			err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, newDeployment)
			Expect(err).ToNot(HaveOccurred())
		case <-done:
			By("stopping continuous deployment creation and deletion")
			return
		}
	}
}

func CreateJobAndWaitTillCompleted(clientJob *batchV1.Job, repetition int) error {
	By("creating jobs that continuously test networking")
	for i := 0; i < repetition; i++ {
		By("creating the client job run" + strconv.Itoa(i))

		job := clientJob.DeepCopy()
		job.Name = fmt.Sprintf("client-job-%d", i)

		_, err := frameWork.JobManager.CreateAndWaitForJobToComplete(ctx, job)
		if err != nil {
			return err
		}
	}
	return nil
}
