package scale_test

import (
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	utils_vpc_rc "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	utils_test "github.com/aws/amazon-vpc-resource-controller-k8s/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/apps/v1"
)

var ns = "node-termination-cleaner-scale"
var _ = Describe("Node termination ENI Cleaner Scale test", Ordered, func() {
	var (
		asgName        string
		oldDesiredSize int32
		oldMinSize     int32
		oldMaxSize     int32
		deployment     *v1.Deployment
		eniDetached    []string
	)

	BeforeAll(func() {
		Expect(nodes).To(BeNumerically(">", 0))
		Expect(deletePerMin).To(BeNumerically(">", 0))
		asgName = utils_test.GetAutoScalingGroupName(frameWork, config.OSLinux)
		asg, err := frameWork.AutoScalingManager.DescribeAutoScalingGroup(asgName)
		Expect(err).NotTo(HaveOccurred())
		oldDesiredSize = *asg[0].DesiredCapacity
		oldMinSize = *asg[0].MinSize
		oldMaxSize = *asg[0].MaxSize

		By(fmt.Sprintf("scaling the cluster to %d nodes", nodes))
		Expect(frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, int32(nodes), 0, int32(nodes))).To(Succeed())
		Eventually(func() int {
			list, err := frameWork.NodeManager.GetCNINodeList()
			Expect(err).NotTo(HaveOccurred())
			return len(list.Items)
		}, utils.ResourceOperationTimeout, utils.PollIntervalMedium).Should(BeNumerically("==", nodes))

		By("Creating name space")
		Expect(frameWork.NSManager.CreateNamespace(ctx, ns)).To(Succeed())

		By("deploying pods to 70% capacity")
		nodeList, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
		Expect(err).NotTo(HaveOccurred())
		podCap, ok := nodeList.Items[0].Status.Allocatable.Pods().AsInt64()
		Expect(ok).To(BeTrue())
		replicas := int(podCap * int64(len(nodeList.Items)) * 7 / 10)
		fmt.Println("replicas", replicas)
		deployment = manifest.NewDefaultDeploymentBuilder().Namespace(ns).Replicas(replicas).PodLabel("node-scale", "1").Build()
		_, err = frameWork.DeploymentManager.CreateAndWaitUntilDeploymentReady(ctx, deployment)
		Expect(err).NotTo(HaveOccurred())
	})
	AfterAll(func() {

		By("deleting deployment")
		if deployment != nil {
			Expect(frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)).To(Succeed())
		}

		By("cleaning up all old nodes")
		Expect(frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, 0, oldMinSize, oldMaxSize)).To(Succeed())

		By("waiting for 3 minutes to take clean up all nodes", func() {
			time.Sleep(3 * time.Minute)
		})

		By(fmt.Sprintf("oldDesiredSize %v, oldMinSize %v oldMaxSize %v", oldDesiredSize, oldMinSize, oldMaxSize))
		Expect(frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, oldDesiredSize, oldMinSize, oldMaxSize)).To(Succeed())
		Eventually(func() int {
			list, err := frameWork.NodeManager.GetCNINodeList()
			Expect(err).NotTo(HaveOccurred())
			return len(list.Items)
		}, utils.ResourceOperationTimeout, utils.PollIntervalMedium).Should(BeNumerically("==", oldDesiredSize))

		By("deleting namespace")
		Expect(frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, ns)).To(Succeed())

	})

	It("detaching Secondary ENI from worker nodes", func() {
		nodeList, err := frameWork.NodeManager.GetNodeList()
		Expect(err).NotTo(HaveOccurred())
		var instanceIDs []string
		fmt.Println("no of nodes in list for detachment ", len(nodeList.Items))
		for _, node := range nodeList.Items {
			id, err := utils_vpc_rc.GetNodeID(&node)
			Expect(err).NotTo(HaveOccurred())
			instanceIDs = append(instanceIDs, id)
		}
		instances, err := frameWork.EC2Manager.GetInstances(ctx, instanceIDs)
		Expect(err).NotTo(HaveOccurred())
		fmt.Println("no of instance objects we have", len(instances))
		var zero = int32(0)
		for _, instance := range instances {
			for _, nw := range instance.NetworkInterfaces {
				if *nw.Attachment.DeviceIndex > zero {
					//fmt.Println("eniID", *nw.NetworkInterfaceId)
					eniDetached = append(eniDetached, *nw.NetworkInterfaceId)
					Expect(frameWork.EC2Manager.DetachNetworkInterface(ctx, *nw.Attachment.AttachmentId, true)).To(Succeed())
				}
			}
		}
		fmt.Println("total ENI's detached", len(eniDetached))
	})

	It("waiting for 2 minutes to let eni state sync", func() {
		time.Sleep(2 * time.Minute)
	})

	It(fmt.Sprintf("deleting node at rate of %d per minute", deletePerMin), func() {
		nl, err := frameWork.NodeManager.GetNodeList()
		Expect(err).NotTo(HaveOccurred())
		utils_test.DeleteNodesWithThrottle(frameWork, nl, deletePerMin)
		Eventually(func() int {
			list, err := frameWork.NodeManager.GetCNINodeList()
			Expect(err).NotTo(HaveOccurred())
			return len(list.Items)
		}, utils.ResourceOperationTimeout, utils.PollIntervalMedium).Should(BeNumerically("==", 1))
	})

	It("waiting for 2 minutes for vpc-rc to clean up all ENI", func() {
		time.Sleep(2 * time.Minute)
	})

	It("verifying all detached interfaces are deleted", func() {
		for _, eniId := range eniDetached {
			err := frameWork.EC2Manager.DescribeNetworkInterface(eniId)
			Expect(err).To(HaveOccurred(), fmt.Sprintf("ENI with ID %s was not deleted. with error %s", eniId, err))
		}
	})

})
