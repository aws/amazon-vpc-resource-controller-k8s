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
	"testing"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	pkgUtils "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	verifier "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/verify"

	"github.com/aws/aws-sdk-go-v2/aws"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

var frameWork *framework.Framework
var verify *verifier.PodVerification
var securityGroupID1 string
var securityGroupID2 string
var ctx context.Context
var err error
var nodeList *v1.NodeList

func TestPerPodGG(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Per Pod Security Group Suite")
}

var _ = BeforeSuite(func() {
	By("creating a framework")
	frameWork = framework.New(framework.GlobalOptions)
	ctx = context.Background()
	verify = verifier.NewPodVerification(frameWork, ctx)

	securityGroupID1, err = frameWork.EC2Manager.ReCreateSG(utils.ResourceNamePrefix+"sg-1", ctx)
	Expect(err).ToNot(HaveOccurred())
	securityGroupID2, err = frameWork.EC2Manager.ReCreateSG(utils.ResourceNamePrefix+"sg-2", ctx)
	Expect(err).ToNot(HaveOccurred())

	// Reused nodes can have every ENI slot taken, leaving no room for a trunk ENI; recycle for fresh nodes.
	recycleLinuxNodes()

	nodeList = node.GetNodeAndWaitTillCapacityPresent(frameWork.NodeManager, "linux",
		config.ResourceNamePodENI)
	err = node.VerifyCNINode(frameWork.NodeManager)
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID1)).To(Succeed())
	Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID2)).To(Succeed())
})

// recycleLinuxNodes replaces the linux nodes via an ASG instance refresh so a free
// ENI slot is available for the trunk ENI. Nodes reused from the preceding CNI arm
// can have every ENI attached (maxENI reached). No-op when the nodes are already
// usable or aren't part of an ASG.
func recycleLinuxNodes() {
	nodes, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
	Expect(err).ToNot(HaveOccurred())
	if len(nodes.Items) == 0 || allNodesHaveResource(nodes, config.ResourceNamePodENI) {
		return
	}

	instanceID := frameWork.NodeManager.GetInstanceID(&nodes.Items[0])
	Expect(instanceID).ToNot(BeEmpty())
	instance, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
	Expect(err).ToNot(HaveOccurred())
	asgName, ok := pkgUtils.GetTagKeyValueMap(instance.Tags)["aws:autoscaling:groupName"]
	if !ok {
		By("skipping node recycle: linux nodes are not part of an autoscaling group")
		return
	}

	By("recycling linux nodes via instance refresh")
	// MinHealthyPercentage 0: the canary ASGs are tiny, so replace every node.
	refreshID, err := frameWork.AutoScalingManager.StartInstanceRefresh(asgName, &autoscalingtypes.RefreshPreferences{
		MinHealthyPercentage: aws.Int32(0),
		InstanceWarmup:       aws.Int32(0),
	})
	Expect(err).ToNot(HaveOccurred())

	Expect(wait.PollUntilContextTimeout(context.Background(), utils.PollIntervalMedium, 15*time.Minute, true,
		func(ctx context.Context) (bool, error) {
			refresh, err := frameWork.AutoScalingManager.DescribeInstanceRefresh(asgName, refreshID)
			if err != nil {
				return false, nil
			}
			switch refresh.Status {
			case autoscalingtypes.InstanceRefreshStatusSuccessful:
				return true, nil
			case autoscalingtypes.InstanceRefreshStatusFailed,
				autoscalingtypes.InstanceRefreshStatusCancelled,
				autoscalingtypes.InstanceRefreshStatusRollbackFailed,
				autoscalingtypes.InstanceRefreshStatusRollbackSuccessful:
				return false, fmt.Errorf("instance refresh %s ended in status %q", refreshID, refresh.Status)
			default:
				return false, nil
			}
		})).To(Succeed())
}

// allNodesHaveResource reports whether every node advertises the given allocatable resource.
func allNodesHaveResource(nodes *v1.NodeList, resource string) bool {
	for _, n := range nodes.Items {
		if _, ok := n.Status.Allocatable[v1.ResourceName(resource)]; !ok {
			return false
		}
	}
	return true
}
