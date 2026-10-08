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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/aws-sdk-go-v2/aws"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCNINode(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CNINode Test Suite")
}

type suiteNodeDetails struct {
	InstanceID string `json:"instanceID"`
	NodeName   string `json:"nodeName"`
}

var (
	frameWork           *framework.Framework
	suiteASGName        string
	suiteASGDesiredSize int32
	suiteNodeInstanceID string
	suiteNodeName       string
)

var _ = SynchronizedBeforeSuite(func(ctx SpecContext) []byte {
	initializeFramework()

	nodeList, err := frameWork.NodeManager.GetNodesWithOSContext(ctx, config.OSLinux)
	Expect(err).ToNot(HaveOccurred())
	Expect(nodeList.Items).ToNot(BeEmpty())
	existingNodes := make(map[string]struct{}, len(nodeList.Items))
	for _, existingNode := range nodeList.Items {
		existingNodes[existingNode.Name] = struct{}{}
	}

	asgName := autoScalingGroupName(ctx, nodeList)
	asg, err := frameWork.AutoScalingManager.DescribeAutoScalingGroup(ctx, asgName)
	Expect(err).ToNot(HaveOccurred())
	Expect(asg).To(HaveLen(1))
	Expect(asg[0].DesiredCapacity).ToNot(BeNil())
	Expect(asg[0].MaxSize).ToNot(BeNil())

	desiredSize := aws.ToInt32(asg[0].DesiredCapacity)
	suiteASGName = asgName
	suiteASGDesiredSize = desiredSize
	newDesiredSize := desiredSize + 1
	maxSize := aws.ToInt32(asg[0].MaxSize)
	Expect(newDesiredSize).To(
		BeNumerically("<=", maxSize),
		"cannot add a node to Auto Scaling group %s: desired capacity %d plus one exceeds max size %d; max size will not be changed",
		asgName,
		desiredSize,
		maxSize,
	)

	existingInstances := make(map[string]struct{}, len(asg[0].Instances))
	for _, instance := range asg[0].Instances {
		existingInstances[aws.ToString(instance.InstanceId)] = struct{}{}
	}

	By(fmt.Sprintf(
		"increasing Auto Scaling group %s desired capacity from %d to %d",
		asgName,
		desiredSize,
		newDesiredSize,
	))
	err = frameWork.AutoScalingManager.UpdateAutoScalingGroup(
		ctx,
		asgName,
		aws.Int32(newDesiredSize),
	)
	Expect(err).ToNot(HaveOccurred())

	instanceID, err := waitForNewInstanceInAutoScalingGroup(ctx, asgName, existingInstances)
	Expect(err).ToNot(HaveOccurred())
	suiteNodeInstanceID = instanceID
	fmt.Fprintf(
		GinkgoWriter,
		"CNINode suite added instance %s; remove this instance manually if the suite is interrupted\n",
		suiteNodeInstanceID,
	)

	newNode, err := waitForReadyNode(ctx, suiteNodeInstanceID, existingNodes)
	Expect(err).ToNot(HaveOccurred())
	suiteNodeName = newNode.Name
	Expect(waitTillCNINodePresent(ctx, newNode)).To(Succeed())
	fmt.Fprintf(
		GinkgoWriter,
		"CNINode suite instance %s is Ready as node %s with a CNINode\n",
		suiteNodeInstanceID,
		suiteNodeName,
	)

	data, err := json.Marshal(suiteNodeDetails{
		InstanceID: suiteNodeInstanceID,
		NodeName:   suiteNodeName,
	})
	Expect(err).ToNot(HaveOccurred())
	return data
}, func(data []byte) {
	initializeFramework()

	details := suiteNodeDetails{}
	Expect(json.Unmarshal(data, &details)).To(Succeed())
	Expect(details.InstanceID).ToNot(BeEmpty())
	Expect(details.NodeName).ToNot(BeEmpty())
	suiteNodeInstanceID = details.InstanceID
	suiteNodeName = details.NodeName
})

var _ = SynchronizedAfterSuite(func() {}, func(ctx SpecContext) {
	if suiteASGName == "" {
		return
	}

	var terminateErr error
	if suiteNodeInstanceID != "" {
		fmt.Fprintf(
			GinkgoWriter,
			"CNINode suite removing instance %s for node %s\n",
			suiteNodeInstanceID,
			suiteNodeName,
		)
		terminateErr = frameWork.AutoScalingManager.TerminateInstanceInAutoScalingGroup(
			ctx,
			suiteNodeInstanceID,
			true,
		)
	}
	restoreErr := frameWork.AutoScalingManager.UpdateAutoScalingGroup(
		ctx,
		suiteASGName,
		aws.Int32(suiteASGDesiredSize),
	)
	Expect(terminateErr).ToNot(HaveOccurred())
	Expect(restoreErr).ToNot(HaveOccurred())

	if suiteNodeName != "" {
		node := suiteNode()
		Expect(waitTillNodeDeleted(ctx, node)).To(Succeed())
		Expect(waitTillCNINodeDeleted(ctx, node)).To(Succeed())
	}
})

func initializeFramework() {
	if frameWork == nil {
		By("creating a framework")
		frameWork = framework.New(framework.GlobalOptions)
	}
}

func suiteNode() *v1.Node {
	return &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: suiteNodeName}}
}
