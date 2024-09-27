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

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	testUtils "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/util/wait"
)

var _ = Describe("[CANARY]CNINode test", func() {
	Describe("CNINode count verification on adding or removing node", func() {
		var oldDesiredSize int64
		var oldMinSize int64
		var oldMaxSize int64
		var newSize int64
		var asgName string
		BeforeEach(func() {
			By("getting autoscaling group name")
			asgName = ListNodesAndGetAutoScalingGroupName()
			asg, err := frameWork.AutoScalingManager.DescribeAutoScalingGroup(asgName)
			Expect(err).ToNot(HaveOccurred())
			oldDesiredSize = *asg[0].DesiredCapacity
			oldMinSize = *asg[0].MinSize
			oldMaxSize = *asg[0].MaxSize
		})
		AfterEach(func() {
			By("restoring ASG minSize & maxSize after test")
			err := frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, oldDesiredSize, oldMinSize, oldMaxSize)
			Expect(err).ToNot(HaveOccurred())
			Expect(WaitTillNodeSizeUpdated(int(oldDesiredSize))).Should(Succeed())
		})

		Context("when new node is added", func() {
			It("it should create new CNINode", func() {
				newSize = oldDesiredSize + 1
				// Update ASG to set desiredSize
				By("adding new node")
				err := frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, newSize, oldMinSize, newSize)
				Expect(err).ToNot(HaveOccurred())
				Expect(WaitTillNodeSizeUpdated(int(newSize))).Should(Succeed())
				Expect(node.VerifyCNINode(frameWork.NodeManager)).Should(Succeed())
			})
		})
		Context("when existing node is removed", func() {
			It("it should delete CNINode", func() {
				newSize = oldDesiredSize - 1
				// Update ASG to set new minSize and new maxSize
				By("removing existing node")
				err := frameWork.AutoScalingManager.UpdateAutoScalingGroup(asgName, newSize, newSize, oldMaxSize)
				Expect(err).ToNot(HaveOccurred())
				Expect(WaitTillNodeSizeUpdated(int(newSize))).Should(Succeed())
				Expect(node.VerifyCNINode(frameWork.NodeManager)).Should(Succeed())
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

// Verifies (linux) node size is updated after ASG is updated
func WaitTillNodeSizeUpdated(desiredSize int) error {
	err := wait.PollUntilContextTimeout(context.Background(), testUtils.PollIntervalShort, testUtils.ResourceCreationTimeout, true,
		func(ctx context.Context) (bool, error) {
			nodes, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux) // since we are only updating the linux ASG in the test
			if err != nil {
				return false, nil
			}
			if len(nodes.Items) != desiredSize {
				return false, nil
			}
			return true, nil
		})
	return err
}
