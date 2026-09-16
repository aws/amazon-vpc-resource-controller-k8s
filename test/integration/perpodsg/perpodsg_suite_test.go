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
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	pkgUtils "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/aws/autoscaling"
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
	expectedLinuxNodes := recycleLinuxNodes()

	nodeList = node.GetNodeAndWaitTillCapacityPresent(frameWork.NodeManager, "linux",
		config.ResourceNamePodENI, expectedLinuxNodes)
	err = node.VerifyCNINode(frameWork.NodeManager)
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID1)).To(Succeed())
	Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID2)).To(Succeed())
})

// recycleLinuxNodes refreshes every ASG backing a linux node so each comes back
// with a free trunk-ENI slot, returning the pre-refresh node count. No-op when
// nodes already advertise pod-eni or none are in an ASG.
func recycleLinuxNodes() int {
	nodes, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
	Expect(err).ToNot(HaveOccurred())
	expectedNodeCount := len(nodes.Items)
	if expectedNodeCount == 0 || allNodesReadyWithResource(nodes, config.ResourceNamePodENI) {
		return expectedNodeCount
	}

	asgNames := map[string]struct{}{}
	var nonASGNodes []string
	for i := range nodes.Items {
		instanceID := frameWork.NodeManager.GetInstanceID(&nodes.Items[i])
		Expect(instanceID).ToNot(BeEmpty())
		instance, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
		Expect(err).ToNot(HaveOccurred())
		if asgName, ok := pkgUtils.GetTagKeyValueMap(instance.Tags)["aws:autoscaling:groupName"]; ok {
			asgNames[asgName] = struct{}{}
		} else {
			nonASGNodes = append(nonASGNodes, nodes.Items[i].Name)
		}
	}
	if len(asgNames) == 0 {
		By("skipping node recycle: linux nodes are not part of an autoscaling group")
		return expectedNodeCount
	}
	Expect(nonASGNodes).To(BeEmpty(),
		"linux nodes are not part of an autoscaling group and cannot be recycled: %v", nonASGNodes)

	By("recycling linux nodes via instance refresh")
	refreshInstanceGroups(asgNames)
	return expectedNodeCount
}

func refreshInstanceGroups(asgNames map[string]struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	refreshIDs := map[string]string{}
	for asgName := range asgNames {
		refreshID, startErr := frameWork.AutoScalingManager.StartInstanceRefresh(ctx, asgName,
			&autoscalingtypes.RefreshPreferences{
				MinHealthyPercentage: aws.Int32(100),
				MaxHealthyPercentage: aws.Int32(200),
				InstanceWarmup:       aws.Int32(0),
			})
		if startErr != nil {
			cancelInstanceRefreshes(ctx, refreshIDs)
			Expect(startErr).ToNot(HaveOccurred(), "starting instance refresh for asg %s", asgName)
		}
		refreshIDs[asgName] = refreshID
	}

	var wg sync.WaitGroup
	refreshErrs := make(chan error, len(refreshIDs))
	for asgName, refreshID := range refreshIDs {
		wg.Add(1)
		go func(asgName, refreshID string) {
			defer wg.Done()
			refreshErrs <- waitForInstanceRefresh(ctx, asgName, refreshID)
		}(asgName, refreshID)
	}
	wg.Wait()
	close(refreshErrs)

	var errs []error
	for e := range refreshErrs {
		if e != nil {
			errs = append(errs, e)
		}
	}
	Expect(errors.Join(errs...)).To(Succeed())
}

func waitForInstanceRefresh(ctx context.Context, asgName, refreshID string) error {
	return wait.PollUntilContextCancel(ctx, utils.PollIntervalMedium, true,
		func(ctx context.Context) (bool, error) {
			refresh, err := frameWork.AutoScalingManager.DescribeInstanceRefresh(ctx, asgName, refreshID)
			if err != nil {
				// Not-yet-visible is retryable; other errors are permanent.
				if errors.Is(err, autoscaling.ErrInstanceRefreshNotFound) {
					return false, nil
				}
				return false, fmt.Errorf("describing instance refresh %s for asg %s: %w", refreshID, asgName, err)
			}
			switch refresh.Status {
			case autoscalingtypes.InstanceRefreshStatusSuccessful:
				return true, nil
			case autoscalingtypes.InstanceRefreshStatusFailed,
				autoscalingtypes.InstanceRefreshStatusCancelled,
				autoscalingtypes.InstanceRefreshStatusRollbackFailed,
				autoscalingtypes.InstanceRefreshStatusRollbackSuccessful:
				return false, fmt.Errorf("instance refresh %s for asg %s ended in status %q: %s",
					refreshID, asgName, refresh.Status, aws.ToString(refresh.StatusReason))
			default:
				return false, nil
			}
		})
}

func cancelInstanceRefreshes(ctx context.Context, refreshIDs map[string]string) {
	for asgName := range refreshIDs {
		if err := frameWork.AutoScalingManager.CancelInstanceRefresh(ctx, asgName); err != nil {
			GinkgoWriter.Printf("failed to cancel instance refresh for asg %s: %v\n", asgName, err)
		}
	}
}

// allNodesReadyWithResource reports whether every node is non-deleting, Ready, and
// advertises a positive quantity of the resource. Stale/NotReady nodes that still
// advertise it must not short-circuit a recycle.
func allNodesReadyWithResource(nodes *v1.NodeList, resource string) bool {
	for i := range nodes.Items {
		n := nodes.Items[i]
		if n.DeletionTimestamp != nil || !nodeReady(&n) {
			return false
		}
		if q, ok := n.Status.Allocatable[v1.ResourceName(resource)]; !ok || q.CmpInt64(0) <= 0 {
			return false
		}
	}
	return true
}

func nodeReady(n *v1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == v1.NodeReady {
			return c.Status == v1.ConditionTrue
		}
	}
	return false
}
