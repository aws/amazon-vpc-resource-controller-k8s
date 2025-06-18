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

package perpodsg_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/controller"
	sgpWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchV1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ControllerInitWaitPeriod is the time to wait for the controller
// to start running, acquire the lease and initialize it's caches
const ControllerInitWaitPeriod = time.Second * 90

var _ = Describe("Security Group Per Pod", func() {
	var (
		// num of times new jobs that will be created on the same node
		numJobs int
		// number of different nodes to run the test on
		testNodeCount  int
		namespace      string
		securityGroups []string

		// serverPod to which all the Jobs will connect to
		// verify networking
		serverPod  *v1.Pod
		serverPort int
		// List of jobs, each job list is mapped to a single node
		jobs            map[string][]*batchV1.Job
		jobSleepSeconds int

		sgp               *v1beta1.SecurityGroupPolicy
		serverPodLabelKey string
		serverPodLabelVal string
		jobPodLabelKey    string
		jobPodLabelVal    string

		err error
		ctx context.Context
	)

	Describe("Jobs", func() {
		BeforeEach(func() {
			numJobs = 1
			testNodeCount = 1
			namespace = "sgp-job"
			securityGroups = []string{securityGroupID1}

			jobPodLabelKey = "app"
			jobPodLabelVal = "sgp-job"

			serverPodLabelKey = "app"
			serverPodLabelVal = "sgp-app"

			serverPort = 80
			// On creating multiple Jobs on a node, if the Job execution time is low (<5 seconds)
			// then the Status/IP on the Completed Pod is not updated by kubelet on some occasion.
			// We use the Status IP and match it with the Annotation from VPC Resource Controller
			// to ensure the ENI IP is allocated to Pod and not a regular secondary IPv4 Address.
			// See: https://github.com/kubernetes/kubernetes/issues/39113
			jobSleepSeconds = 5

			jobs = make(map[string][]*batchV1.Job)
			ctx = context.TODO()
		})

		JustBeforeEach(func() {
			Expect(len(nodeList.Items)).To(BeNumerically(">=", testNodeCount))

			By("creating the namespace")
			err = frameWork.NSManager.CreateNamespace(ctx, namespace)
			Expect(err).ToNot(HaveOccurred())

			sgp, err = manifest.NewSGPBuilder().
				Namespace(namespace).
				SecurityGroup(securityGroups).
				Name("job-sgp").
				PodMatchExpression(serverPodLabelKey,
					metav1.LabelSelectorOpIn, serverPodLabelVal, jobPodLabelVal).
				Build()
			Expect(err).ToNot(HaveOccurred())

			sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, sgp)

			serverContainer := manifest.NewBusyBoxContainerBuilder().
				Name("sgp-server").
				Image("nginx").
				AddContainerPort(v1.ContainerPort{
					ContainerPort: int32(serverPort),
				}).
				Command(nil).
				Build()

			// Need the TerminationGracePeriod because of the following issue in IPAMD
			// https://github.com/aws/amazon-vpc-cni-k8s/issues/1313#issuecomment-901818609
			serverPod, err = manifest.NewDefaultPodBuilder().
				Namespace(namespace).
				Name("sgp-server").
				Container(serverContainer).
				Labels(map[string]string{serverPodLabelKey: serverPodLabelVal}).
				TerminationGracePeriod(30).
				Build()
			Expect(err).ToNot(HaveOccurred())

			By("creating the server pod")
			serverPod, err = frameWork.PodManager.
				CreateAndWaitTillPodIsRunning(ctx, serverPod, utils.ResourceOperationTimeout)
			Expect(err).ToNot(HaveOccurred())

			for i := 0; i < testNodeCount; i++ {
				testNode := nodeList.Items[i]
				maxPodENICapacity, found := testNode.Status.Allocatable[config.ResourceNamePodENI]
				Expect(found).To(BeTrue())

				var jobList []*batchV1.Job
				for j := 0; j < numJobs; j++ {
					// The Job Pod tests HTTP connection to server Pod which
					// acts as a High Level check for SGP Pod Networking
					jobContainer := manifest.NewBusyBoxContainerBuilder().
						Image("curlimages/curl").
						Command([]string{"/bin/sh"}).
						Args([]string{"-c",
							fmt.Sprintf(
								"set -e; curl --fail --max-time 7 --retry 3 %s; sleep %d;",
								serverPod.Status.PodIP, jobSleepSeconds)}).
						Build()

					// Create More Jobs then supported capacity on the Nodes. If Networking
					// for Completed Pods is not removed, then second batch of Jobs should
					// not run and test should fail.
					job := manifest.NewLinuxJob().
						Name(fmt.Sprintf("job-node-%d-job-count-%d", i, j)).
						Namespace(namespace).
						Parallelism(int(maxPodENICapacity.Value()-1)). // To accommodate for server Pod
						Container(jobContainer).
						PodLabels(jobPodLabelKey, jobPodLabelVal).
						ForNode(testNode.Name).
						TerminationGracePeriod(30).
						Build()
					jobList = append(jobList, job)
				}
				jobs[testNode.Name] = jobList
			}
		})

		JustAfterEach(func() {
			By("deleting the namespace")
			err = frameWork.NSManager.
				DeleteAndWaitTillNamespaceDeleted(ctx, namespace)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when jobs run successfully", func() {
			JustBeforeEach(func() {
				By("authorizing ingress to server port")
				err = frameWork.EC2Manager.
					AuthorizeSecurityGroupIngress(securityGroups[0], serverPort, "TCP")
				Expect(err).ToNot(HaveOccurred())
			})

			JustAfterEach(func() {
				By("revoking ingress to server port")
				err = frameWork.EC2Manager.
					RevokeSecurityGroupIngress(securityGroups[0], serverPort, "TCP")
				Expect(err).ToNot(HaveOccurred())
			})

			Context("when jobs are completed", func() {
				BeforeEach(func() {
					numJobs = 4
					testNodeCount = 3
				})

				// Add Canary focus once https://github.com/aws/amazon-vpc-cni-k8s/issues/1746 is resolved
				It("completed job's networking should be removed", func() {
					VerifyJobNetworkingRemovedOnCompletion(jobs, namespace,
						jobPodLabelKey, jobPodLabelVal)
				})
			})

			Context("when jobs are currently running", func() {
				BeforeEach(func() {
					jobSleepSeconds = 200
				})

				It("job networking should not be removed", func() {
					CreateJobAndWaitTillItRuns(jobs)

					By("verifying pod networking is not removed for running pods")
					verify.VerifyNetworkingOfAllPodUsingENI(namespace, jobPodLabelKey, jobPodLabelVal,
						securityGroups)
				})
			})

			Context("[LOCAL] when jobs are running and controller restarts", func() {
				BeforeEach(func() {
					testNodeCount = 3
					jobSleepSeconds = 600
				})

				It("job networking should not be removed", func() {
					CreateJobAndWaitTillItRuns(jobs)

					By("restarting the controller")
					leaderName := controller.RestartController(ctx, frameWork.ControllerManager,
						frameWork.DeploymentManager)

					By("verifying the Running Pods don't have their ENIs deleted")
					verify.VerifyNetworkingOfAllPodUsingENI(namespace, jobPodLabelKey, jobPodLabelVal,
						securityGroups)

					controller.VerifyLeaseHolderIsSame(ctx, frameWork.ControllerManager, leaderName)
				})
			})

			Context("[LOCAL] when jobs is completed and controller restarts", func() {
				BeforeEach(func() {
					testNodeCount = 2
					numJobs = 1
					jobSleepSeconds = 100
				})

				It("job networking of completed pod should be removed", func() {
					CreateJobAndWaitTillItRuns(jobs)

					controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 0)

					By("waiting till the job completes")
					time.Sleep(time.Second * time.Duration(jobSleepSeconds))

					controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 2)

					By("waiting till controller inits and removes completed pod networking")
					time.Sleep(ControllerInitWaitPeriod) // This account for 30 seconds cool down period too

					By("verifying completed pod have their networking removed")
					verify.VerifyPodENIDeletedForAllPods(namespace, jobPodLabelKey, jobPodLabelVal)
				})
			})

			Context("[LOCAL] when running jobs are deleted while controller is down", func() {
				BeforeEach(func() {
					testNodeCount = 2
					jobSleepSeconds = 120
				})

				It("deleted job networking should be removed when controller starts up", func() {
					CreateJobAndWaitTillItRuns(jobs)

					controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 0)

					By("getting the job pods")
					pods, err := frameWork.PodManager.GetPodsWithLabel(ctx, namespace, jobPodLabelKey,
						jobPodLabelVal)
					Expect(err).ToNot(HaveOccurred())

					DeleteJobAndPodAndWaitTillDeleted(jobs)

					controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 2)

					By("waiting till controller inits and removes deleted pod networking")
					time.Sleep(ControllerInitWaitPeriod) // This account for 30 seconds cool down period too

					By("verifying the pod ENI are deleted when controller comes back up")
					for _, pod := range pods {
						verify.VerifyPodENIDeleted(pod)
					}
				})
			})
		})
	})
})

func CreateJobAndWaitTillItRuns(jobs map[string][]*batchV1.Job) {
	By("creating job and waiting till it runs")
	for _, nodeJobs := range jobs {
		for _, job := range nodeJobs {
			job, err = frameWork.JobManager.CreateJobAndWaitForJobToRun(ctx, job)
			Expect(err).ToNot(HaveOccurred())
		}
	}
}
func DeleteJobAndPodAndWaitTillDeleted(jobs map[string][]*batchV1.Job) {
	By("deleting job and waiting till its deleted")
	for _, nodeJobs := range jobs {
		for _, job := range nodeJobs {
			err := frameWork.JobManager.DeleteAndWaitTillJobIsDeleted(ctx, job)
			Expect(err).ToNot(HaveOccurred())
		}
	}
}

func VerifyJobNetworkingRemovedOnCompletion(jobs map[string][]*batchV1.Job,
	namespace string, podLabelKey string, podLabelVal string) {
	CreateAndWaitForJobsInParallel(jobs)

	By("waiting for the ENI to be cooled down and deleted")
	// Need to account for actual deletion of ENI + Cool down Period
	time.Sleep(cooldown.DefaultCoolDownPeriod * 2)

	By("verifying the deleted Pod have their ENI deleted")
	verify.VerifyPodENIDeletedForAllPods(namespace, podLabelKey, podLabelVal)
}

func CreateAndWaitForJobsInParallel(jobs map[string][]*batchV1.Job) {
	var wg sync.WaitGroup
	for nodeName, jobs := range jobs {
		wg.Add(1)
		// Parallelize the job creation and validation on each node. This
		// will help stress the controller and possibly catch regressions
		go func(nodeName string, jobs []*batchV1.Job) {
			defer GinkgoRecover()
			defer wg.Done()
			// On each node create job with parallelization count equal to
			// the capacity of pod eni on the node. Once the first job succeeds,
			// launch the second job and so on. If the networking for the older
			// Pod is not released by controller we will observer that the new
			// Job has failed to start up.
			for i, job := range jobs {
				By(fmt.Sprintf("creating job %d on node %s", i, nodeName))
				_, err := frameWork.JobManager.CreateAndWaitForJobToComplete(ctx, job)
				Expect(err).ToNot(HaveOccurred())
			}
		}(nodeName, jobs)
	}
	// Wait for Pods to be Completed on all of the Nodes
	wg.Wait()
}
