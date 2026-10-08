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
	"strings"
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
	dto "github.com/prometheus/client_model/go"
	batchV1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	controllerRestartHangGuard = 10 * time.Minute
)

type controllerRestart struct {
	kubernetesClient     kubernetes.Interface
	previousLeader       controller.LeaderLease
	leaderHolderIdentity string
	leaderPodName        string
	logsSince            metav1.Time
}

type branchENIAtRestart struct {
	associationID string
	branchENIID   string
	trunkENIID    string
}

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
				CreateAndWaitTillPodIsRunning(ctx, serverPod, utils.ResourceCreationTimeout)
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

					pods, err := frameWork.PodManager.GetPodsWithLabel(
						ctx, namespace, jobPodLabelKey, jobPodLabelVal)
					Expect(err).ToNot(HaveOccurred())

					restartContext, cancel := context.WithTimeout(ctx, controllerRestartHangGuard)
					defer cancel()
					restart := stopControllerAndWaitForLeaseExpiry(restartContext)
					restart.startAndWaitForNodeRestore(
						restartContext, nodeNamesForPods(pods), nil)

					By("verifying the Running Pods don't have their ENIs deleted")
					verify.VerifyNetworkingOfAllPodUsingENI(namespace, jobPodLabelKey, jobPodLabelVal,
						securityGroups)

					restart.expectLeaderUnchanged(restartContext)
				})
			})

			Context("[LOCAL] when jobs is completed and controller restarts", func() {
				BeforeEach(func() {
					testNodeCount = 1
					numJobs = 1
					jobSleepSeconds = 100
				})

				It("job networking of completed pod should be removed", func() {
					CreateJobAndWaitTillItRuns(jobs)

					By("getting the job pods")
					pods, err := frameWork.PodManager.GetPodsWithLabel(ctx, namespace, jobPodLabelKey,
						jobPodLabelVal)
					Expect(err).ToNot(HaveOccurred())
					nodeNames := nodeNamesForPods(pods)
					branchENIs := branchENIsAtRestart(ctx, pods)

					restartContext, cancel := context.WithTimeout(ctx, controllerRestartHangGuard)
					defer cancel()
					restart := stopControllerAndWaitForLeaseExpiry(restartContext)

					By("waiting till the job completes")
					for _, nodeJobs := range jobs {
						for _, job := range nodeJobs {
							observedJob := &batchV1.Job{}
							Expect(wait.PollUntilContextCancel(
								restartContext,
								utils.PollIntervalShort,
								true,
								func(ctx context.Context) (bool, error) {
									if err := frameWork.K8sClient.Get(
										ctx, client.ObjectKeyFromObject(job), observedJob); err != nil {
										return false, err
									}
									if observedJob.Status.Failed > 0 {
										return false, fmt.Errorf("job %s failed", job.Name)
									}
									return observedJob.Status.Succeeded == *job.Spec.Parallelism, nil
								},
							)).To(Succeed())
						}
					}

					Expect(verifyBranchENIsAssociated(restartContext, branchENIs)).To(Succeed())
					restart.startAndWaitForNodeRestore(restartContext, nodeNames, branchENIs)
					preparedAt := restart.waitForTargetTrunkPreparationWithoutCleanup(
						restartContext, branchENIs)

					By("waiting for completed pod networking to be removed after preparation")
					waitForBranchENIsDeleted(ctx, branchENIs, preparedAt)
				})
			})

			Context("[LOCAL] when running jobs are deleted while controller is down", func() {
				BeforeEach(func() {
					testNodeCount = 1
					jobSleepSeconds = 120
				})

				It("deleted job networking should be removed when controller starts up", func() {
					CreateJobAndWaitTillItRuns(jobs)

					By("getting the job pods")
					pods, err := frameWork.PodManager.GetPodsWithLabel(ctx, namespace, jobPodLabelKey,
						jobPodLabelVal)
					Expect(err).ToNot(HaveOccurred())
					nodeNames := nodeNamesForPods(pods)
					branchENIs := branchENIsAtRestart(ctx, pods)

					restartContext, cancel := context.WithTimeout(ctx, controllerRestartHangGuard)
					defer cancel()
					restart := stopControllerAndWaitForLeaseExpiry(restartContext)
					DeleteJobAndPodAndWaitTillDeleted(jobs)
					for index := range pods {
						Expect(frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(
							restartContext, &pods[index])).To(Succeed())
					}

					Expect(verifyBranchENIsAssociated(restartContext, branchENIs)).To(Succeed())
					restart.startAndWaitForNodeRestore(restartContext, nodeNames, branchENIs)
					preparedAt := restart.waitForTargetTrunkPreparationWithoutCleanup(
						restartContext, branchENIs)

					By("waiting for deleted pod networking to be removed after preparation")
					waitForBranchENIsDeleted(ctx, branchENIs, preparedAt)
				})
			})
		})
	})
})

func stopControllerAndWaitForLeaseExpiry(waitContext context.Context) *controllerRestart {
	restConfig, err := clientcmd.BuildConfigFromFlags("", frameWork.Options.KubeConfig)
	Expect(err).ToNot(HaveOccurred())
	kubernetesClient, err := kubernetes.NewForConfig(restConfig)
	Expect(err).ToNot(HaveOccurred())

	previousLeader := controller.StopControllerAndWaitForLeaseExpiry(
		waitContext,
		frameWork.ControllerManager,
		frameWork.DeploymentManager,
	)

	return &controllerRestart{
		kubernetesClient: kubernetesClient,
		previousLeader:   previousLeader,
	}
}

func (restart *controllerRestart) startAndWaitForNodeRestore(
	waitContext context.Context,
	nodeNames []string,
	branchENIsToProtect []branchENIAtRestart,
) {
	restart.logsSince = metav1.Now()
	leader := controller.StartControllerAndWaitForNewLeader(
		waitContext,
		frameWork.ControllerManager,
		frameWork.DeploymentManager,
		restart.previousLeader,
	)
	restart.leaderHolderIdentity = leader.HolderIdentity
	restart.leaderPodName = leader.PodName
	if len(branchENIsToProtect) > 0 {
		Expect(verifyBranchENIsAssociated(waitContext, branchENIsToProtect)).To(Succeed())
		Expect(restart.verifyNoBranchCleanupCalls(waitContext)).To(Succeed())
	}

	By("waiting for the target nodes to restore from their checkpoints")
	Expect(wait.PollUntilContextCancel(
		waitContext,
		utils.PollIntervalShort,
		true,
		func(ctx context.Context) (bool, error) {
			events, err := restart.controllerLogEvents(
				ctx,
				"restored stable instance and trunk state from CNINode checkpoint",
				nodeNames,
			)
			return len(events) == len(nodeNames), err
		},
	)).To(Succeed())
}

func (restart *controllerRestart) expectLeaderUnchanged(waitContext context.Context) {
	By("verifying the leader lease holder did not change")
	leader, err := frameWork.ControllerManager.WaitForActiveLeader(waitContext, "")
	Expect(err).ToNot(HaveOccurred())
	Expect(leader.HolderIdentity).To(Equal(restart.leaderHolderIdentity))
}

func (restart *controllerRestart) controllerLogEvents(
	waitContext context.Context,
	message string,
	identifiers []string,
) (map[string]time.Time, error) {
	logs, err := restart.kubernetesClient.CoreV1().
		Pods(controller.Namespace).
		GetLogs(restart.leaderPodName, &v1.PodLogOptions{
			Container:  "controller",
			SinceTime:  &restart.logsSince,
			Timestamps: true,
		}).
		DoRaw(waitContext)
	if err != nil {
		return nil, err
	}

	events := make(map[string]time.Time, len(identifiers))
	for _, line := range strings.Split(string(logs), "\n") {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 || !strings.Contains(parts[1], message) {
			continue
		}
		eventTime, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return nil, fmt.Errorf("parsing controller log timestamp %q: %w", parts[0], err)
		}
		for _, identifier := range identifiers {
			if strings.Contains(parts[1], identifier) {
				events[identifier] = eventTime
			}
		}
	}
	return events, nil
}

func (restart *controllerRestart) verifyNoBranchCleanupCalls(
	waitContext context.Context,
) error {
	// These counters belong to the restarted leader and assume a dedicated cluster with no other node's leftover branches.
	rawMetrics, err := restart.kubernetesClient.CoreV1().
		Pods(controller.Namespace).
		ProxyGet(
			"http",
			restart.leaderPodName,
			"8443",
			"metrics",
			nil,
		).
		DoRaw(waitContext)
	if err != nil {
		return err
	}

	deleteCalls, err := prometheusCounterValue(
		rawMetrics,
		"ec2_delete_network_interface_api_req_count",
	)
	if err != nil {
		return err
	}
	disassociateCalls, err := prometheusCounterValue(
		rawMetrics,
		"ec2_disassociate_trunk_interface_api_req_count",
	)
	if err != nil {
		return err
	}
	if deleteCalls != 0 || disassociateCalls != 0 {
		return fmt.Errorf(
			"branch cleanup ran before preparation: delete=%v disassociate=%v",
			deleteCalls,
			disassociateCalls,
		)
	}
	return nil
}

func prometheusCounterValue(rawMetrics []byte, name string) (float64, error) {
	metrics, err := utils.RetrieveTestedMetricValue(rawMetrics, name, dto.MetricType_COUNTER)
	if err != nil {
		return 0, err
	}
	if len(metrics) != 1 {
		return 0, fmt.Errorf("expected one %s metric, found %d", name, len(metrics))
	}
	return metrics[0].GetCounter().GetValue(), nil
}

func nodeNamesForPods(pods []v1.Pod) []string {
	Expect(pods).NotTo(BeEmpty())

	seen := make(map[string]struct{}, len(pods))
	var nodeNames []string
	for _, pod := range pods {
		Expect(pod.Spec.NodeName).NotTo(BeEmpty())
		if _, found := seen[pod.Spec.NodeName]; found {
			continue
		}
		seen[pod.Spec.NodeName] = struct{}{}
		nodeNames = append(nodeNames, pod.Spec.NodeName)
	}
	return nodeNames
}

func branchENIsAtRestart(
	waitContext context.Context,
	pods []v1.Pod,
) []branchENIAtRestart {
	nodeToTrunkENI := make(map[string]string)
	for _, nodeName := range nodeNamesForPods(pods) {
		cniNode, err := frameWork.NodeManager.GetCNINodeContext(
			waitContext,
			&v1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(cniNode.Status.TrunkInterface).NotTo(BeNil())
		Expect(cniNode.Status.TrunkInterface.ID).NotTo(BeEmpty())
		nodeToTrunkENI[nodeName] = cniNode.Status.TrunkInterface.ID
	}

	var branchENIs []branchENIAtRestart
	for _, pod := range pods {
		eniDetails, err := frameWork.PodManager.GetENIDetailsFromPodAnnotation(pod.Annotations)
		Expect(err).ToNot(HaveOccurred())
		Expect(eniDetails).NotTo(BeEmpty())
		for _, eni := range eniDetails {
			Expect(eni.ID).NotTo(BeEmpty())
			Expect(eni.AssociationID).NotTo(BeEmpty())
			branchENIs = append(branchENIs, branchENIAtRestart{
				associationID: eni.AssociationID,
				branchENIID:   eni.ID,
				trunkENIID:    nodeToTrunkENI[pod.Spec.NodeName],
			})
		}
	}
	return branchENIs
}

func (restart *controllerRestart) waitForTargetTrunkPreparationWithoutCleanup(
	waitContext context.Context,
	branchENIs []branchENIAtRestart,
) map[string]time.Time {
	Expect(branchENIs).NotTo(BeEmpty())
	targetTrunkENIID := branchENIs[0].trunkENIID
	for _, branchENI := range branchENIs {
		Expect(branchENI.trunkENIID).To(Equal(targetTrunkENIID))
	}

	var preparedAt time.Time
	By("waiting for the restored trunk to be prepared")
	Expect(wait.PollUntilContextCancel(
		waitContext,
		utils.PollIntervalShort,
		true,
		func(ctx context.Context) (bool, error) {
			events, err := restart.controllerLogEvents(
				ctx,
				"validated restored trunk and recovered branch state before allocation",
				[]string{targetTrunkENIID},
			)
			if err != nil {
				return false, err
			}
			eventTime, prepared := events[targetTrunkENIID]
			if !prepared {
				return false, restart.verifyNoBranchCleanupCalls(ctx)
			}
			if err := verifyBranchENIsAssociated(ctx, branchENIs); err != nil {
				return false, err
			}
			if err := restart.verifyNoBranchCleanupCalls(ctx); err != nil {
				return false, err
			}
			preparedAt = eventTime
			return true, nil
		},
	)).To(Succeed())

	return map[string]time.Time{targetTrunkENIID: preparedAt}
}

func verifyBranchENIsAssociated(
	waitContext context.Context,
	branchENIs []branchENIAtRestart,
) error {
	for _, branchENI := range branchENIs {
		associated, err := frameWork.EC2Manager.IsBranchENIAssociated(
			waitContext,
			branchENI.associationID,
			branchENI.branchENIID,
			branchENI.trunkENIID,
		)
		if err != nil {
			return err
		}
		if !associated {
			return fmt.Errorf(
				"branch ENI %s is not associated with trunk %s",
				branchENI.branchENIID,
				branchENI.trunkENIID,
			)
		}
	}
	return nil
}

func waitForBranchENIsDeleted(
	waitContext context.Context,
	branchENIs []branchENIAtRestart,
	preparedAt map[string]time.Time,
) {
	deletionResults := make(chan error, len(branchENIs))
	for _, branchENI := range branchENIs {
		trunkPreparedAt, found := preparedAt[branchENI.trunkENIID]
		Expect(found).To(BeTrue())

		// Allow one 30-second delete tick and 30 seconds of scheduling margin.
		branchENIDeletionDeadline := trunkPreparedAt.Add(
			cooldown.DefaultCoolDownPeriod + time.Minute,
		)
		go func(branchENIID string, deadline time.Time) {
			deleteContext, cancel := context.WithDeadline(waitContext, deadline)
			defer cancel()
			if err := frameWork.EC2Manager.WaitTillTheENIIsDeleted(
				deleteContext,
				branchENIID,
			); err != nil {
				deletionResults <- fmt.Errorf("waiting for branch ENI %s deletion: %w", branchENIID, err)
				return
			}
			deletionResults <- nil
		}(branchENI.branchENIID, branchENIDeletionDeadline)
	}
	for range branchENIs {
		Expect(<-deletionResults).To(Succeed())
	}
}

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
