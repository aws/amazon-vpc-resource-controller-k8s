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

package cninode_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	nodeWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	testUtils "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	nodeNameReuseNamespace = testUtils.ResourceNamePrefix + "node-name-reuse"
	nodeNameReuseLabel     = "node-name-reuse"
	// testHoldFinalizer keeps a Node or CNINode for as long as a spec needs it.
	testHoldFinalizer = "vpcresources.k8s.aws/integration-test-hold"
)

// describeNodeNameReuse reproduces EC2 private-IP reuse: the suite instance is terminated and a new instance takes
// its private IP, so it registers under the same node name while the controller still holds state for the old one.
func describeNodeNameReuse() {
	var launchTemplate ec2types.LaunchTemplateSpecification
	var securityGroups []string

	BeforeAll(func(ctx SpecContext) {
		instance, err := frameWork.EC2Manager.GetInstanceDetailsContext(ctx, suiteNodeInstanceID)
		Expect(err).ToNot(HaveOccurred())
		if aws.ToString(instance.PrivateDnsName) != suiteNodeName {
			Skip(fmt.Sprintf("node %s is not named after its private IP, so a new instance cannot reuse the name",
				suiteNodeName))
		}
		securityGroups = lo.Map(instance.SecurityGroups, func(group ec2types.GroupIdentifier, _ int) string {
			return aws.ToString(group.GroupId)
		})

		asg, err := frameWork.AutoScalingManager.DescribeAutoScalingGroup(ctx, suiteASGName)
		Expect(err).ToNot(HaveOccurred())
		member, found := lo.Find(asg[0].Instances, func(member autoscalingtypes.Instance) bool {
			return aws.ToString(member.InstanceId) == suiteNodeInstanceID
		})
		Expect(found).To(BeTrue(), "instance %s is not in Auto Scaling group %s", suiteNodeInstanceID, suiteASGName)
		if member.LaunchTemplate == nil {
			Skip(fmt.Sprintf("Auto Scaling group %s does not launch instances from a launch template", suiteASGName))
		}
		launchTemplate = ec2types.LaunchTemplateSpecification{
			LaunchTemplateId: member.LaunchTemplate.LaunchTemplateId,
			Version:          member.LaunchTemplate.Version,
		}

		Expect(frameWork.NSManager.CreateNamespace(ctx, nodeNameReuseNamespace)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			Expect(frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, nodeNameReuseNamespace)).To(Succeed())
		})
		sgp, err := manifest.NewSGPBuilder().
			Namespace(nodeNameReuseNamespace).
			PodMatchLabel("role", nodeNameReuseLabel).
			SecurityGroup(securityGroups).
			Build()
		Expect(err).ToNot(HaveOccurred())
		Expect(frameWork.K8sClient.Create(ctx, sgp)).To(Succeed())
		createPodWithBranchENI(ctx, "before-reuse")
	})

	It("keeps the trunk of the new instance inside the old instance's delete window", func(ctx SpecContext) {
		newNode := replaceSuiteInstanceWithSameIP(ctx, launchTemplate)

		By("waiting past the delayed cleanup of the old instance")
		time.Sleep(branch.NodeDeleteRequeueRequestDelay + 30*time.Second)
		pod := createPodWithBranchENI(ctx, "after-delete-window")
		verifyNodeNetworking(ctx, newNode, pod)
	})

	It("moves to the new node a CNINode that is still being deleted", func(ctx SpecContext) {
		By("holding the CNINode of the old instance in deletion")
		oldCNINode, err := frameWork.NodeManager.GetCNINodeContext(ctx, suiteNode())
		Expect(err).ToNot(HaveOccurred())
		held := oldCNINode.DeepCopy()
		controllerutil.AddFinalizer(held, testHoldFinalizer)
		Expect(frameWork.NodeManager.UpdateCNINode(oldCNINode, held)).To(Succeed())
		DeferCleanup(releaseCNINodeHold)

		newNode := replaceSuiteInstanceWithSameIP(ctx, launchTemplate)

		By("verifying the deleting CNINode is not re-created for the old owner while it is held")
		Eventually(func(g Gomega) {
			cniNode, err := frameWork.NodeManager.GetCNINodeContext(ctx, newNode)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cniNode.UID).To(Equal(oldCNINode.UID))
			g.Expect(cniNode.DeletionTimestamp).ToNot(BeNil())
		}, testUtils.ResourceCreationTimeout, testUtils.PollIntervalShort).WithContext(ctx).Should(Succeed())

		releaseCNINodeHold(ctx)

		By("verifying the new CNINode belongs to the new node and keeps the Security Groups for Pods feature")
		Eventually(func(g Gomega) {
			cniNode, err := frameWork.NodeManager.GetCNINodeContext(ctx, newNode)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cniNode.UID).ToNot(Equal(oldCNINode.UID))
			g.Expect(cniNode.OwnerReferences).To(ContainElement(HaveField("UID", newNode.UID)))
			g.Expect(cniNode.Spec.Features).To(ContainElement(HaveField("Name", v1alpha1.SecurityGroupsForPods)))
		}, testUtils.ResourceCreationTimeout, testUtils.PollIntervalShort).WithContext(ctx).Should(Succeed())
		pod := createPodWithBranchENI(ctx, "after-cninode-replaced")
		verifyNodeNetworking(ctx, newNode, pod)
	})
}

// replaceSuiteInstanceWithSameIP terminates the suite instance, launches a new one with the same private IP, and
// returns the Ready node of the new instance, which has the suite node name.
func replaceSuiteInstanceWithSameIP(
	ctx context.Context,
	launchTemplate ec2types.LaunchTemplateSpecification,
) *v1.Node {
	old, err := frameWork.EC2Manager.GetInstanceDetailsContext(ctx, suiteNodeInstanceID)
	Expect(err).ToNot(HaveOccurred())
	tags := lo.Filter(old.Tags, func(tag ec2types.Tag, _ int) bool {
		return !strings.HasPrefix(aws.ToString(tag.Key), "aws:")
	})

	// Pods bound to the old instance would otherwise be handed to the new kubelet with the same node name.
	Expect(frameWork.PodManager.DeleteAllPodsForcefully(ctx, "role", nodeNameReuseLabel)).To(Succeed())

	// EC2 releases the private IP anywhere from seconds to minutes after termination. Holding the old Node until the
	// new instance has the IP starts the controller's delayed cleanup right before the new instance boots, and keeps
	// the new kubelet from registering while the old Node still exists.
	By(fmt.Sprintf("holding node %s until a new instance has its private IP", suiteNodeName))
	oldNode, err := frameWork.NodeManager.GetNodeContext(ctx, suiteNode())
	Expect(err).ToNot(HaveOccurred())
	held := oldNode.DeepCopy()
	controllerutil.AddFinalizer(held, testHoldFinalizer)
	Expect(frameWork.K8sClient.Patch(ctx, held, client.MergeFrom(oldNode))).To(Succeed())
	DeferCleanup(releaseNodeHold, oldNode.UID)

	By(fmt.Sprintf("terminating instance %s of node %s", suiteNodeInstanceID, suiteNodeName))
	Expect(terminateSuiteInstance(ctx)).To(Succeed())

	By(fmt.Sprintf("launching a new instance with private IP %s", aws.ToString(old.PrivateIpAddress)))
	newInstanceID, err := frameWork.EC2Manager.RunInstanceWithPrivateIP(ctx, launchTemplate, old.InstanceType,
		aws.ToString(old.SubnetId), aws.ToString(old.PrivateIpAddress), tags)
	Expect(err).ToNot(HaveOccurred())
	suiteNodeInstanceID = newInstanceID
	suiteNodeInAutoScalingGroup = false
	fmt.Fprintf(GinkgoWriter, "CNINode suite launched instance %s outside its Auto Scaling group; "+
		"remove this instance manually if the suite is interrupted\n", newInstanceID)

	By(fmt.Sprintf("deleting node %s of the terminated instance", suiteNodeName))
	releaseNodeHold(ctx, oldNode.UID)
	Expect(wait.PollUntilContextTimeout(ctx, testUtils.PollIntervalShort, testUtils.ResourceCreationTimeout, true,
		func(ctx context.Context) (bool, error) {
			node, err := frameWork.NodeManager.GetNodeContext(ctx, suiteNode())
			return apierrors.IsNotFound(err) || (err == nil && node.UID != oldNode.UID), nil
		})).To(Succeed())

	By(fmt.Sprintf("waiting for node %s to register for instance %s", suiteNodeName, newInstanceID))
	var newNode *v1.Node
	observed := "no node"
	err = wait.PollUntilContextTimeout(ctx, testUtils.PollIntervalShort, testUtils.ResourceCreationTimeout, true,
		func(ctx context.Context) (bool, error) {
			node, err := frameWork.NodeManager.GetNodeContext(ctx, suiteNode())
			if err != nil {
				observed = err.Error()
				return false, nil
			}
			observed = fmt.Sprintf("node uid %s, provider ID %s, ready %t, created %s", node.UID,
				node.Spec.ProviderID, nodeWrapper.IsNodeReady(node), node.CreationTimestamp)
			if frameWork.NodeManager.GetInstanceID(node) != newInstanceID {
				return false, nil
			}
			newNode = node
			return nodeWrapper.IsNodeReady(node), nil
		})
	Expect(err).ToNot(HaveOccurred(), "node %s did not register for instance %s, last observed: %s",
		suiteNodeName, newInstanceID, observed)
	return newNode
}

// createPodWithBranchENI creates a pod on the suite node that matches the Security Group Policy and verifies it
// gets a branch ENI.
func createPodWithBranchENI(ctx context.Context, name string) *v1.Pod {
	pod, err := manifest.NewDefaultPodBuilder().
		Namespace(nodeNameReuseNamespace).
		Name(name).
		NodeName(suiteNodeName).
		Labels(map[string]string{"role": nodeNameReuseLabel}).
		Build()
	Expect(err).ToNot(HaveOccurred())

	By(fmt.Sprintf("creating pod %s and waiting for its branch ENI", name))
	pod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, pod, testUtils.ResourceCreationTimeout)
	Expect(err).ToNot(HaveOccurred())
	Expect(pod.Annotations).To(HaveKey(config.ResourceNamePodENI))
	return pod
}

// verifyNodeNetworking verifies that the node's networking belongs to its current instance: the instance has a trunk
// ENI, the pod's branch ENIs are associated with that trunk, and the CNINode is owned by the node and records the
// instance and its trunk.
func verifyNodeNetworking(ctx context.Context, node *v1.Node, pod *v1.Pod) {
	instanceID := frameWork.NodeManager.GetInstanceID(node)
	By(fmt.Sprintf("verifying the trunk, branch ENIs, and CNINode of instance %s", instanceID))
	instance, err := frameWork.EC2Manager.GetInstanceDetailsContext(ctx, instanceID)
	Expect(err).ToNot(HaveOccurred())
	trunkENI, found := lo.Find(instance.NetworkInterfaces, func(eni ec2types.InstanceNetworkInterface) bool {
		return aws.ToString(eni.InterfaceType) == "trunk"
	})
	Expect(found).To(BeTrue(), "instance %s has no trunk ENI", instanceID)
	trunkENIID := aws.ToString(trunkENI.NetworkInterfaceId)

	branchENIs, err := frameWork.PodManager.GetENIDetailsFromPodAnnotation(pod.Annotations)
	Expect(err).ToNot(HaveOccurred())
	Expect(branchENIs).ToNot(BeEmpty())
	for _, branchENI := range branchENIs {
		associated, err := frameWork.EC2Manager.IsBranchENIAssociated(ctx, branchENI.AssociationID, branchENI.ID,
			trunkENIID)
		Expect(err).ToNot(HaveOccurred())
		Expect(associated).To(BeTrue(), "branch ENI %s of pod %s is not associated with trunk ENI %s",
			branchENI.ID, pod.Name, trunkENIID)
	}

	Eventually(func(g Gomega) {
		cniNode, err := frameWork.NodeManager.GetCNINodeContext(ctx, node)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cniNode.DeletionTimestamp).To(BeNil())
		g.Expect(cniNode.OwnerReferences).To(ContainElement(HaveField("UID", node.UID)))
		g.Expect(cniNode.Spec.Features).To(ContainElement(HaveField("Name", v1alpha1.SecurityGroupsForPods)))
		g.Expect(cniNode.Status.NodeNetworkState).ToNot(BeNil())
		g.Expect(cniNode.Status.NodeNetworkState.InstanceID).To(Equal(instanceID))
		g.Expect(cniNode.Status.TrunkInterface).ToNot(BeNil())
		g.Expect(cniNode.Status.TrunkInterface.ID).To(Equal(trunkENIID))
	}, testUtils.PollTimeout, testUtils.PollIntervalShort).WithContext(ctx).Should(Succeed())
}

// releaseNodeHold removes the test finalizer from the Node with the given UID and deletes it, if it is still there.
func releaseNodeHold(ctx context.Context, uid types.UID) {
	node := &v1.Node{}
	err := frameWork.K8sClient.Get(ctx, types.NamespacedName{Name: suiteNodeName}, node)
	if apierrors.IsNotFound(err) || (err == nil && node.UID != uid) {
		return
	}
	Expect(err).ToNot(HaveOccurred())
	released := node.DeepCopy()
	controllerutil.RemoveFinalizer(released, testHoldFinalizer)
	Expect(frameWork.K8sClient.Patch(ctx, released, client.MergeFrom(node))).To(Succeed())
	if node.DeletionTimestamp.IsZero() {
		Expect(client.IgnoreNotFound(frameWork.K8sClient.Delete(ctx, released))).To(Succeed())
	}
}

// releaseCNINodeHold removes the test finalizer from the suite node's CNINode, if it is still there.
func releaseCNINodeHold(ctx context.Context) {
	cniNode := &v1alpha1.CNINode{}
	err := frameWork.K8sClient.Get(ctx, types.NamespacedName{Name: suiteNodeName}, cniNode)
	if apierrors.IsNotFound(err) || !controllerutil.ContainsFinalizer(cniNode, testHoldFinalizer) {
		return
	}
	Expect(err).ToNot(HaveOccurred())
	released := cniNode.DeepCopy()
	controllerutil.RemoveFinalizer(released, testHoldFinalizer)
	Expect(frameWork.NodeManager.UpdateCNINode(cniNode, released)).To(Succeed())
}
