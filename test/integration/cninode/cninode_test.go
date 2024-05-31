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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	testUtils "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ = Describe("[CANARY]CNINode test", func() {
	Describe("CNINode count verification on adding or removing node", func() {
		var oldMinSize int64
		var oldMaxSize int64
		var newMinSize int64
		var newMaxSize int64
		var asgName string
		BeforeEach(func() {
			By("getting autoscaling group name")
			asgName = ListNodesAndGetAutoScalingGroupName()
			asg, err := frameWork.AutoScalingManager.DescribeAutoScalingGroup(asgName)
			Expect(err).ToNot(HaveOccurred())
			oldMinSize = *asg[0].MinSize
			oldMaxSize = *asg[0].MaxSize

			// Keep atleast one instance running in the cluster when restoring the values in AfterEach
			if oldMinSize == 0 {
				oldMinSize = 1
			}
		})
		AfterEach(func() {
			By("restoring ASG minSize & maxSize after test")
			err := frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, oldMinSize, oldMaxSize)
			Expect(err).ToNot(HaveOccurred())
			// sleep to allow ASG to be updated
			time.Sleep(testUtils.ResourceCreationTimeout)
		})

		Context("when new node is added", func() {
			It("it should create new CNINode", func() {
				newMinSize = oldMinSize + 1
				newMaxSize = oldMaxSize + 1
				// Update ASG to set new minSize and new maxSize
				By("adding new node")
				err := frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, newMinSize, newMaxSize)
				Expect(err).ToNot(HaveOccurred())
				// sleep to allow ASG to be updated
				time.Sleep(testUtils.ResourceCreationTimeout)

				err = node.VerifyCNINodeCount(frameWork.NodeManager)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("when existing node is removed", func() {
			It("it should delete CNINode", func() {
				newMinSize = oldMinSize - 1
				newMaxSize = oldMaxSize - 1
				// Update ASG to set new minSize and new maxSize
				By("removing existing node")
				err := frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, newMinSize, newMaxSize)
				Expect(err).ToNot(HaveOccurred())
				// sleep to allow ASG to be updated
				time.Sleep(testUtils.ResourceCreationTimeout)
				err = node.VerifyCNINodeCount(frameWork.NodeManager)
				Expect(err).ToNot(HaveOccurred())

			})
		})
	})

	Describe("CNINode is re-created when node exists", func() {
		Context("when CNINode is deleted but node exists", func() {
			It("it should re-create CNINode", func() {
				nodeList, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
				Expect(err).ToNot(HaveOccurred())
				cniNode, err := frameWork.NodeManager.GetCNINode(&nodeList.Items[0])
				Expect(err).ToNot(HaveOccurred())
				err = frameWork.NodeManager.DeleteCNINode(cniNode)
				Expect(err).ToNot(HaveOccurred())
				time.Sleep(testUtils.PollIntervalShort) // allow time to re-create CNINode
				_, err = frameWork.NodeManager.GetCNINode(&nodeList.Items[0])
				Expect(err).ToNot(HaveOccurred())
				VerifyCNINodeFields(cniNode)
			})
		})

	})

	Describe("CNINode update tests", func() {
		var cniNode *v1alpha1.CNINode
		var node *v1.Node
		BeforeEach(func() {
			nodeList, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
			Expect(err).ToNot(HaveOccurred())
			node = &nodeList.Items[0]
			cniNode, err = frameWork.NodeManager.GetCNINode(node)
			Expect(err).ToNot(HaveOccurred())
			VerifyCNINodeFields(cniNode)
		})
		AfterEach(func() {
			time.Sleep(testUtils.PollIntervalShort)
			newCNINode, err := frameWork.NodeManager.GetCNINode(node)
			Expect(err).ToNot(HaveOccurred())
			// Verify CNINode after update matches CNINode before update
			Expect(newCNINode).To(BeComparableTo(cniNode, cmp.Options{
				cmpopts.IgnoreTypes(metav1.TypeMeta{}),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "Generation", "ManagedFields"),
			}))
		})

		Context("when finalizer is removed", func() {
			It("it should add the finalizer", func() {
				cniNodeCopy := cniNode.DeepCopy()
				controllerutil.RemoveFinalizer(cniNodeCopy, config.NodeTerminationFinalizer)
				err := frameWork.NodeManager.UpdateCNINode(cniNode, cniNodeCopy)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("when Tags is removed", func() {
			It("it should add the Tags", func() {
				cniNodeCopy := cniNode.DeepCopy()
				cniNodeCopy.Spec.Tags = map[string]string{}
				err := frameWork.NodeManager.UpdateCNINode(cniNode, cniNodeCopy)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("when Label is removed", func() {
			It("it should add the label", func() {
				cniNodeCopy := cniNode.DeepCopy()
				cniNodeCopy.ObjectMeta.Labels = map[string]string{}
				err := frameWork.NodeManager.UpdateCNINode(cniNode, cniNodeCopy)
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})

})

func ListNodesAndGetAutoScalingGroupName() string {
	By("getting instance details")
	nodeList, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
	Expect(err).ToNot(HaveOccurred())
	Expect(nodeList.Items).ToNot(BeEmpty())
	instanceID := frameWork.NodeManager.GetInstanceID(&nodeList.Items[0])
	Expect(instanceID).ToNot(BeEmpty())
	instance, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
	Expect(err).ToNot(HaveOccurred())
	tags := utils.GetTagKeyValueMap(instance.Tags)
	val, ok := tags["aws:autoscaling:groupName"]
	Expect(ok).To(BeTrue())
	return val
}

// Verify finalizer, tag, and label is set on new CNINode
func VerifyCNINodeFields(cniNode *v1alpha1.CNINode) {
	By("verifying finalizer is set")
	Expect(cniNode.ObjectMeta.Finalizers).To(ContainElement(config.NodeTerminationFinalizer))
	// For maps, ContainElement searches through the map's values.
	By("verifying cluster name tag is set")
	Expect(cniNode.Spec.Tags).To(ContainElement(frameWork.Options.ClusterName))
	Expect(config.CNINodeClusterNameKey).To(BeKeyOf(cniNode.Spec.Tags))

	By("verifying node OS label is set")
	Expect(cniNode.ObjectMeta.Labels).To(ContainElement(config.OSLinux))
	Expect(config.NodeLabelOS).To(BeKeyOf(cniNode.ObjectMeta.Labels))
}
