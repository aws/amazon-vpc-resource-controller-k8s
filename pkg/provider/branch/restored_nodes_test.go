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

package branch

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_trunk "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/trunk"
	mock_worker "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsEC2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sCtrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	restoredInstanceID        = "i-00000000000000001"
	restoredPrimaryENIID      = "eni-00000000000000011"
	restoredTrunkENIID        = "eni-00000000000000012"
	restoredBranchENIID       = "eni-00000000000000013"
	restoredSecondBranchENIID = "eni-00000000000000014"
	restoredSubnetID          = "subnet-00000000000000001"
)

func restoredInstanceForProvider(t *testing.T) ec2.EC2Instance {
	t.Helper()
	instance := ec2.NewEC2Instance(NodeName, restoredInstanceID, config.OSLinux, zap.New())
	require.NoError(t, instance.LoadFromNodeNetworkState(rcv1alpha1.NodeNetworkState{
		InstanceID:                restoredInstanceID,
		InstanceType:              "m5.large",
		SubnetID:                  restoredSubnetID,
		SubnetCIDRBlock:           "10.0.0.0/16",
		PrimaryNetworkInterfaceID: restoredPrimaryENIID,
	}, restoredTrunkENIID))
	require.NoError(t, instance.UpdateCurrentSubnetAndCidrBlock(nil))
	return instance
}

func newRestoredTrunkForProvider(
	t *testing.T,
	ctrl *gomock.Controller,
	pods []v1.Pod,
) (trunk.TrunkENI, *mock_api.MockEC2APIHelper) {
	t.Helper()
	helper := mock_api.NewMockEC2APIHelper(ctrl)
	trunkENI := trunk.NewTrunkENI(zap.New(), restoredInstanceForProvider(t), helper)
	require.NoError(t, trunkENI.InitTrunk(pods))
	return trunkENI, helper
}

// podOwningBranch is a running pod whose annotation names one branch ENI.
func podOwningBranch(t *testing.T, uid, eniID string, vlanID int, associationID string) v1.Pod {
	t.Helper()
	annotation, err := json.Marshal([]*trunk.ENIDetails{{ID: eniID, VlanID: vlanID, AssociationID: associationID}})
	require.NoError(t, err)
	return v1.Pod{ObjectMeta: metav1.ObjectMeta{
		UID:         types.UID(uid),
		Annotations: map[string]string{config.ResourceNamePodENI: string(annotation)},
	}}
}

func branchTaggedWithVLAN(eniID string, vlanID int) *ec2types.NetworkInterface {
	return &ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String(eniID),
		TagSet:             []ec2types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String(strconv.Itoa(vlanID))}},
	}
}

// awaitSignal fails the test instead of hanging when a step never happens.
func awaitSignal[T any](t *testing.T, signal <-chan T, step string) (value T) {
	t.Helper()
	select {
	case value = <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", step)
	}
	return value
}

func restoredInterfacesForProvider() []ec2types.NetworkInterface {
	return []ec2types.NetworkInterface{
		{
			NetworkInterfaceId: aws.String(restoredPrimaryENIID),
			SubnetId:           aws.String(restoredSubnetID),
			PrivateIpAddress:   aws.String("10.0.0.10"),
			Groups:             []ec2types.GroupIdentifier{{GroupId: aws.String("sg-00000000000000001")}},
			Attachment: &ec2types.NetworkInterfaceAttachment{
				InstanceId:  aws.String(restoredInstanceID),
				Status:      ec2types.AttachmentStatusAttached,
				DeviceIndex: aws.Int32(0),
			},
		},
		{
			NetworkInterfaceId: aws.String(restoredTrunkENIID),
			InterfaceType:      ec2types.NetworkInterfaceTypeTrunk,
			Attachment: &ec2types.NetworkInterfaceAttachment{
				InstanceId: aws.String(restoredInstanceID),
				Status:     ec2types.AttachmentStatusAttached,
			},
		},
	}
}

// A first allocation and the background pass share one restored-trunk check, whichever arrives first.
func TestBranchENIProvider_AllocationAndBackgroundShareOneCheck(t *testing.T) {
	for _, allocationFirst := range []bool{true, false} {
		name := "background arrives during the allocation check"
		if !allocationFirst {
			name = "allocation arrives during the background check"
		}
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			provider, podAPI, sgpAPI, k8sAPI := getProviderAndMocks(ctrl)
			conditions := mock_condition.NewMockConditions(ctrl)
			provider.conditions = conditions
			restoredTrunk, helper := newRestoredTrunkForProvider(t, ctrl, nil)
			provider.trunkENICache[NodeName] = restoredTrunk

			allocationAttempts := 1
			if !allocationFirst {
				allocationAttempts = 2
			}
			podAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil).Times(allocationAttempts)
			// Only the attempt that gets past preparation reads the API server and matches the SGP.
			podAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
			sgpAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).Return(SecurityGroups, nil)
			k8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonSecurityGroupRequested, gomock.Any(), v1.EventTypeNormal)
			conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
			var classifications atomic.Int32
			k8sAPI.EXPECT().GetNode(NodeName).DoAndReturn(func(string) (*v1.Node, error) {
				classifications.Add(1)
				return branchEligibleNode(NodeName, restoredInstanceID), nil
			}).AnyTimes()

			podAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
			describeStarted := make(chan struct{})
			releaseDescribe := make(chan struct{})
			helper.EXPECT().DescribeNetworkInterfaces([]string{restoredPrimaryENIID, restoredTrunkENIID}).
				DoAndReturn(func([]string) ([]ec2types.NetworkInterface, error) {
					close(describeStarted)
					<-releaseDescribe
					return restoredInterfacesForProvider(), nil
				})
			helper.EXPECT().GetBranchNetworkInterface(aws.String(restoredTrunkENIID)).Return(nil, nil)
			helper.EXPECT().CreateNetworkInterface(gomock.Any(), aws.String(restoredSubnetID), SecurityGroups,
				gomock.Any(), nil, nil, nil).
				Return(&ec2types.NetworkInterface{
					NetworkInterfaceId: aws.String(restoredBranchENIID),
					MacAddress:         aws.String("02:00:00:00:00:01"),
					PrivateIpAddress:   aws.String("10.0.0.10"),
				}, nil)
			helper.EXPECT().AssociateBranchToTrunk(aws.String(restoredTrunkENIID), aws.String(restoredBranchENIID), 1).
				Return(&awsEC2.AssociateTrunkInterfaceOutput{
					InterfaceAssociation: &ec2types.TrunkInterfaceAssociation{AssociationId: aws.String("trunk-assoc-1")},
				}, nil)
			podAPI.EXPECT().AnnotatePod(MockPodNamespace1, MockPodName1, MockPodUID1, config.ResourceNamePodENI,
				gomock.Any()).Return(nil)
			k8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonResourceAllocated, gomock.Any(), v1.EventTypeNormal)

			backgroundDone := make(chan error, 1)
			runBackground := func() {
				backgroundDone <- provider.runRestoredNodeChecks(
					t.Context(),
					[]restoredNode{restoredNodeForTest(NodeName, restoredInstanceID)},
					0,
					time.Millisecond,
					rate.NewLimiter(rate.Inf, 1),
				)
			}
			type allocationResult struct {
				requeue      bool
				requeueAfter time.Duration
				err          error
			}
			allocate := func() <-chan allocationResult {
				done := make(chan allocationResult, 1)
				go func() {
					result, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)
					done <- allocationResult{requeue: result.Requeue, requeueAfter: result.RequeueAfter, err: err}
				}()
				return done
			}
			if allocationFirst {
				allocationDone := allocate()
				awaitSignal(t, describeStarted, "the allocation check")
				go runBackground()
				require.Eventually(t, func() bool { return classifications.Load() >= 4 }, time.Second, time.Millisecond,
					"background pass did not retry while the allocation held the check")
				select {
				case err := <-backgroundDone:
					close(releaseDescribe)
					t.Fatalf("background pass finished before the trunk was prepared: %v", err)
				default:
				}
				close(releaseDescribe)
				allocation := awaitSignal(t, allocationDone, "the allocation")
				require.NoError(t, allocation.err)
				require.False(t, allocation.requeue)
				select {
				case err := <-backgroundDone:
					require.NoError(t, err)
				case <-time.After(time.Second):
					t.Fatal("background pass did not stop after the shared check succeeded")
				}
			} else {
				go runBackground()
				awaitSignal(t, describeStarted, "the background check")
				deferred := awaitSignal(t, allocate(), "the allocation that arrives during the background check")
				require.NoError(t, deferred.err)
				require.True(t, deferred.requeue)
				require.Equal(t, time.Second, deferred.requeueAfter)
				close(releaseDescribe)
				require.NoError(t, awaitSignal(t, backgroundDone, "the background pass"))
				retried := awaitSignal(t, allocate(), "the requeued allocation")
				require.NoError(t, retried.err)
				require.False(t, retried.requeue)
			}
			require.False(t, restoredTrunk.NeedsPreparation())
			owned := restoredTrunk.Introspect().PodToBranchENI[PodUID1]
			require.Len(t, owned, 1, "a second commit overwrote the allocated branch")
			require.Equal(t, restoredBranchENIID, owned[0].ID)
		})
	}
}

func TestBranchENIProvider_RestoredNodeCheckFailureIsNotGlobal(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, _, _, mockK8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	var restoredNodes []restoredNode
	var attempts []string
	for index := range 3 {
		nodeName := fmt.Sprintf("node-%d", index)
		instanceID := fmt.Sprintf("i-%d", index)
		restoredNodes = append(restoredNodes, restoredNodeForTest(nodeName, instanceID))
		fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
		provider.trunkENICache[nodeName] = fakeTrunk
		mockK8sAPI.EXPECT().GetNode(nodeName).Return(branchEligibleNode(nodeName, instanceID), nil).AnyTimes()
		fakeTrunk.EXPECT().InstanceID().Return(instanceID).AnyTimes()
		prepared := false
		failures := 0
		fakeTrunk.EXPECT().NeedsPreparation().DoAndReturn(func() bool { return !prepared }).AnyTimes()
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
			func() ([]v1.Pod, error),
		) (ec2.EC2Instance, error) {
			attempts = append(attempts, nodeName)
			if nodeName == "node-1" && failures < 2 {
				failures++
				return nil, MockError
			}
			prepared = true
			return nil, nil
		}).AnyTimes()
	}

	checkCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, provider.runRestoredNodeChecks(
		checkCtx,
		restoredNodes,
		0,
		time.Millisecond,
		rate.NewLimiter(rate.Inf, 1),
	))
	require.NoError(t, checkCtx.Err(), "the restored-node pass did not finish")
	require.Equal(t, []string{"node-0", "node-1", "node-2", "node-1", "node-1"}, attempts)
}

func TestBranchENIProvider_RestoredNodeCheckDropsNodeReplacedDuringPreparation(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, _, _, mockK8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	oldTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	replacementTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	// Either signal, the node's provider ID or the cached trunk, may drop the node; the new trunk is never prepared.
	replacementTrunk.EXPECT().InstanceID().Return("i-replacement").AnyTimes()
	provider.trunkENICache[NodeName] = oldTrunk
	gomock.InOrder(
		mockK8sAPI.EXPECT().GetNode(NodeName).Return(branchEligibleNode(NodeName, "i-restored"), nil).Times(2),
		mockK8sAPI.EXPECT().GetNode(NodeName).Return(branchEligibleNode(NodeName, "i-replacement"), nil),
	)
	oldTrunk.EXPECT().InstanceID().Return("i-restored").Times(2)
	oldTrunk.EXPECT().NeedsPreparation().Return(true).Times(2)
	oldTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
		func() ([]v1.Pod, error),
	) (ec2.EC2Instance, error) {
		provider.lock.Lock()
		provider.trunkENICache[NodeName] = replacementTrunk
		provider.lock.Unlock()
		return nil, MockError
	})

	checkCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	require.NoError(t, provider.runRestoredNodeChecks(
		checkCtx,
		[]restoredNode{restoredNodeForTest(NodeName, "i-restored")},
		0,
		time.Millisecond,
		rate.NewLimiter(rate.Inf, 1),
	))
	require.NoError(t, checkCtx.Err(), "the restored-node pass did not finish")
}

// The leader's sweep prepares a restored node only once the ten minute grace has passed.
func TestBranchENIProvider_StartPreparesRestoredNodesAfterTenMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		provider, _, _, k8sAPI := getProviderAndMocks(ctrl)
		conditions := mock_condition.NewMockConditions(ctrl)
		provider.conditions = conditions
		fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
		provider.trunkENICache[NodeName] = fakeTrunk
		k8sAPI.EXPECT().ListCNINodes().Return([]*rcv1alpha1.CNINode{{
			ObjectMeta: metav1.ObjectMeta{Name: NodeName},
			Status: rcv1alpha1.CNINodeStatus{
				NodeNetworkState: &rcv1alpha1.NodeNetworkState{InstanceID: restoredInstanceID, InstanceType: "m5.large"},
				TrunkInterface:   &rcv1alpha1.TrunkInterface{ID: restoredTrunkENIID},
			},
		}}, nil)
		conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
		k8sAPI.EXPECT().GetNode(NodeName).Return(branchEligibleNode(NodeName, restoredInstanceID), nil).Times(2)
		fakeTrunk.EXPECT().InstanceID().Return(restoredInstanceID).Times(2)
		fakeTrunk.EXPECT().NeedsPreparation().Return(true).Times(2)
		var preparations atomic.Int32
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
			func() ([]v1.Pod, error),
		) (ec2.EC2Instance, error) {
			preparations.Add(1)
			return nil, nil
		})

		done := make(chan error, 1)
		go func() { done <- provider.Start(t.Context()) }()
		time.Sleep(599 * time.Second)
		synctest.Wait()
		require.Zero(t, preparations.Load(), "the sweep prepared a restored node before the grace ended")
		time.Sleep(time.Second)
		synctest.Wait()
		require.Equal(t, int32(1), preparations.Load())
		require.NoError(t, <-done)
	})
}

// A node drained and deleted before its restored trunk is prepared still deletes its deleted pods' branch ENIs.
func TestBranchENIProvider_DrainedRestoredNodeDeletesQueuedBranchENIs(t *testing.T) {
	// The bubble's fake clock lets the real cooldown and node-delete delay elapse instantly.
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		provider, podAPI, _, k8sAPI := getProviderAndMocks(ctrl)
		helper := mock_api.NewMockEC2APIHelper(ctrl)
		workerPool := mock_worker.NewMockWorker(ctrl)
		conditions := mock_condition.NewMockConditions(ctrl)
		provider.apiWrapper.EC2API = helper
		provider.workerPool = workerPool
		provider.conditions = conditions
		k8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(nil, MockError)
		cooldown.InitCoolDownPeriod(k8sAPI, zap.New())

		instance := ec2.NewEC2Instance(NodeName, restoredInstanceID, config.OSLinux, zap.New())
		require.NoError(t, instance.LoadFromNodeNetworkState(rcv1alpha1.NodeNetworkState{
			InstanceID:                restoredInstanceID,
			InstanceType:              "m5.large",
			SubnetID:                  restoredSubnetID,
			SubnetCIDRBlock:           "10.0.0.0/16",
			PrimaryNetworkInterfaceID: restoredPrimaryENIID,
		}, restoredTrunkENIID))
		require.NoError(t, instance.UpdateCurrentSubnetAndCidrBlock(nil))
		branchIDs := []string{restoredBranchENIID, restoredSecondBranchENIID}
		var runningPods []v1.Pod
		var branchInterfaces []*ec2types.NetworkInterface
		for index, branchID := range branchIDs {
			vlanID := index + 1
			annotation, err := json.Marshal([]*trunk.ENIDetails{
				{ID: branchID, VlanID: vlanID, AssociationID: "trunk-assoc-" + branchID},
			})
			require.NoError(t, err)
			runningPods = append(runningPods, v1.Pod{ObjectMeta: metav1.ObjectMeta{
				UID:         types.UID(fmt.Sprintf("drained-pod-%d", index)),
				Annotations: map[string]string{config.ResourceNamePodENI: string(annotation)},
			}})
			branchInterfaces = append(branchInterfaces, &ec2types.NetworkInterface{
				NetworkInterfaceId: aws.String(branchID),
				TagSet:             []ec2types.Tag{{Key: aws.String(config.VLandIDTag), Value: aws.String(strconv.Itoa(vlanID))}},
			})
		}

		nodeDeleted := false
		var deletedENIs []string
		podAPI.EXPECT().GetRunningPodsOnNode(NodeName).DoAndReturn(func(string) ([]v1.Pod, error) {
			return runningPods, nil
		}).AnyTimes()
		k8sAPI.EXPECT().GetNode(NodeName).DoAndReturn(func(string) (*v1.Node, error) {
			if nodeDeleted {
				return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, NodeName)
			}
			return branchEligibleNode(NodeName, restoredInstanceID), nil
		}).AnyTimes()
		k8sAPI.EXPECT().BroadcastEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
		k8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, gomock.Any(), restoredTrunkENIID).Return(nil).AnyTimes()
		// DeleteNode clears the checkpoint, so the node cold-inits if it is ever managed again.
		k8sAPI.EXPECT().ClearCNINodeCheckpoint(NodeName).Return(nil)
		workerPool.EXPECT().SubmitJob(gomock.Any()).AnyTimes()
		workerPool.EXPECT().SubmitJobAfter(worker.NewOnDemandDeleteNodeJob(NodeName), NodeDeleteRequeueRequestDelay)
		conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true).AnyTimes()
		// EC2 inventory for any ownership check made before the branch ENIs are deleted.
		helper.EXPECT().DescribeNetworkInterfaces([]string{restoredPrimaryENIID, restoredTrunkENIID}).
			Return(restoredInterfacesForProvider(), nil).AnyTimes()
		helper.EXPECT().GetBranchNetworkInterface(aws.String(restoredTrunkENIID)).Return(branchInterfaces, nil).AnyTimes()
		helper.EXPECT().DisassociateTrunkInterface(gomock.Any()).Return(nil).AnyTimes()
		helper.EXPECT().DeleteNetworkInterface(gomock.Any()).DoAndReturn(func(eniID *string) error {
			deletedENIs = append(deletedENIs, *eniID)
			return nil
		}).AnyTimes()

		// The controller restarts and restores the trunk from the CNINode checkpoint without reading EC2.
		require.NoError(t, provider.InitResource(instance))
		restoredTrunk, _ := provider.getTrunkFromCache(NodeName)
		require.True(t, restoredTrunk.NeedsPreparation())

		// The node is drained: its pods are deleted and their branch ENIs wait out the cooldown.
		deletedPods := runningPods
		runningPods = nil
		for _, pod := range deletedPods {
			_, err := provider.ProcessAsyncJob(worker.NewOnDemandDeletedJob(NodeName, pod.UID))
			require.NoError(t, err)
		}
		time.Sleep(cooldown.GetCoolDown().GetCoolDownPeriod() + time.Second)
		_, err := provider.ProcessAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))
		require.NoError(t, err)

		// The node is deleted before the restored-node pass prepares it, so that pass skips it.
		nodeDeleted = true
		require.NoError(t, provider.DeInitResource(instance))
		require.NoError(t, provider.runRestoredNodeChecks(
			t.Context(),
			[]restoredNode{restoredNodeForTest(NodeName, restoredInstanceID)},
			0,
			time.Millisecond,
			rate.NewLimiter(rate.Inf, 1),
		))
		time.Sleep(NodeDeleteRequeueRequestDelay)
		_, err = provider.ProcessAsyncJob(worker.NewOnDemandDeleteNodeJob(NodeName))
		require.NoError(t, err)
		// Run the requeued cleanup job until it stops, as the worker does.
		for range 20 {
			result, err := provider.ProcessAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))
			require.NoError(t, err)
			if !result.Requeue {
				break
			}
			time.Sleep(result.RequeueAfter)
		}

		require.ElementsMatch(t, branchIDs, deletedENIs,
			"branch ENIs of pods deleted before their restored node was removed were never deleted in EC2")
	})
}

// A node restored after the leader's sweep finished still drains its deleted pods' branch ENIs, from the delete tick.
func TestBranchENIProvider_LateRestoredNodeDrainsFromTheDeleteTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		provider, podAPI, _, k8sAPI := getProviderAndMocks(ctrl)
		helper := mock_api.NewMockEC2APIHelper(ctrl)
		workerPool := mock_worker.NewMockWorker(ctrl)
		conditions := mock_condition.NewMockConditions(ctrl)
		provider.apiWrapper.EC2API = helper
		provider.workerPool = workerPool
		provider.conditions = conditions
		k8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(nil, MockError)
		cooldown.InitCoolDownPeriod(k8sAPI, zap.New())
		k8sAPI.EXPECT().ListCNINodes().Return(nil, nil)
		conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
		require.NoError(t, provider.Start(t.Context()))

		pod := podOwningBranch(t, "late-pod", restoredBranchENIID, 1, "trunk-assoc-1")
		runningPods := []v1.Pod{pod}
		podAPI.EXPECT().GetRunningPodsOnNode(NodeName).DoAndReturn(func(string) ([]v1.Pod, error) {
			return runningPods, nil
		}).Times(2)
		k8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, gomock.Any(), restoredTrunkENIID).Return(nil)
		workerPool.EXPECT().SubmitJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))
		k8sAPI.EXPECT().GetNode(NodeName).Return(branchEligibleNode(NodeName, restoredInstanceID), nil)
		k8sAPI.EXPECT().BroadcastEvent(gomock.Any(), gomock.Any(), gomock.Any(), v1.EventTypeNormal)
		helper.EXPECT().DescribeNetworkInterfaces([]string{restoredPrimaryENIID, restoredTrunkENIID}).
			Return(restoredInterfacesForProvider(), nil)
		helper.EXPECT().GetBranchNetworkInterface(aws.String(restoredTrunkENIID)).
			Return([]*ec2types.NetworkInterface{branchTaggedWithVLAN(restoredBranchENIID, 1)}, nil)
		helper.EXPECT().DisassociateTrunkInterface(aws.String("trunk-assoc-1")).Return(nil)
		helper.EXPECT().DeleteNetworkInterface(aws.String(restoredBranchENIID)).Return(nil)

		require.NoError(t, provider.InitResource(restoredInstanceForProvider(t)))
		runningPods = nil
		_, err := provider.DeleteBranchUsedByPods(NodeName, string(pod.UID))
		require.NoError(t, err)
		time.Sleep(cooldown.GetCoolDown().GetCoolDownPeriod() + time.Second)
		_, err = provider.ProcessDeleteQueue(NodeName)
		require.NoError(t, err)
	})
}

// Pod deletes on a trunk being prepared return at once, so a drain cannot hold every shared worker.
func TestBranchENIProvider_PodDeletesDoNotWaitForTrunkPreparation(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, podAPI, _, _ := getProviderAndMocks(ctrl)
	var pods []v1.Pod
	var branches []*ec2types.NetworkInterface
	for index := range 30 {
		eniID := fmt.Sprintf("eni-%017x", 0x100+index)
		pods = append(pods, podOwningBranch(t, fmt.Sprintf("pod-%d", index), eniID, index+1, ""))
		branches = append(branches, branchTaggedWithVLAN(eniID, index+1))
	}
	restoredTrunk, helper := newRestoredTrunkForProvider(t, ctrl, pods)
	provider.trunkENICache[NodeName] = restoredTrunk
	podAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(pods, nil)
	describeStarted := make(chan struct{})
	releaseDescribe := make(chan struct{})
	helper.EXPECT().DescribeNetworkInterfaces([]string{restoredPrimaryENIID, restoredTrunkENIID}).
		DoAndReturn(func([]string) ([]ec2types.NetworkInterface, error) {
			close(describeStarted)
			<-releaseDescribe
			return restoredInterfacesForProvider(), nil
		})
	helper.EXPECT().GetBranchNetworkInterface(aws.String(restoredTrunkENIID)).Return(branches, nil)
	prepared := make(chan error, 1)
	go func() { prepared <- provider.prepareTrunkForAllocation(NodeName, restoredTrunk) }()
	awaitSignal(t, describeStarted, "the restored-trunk check to start")

	results := make(chan k8sCtrl.Result, len(pods))
	for _, pod := range pods {
		go func() {
			result, _ := provider.DeleteBranchUsedByPods(NodeName, string(pod.UID))
			results <- result
		}()
	}
	timeout := time.After(100 * time.Millisecond)
	for range pods {
		select {
		case result := <-results:
			require.Equal(t, k8sCtrl.Result{RequeueAfter: preparationRetryPeriod, Requeue: true}, result)
		case <-timeout:
			close(releaseDescribe)
			t.Fatal("a pod delete waited for the restored-trunk check")
		}
	}
	close(releaseDescribe)
	require.NoError(t, awaitSignal(t, prepared, "the restored-trunk check to finish"))

	for _, pod := range pods {
		result, err := provider.DeleteBranchUsedByPods(NodeName, string(pod.UID))
		require.NoError(t, err)
		require.Equal(t, k8sCtrl.Result{}, result)
	}
	require.Len(t, restoredTrunk.Introspect().DeleteQueue, len(pods))
}
