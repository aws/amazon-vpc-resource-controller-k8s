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
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_pod "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/pod"
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

// A node drained and deleted before its restored trunk is prepared still deletes its deleted pods' branch ENIs, also
// when EC2 Describe fails from the drain until after the node is deleted: DeleteNode then keeps the trunk until the
// delete-queue job can prepare it.
func TestBranchENIProvider_DrainedRestoredNodeDeletesQueuedBranchENIs(t *testing.T) {
	for name, describeDown := range map[string]bool{"EC2 available": false, "EC2 Describe down until after DeleteNode": true} {
		t.Run(name, func(t *testing.T) { testDrainedRestoredNode(t, describeDown) })
	}
}

func testDrainedRestoredNode(t *testing.T, describeDown bool) {
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

		instance := restoredInstanceForProvider(t)
		branchIDs := []string{restoredBranchENIID, restoredSecondBranchENIID}
		var runningPods []v1.Pod
		var branchInterfaces []*ec2types.NetworkInterface
		for index, branchID := range branchIDs {
			runningPods = append(runningPods, podOwningBranch(t, fmt.Sprintf("drained-pod-%d", index), branchID, index+1,
				"trunk-assoc-"+branchID))
			branchInterfaces = append(branchInterfaces, branchTaggedWithVLAN(branchID, index+1))
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
		ec2Down := false
		helper.EXPECT().DescribeNetworkInterfaces([]string{restoredPrimaryENIID, restoredTrunkENIID}).
			DoAndReturn(func([]string) ([]ec2types.NetworkInterface, error) {
				if ec2Down {
					return nil, MockError
				}
				return restoredInterfacesForProvider(), nil
			}).AnyTimes()
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
		ec2Down = describeDown
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
		result, err := provider.ProcessAsyncJob(worker.NewOnDemandDeleteNodeJob(NodeName))
		require.NoError(t, err)
		if describeDown {
			// The unprepared trunk still holds the queued ENIs, so DeleteNode keeps it and asks to run again.
			require.True(t, result.Requeue)
			_, cached := provider.getTrunkFromCache(NodeName)
			require.True(t, cached)
			ec2Down = false
			for range 20 {
				result, err = provider.ProcessAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))
				require.NoError(t, err)
				if !restoredTrunk.HasPendingDeletions() {
					break
				}
				time.Sleep(result.RequeueAfter)
			}
			result, err = provider.ProcessAsyncJob(worker.NewOnDemandDeleteNodeJob(NodeName))
			require.NoError(t, err)
		}
		require.False(t, result.Requeue)
		// Run the requeued cleanup job until it stops, as the worker does.
		for range 20 {
			result, err = provider.ProcessAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))
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

func restoredCheckProvider(ctrl *gomock.Controller) (
	*branchENIProvider, *mock_pod.MockPodClientAPIWrapper, *mock_k8s.MockK8sWrapper, *mock_condition.MockConditions,
) {
	provider, podAPI, _, k8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	return &provider, podAPI, k8sAPI, conditions
}

func runRestoredChecks(ctx context.Context, provider *branchENIProvider, limiter *rate.Limiter, nodes ...restoredNode) error {
	return provider.runRestoredNodeChecks(ctx, nodes, 0, time.Millisecond, limiter)
}

// expectPreparation makes the fake trunk need preparation for two classifications and prepare once.
func expectPreparation(ctrl *gomock.Controller, provider *branchENIProvider, podAPI *mock_pod.MockPodClientAPIWrapper,
	nodeName, instanceID string, prepare func() error,
) *mock_trunk.MockTrunkENI {
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[nodeName] = fakeTrunk
	fakeTrunk.EXPECT().InstanceID().Return(instanceID).Times(2)
	fakeTrunk.EXPECT().NeedsPreparation().Return(true).Times(2)
	podAPI.EXPECT().GetRunningPodsOnNode(nodeName).Return(nil, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
		listRunningPods func() ([]v1.Pod, error),
	) (ec2.EC2Instance, error) {
		_, err := listRunningPods()
		if err == nil && prepare != nil {
			err = prepare()
		}
		return nil, err
	})
	return fakeTrunk
}

func TestBranchENIProvider_StartRetriesRestoredNodeCapture(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
	startContext, cancelStart := context.WithCancel(context.Background())
	gomock.InOrder(
		mockK8sAPI.EXPECT().ListCNINodes().Return(nil, MockError),
		mockK8sAPI.EXPECT().ListCNINodes().DoAndReturn(func() ([]*rcv1alpha1.CNINode, error) {
			cancelStart()
			return nil, nil
		}),
	)

	require.NoError(t, provider.Start(startContext))
}

// A node that needs no preparation, or can never get one, leaves the pass at once without a rate-limit token.
func TestBranchENIProvider_RestoredNodeCheckDropsNodesWithoutAToken(t *testing.T) {
	instanceID := "i-restored"
	eligibleWith := func(mutate func(*v1.Node)) *v1.Node {
		node := branchEligibleNode(NodeName, instanceID)
		mutate(node)
		return node
	}
	tests := map[string]struct {
		currentNode      *v1.Node
		getNodeErr       error
		cachedInstanceID string
		cniNode          *rcv1alpha1.CNINode
		instanceType     string
	}{
		"prepared":                       {currentNode: branchEligibleNode(NodeName, instanceID), cachedInstanceID: instanceID},
		"removed":                        {getNodeErr: apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, NodeName)},
		"replaced in cache":              {currentNode: branchEligibleNode(NodeName, instanceID), cachedInstanceID: "i-replacement"},
		"replaced before initialization": {currentNode: &v1.Node{Spec: v1.NodeSpec{ProviderID: "aws:///us-west-2a/i-replacement"}}},
		"replaced by an eligible node":   {currentNode: branchEligibleNode(NodeName, "i-replacement")},
		"windows":                        {currentNode: eligibleWith(func(node *v1.Node) { node.Labels[config.NodeLabelOS] = config.OSWindows })},
		"unsupported instance type":      {currentNode: branchEligibleNode(NodeName, instanceID), instanceType: "unsupported"},
		"not selected for branch management": {
			currentNode: eligibleWith(func(node *v1.Node) { delete(node.Labels, config.HasTrunkAttachedLabel) }),
			cniNode:     &rcv1alpha1.CNINode{},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			provider, _, k8sAPI, conditions := restoredCheckProvider(ctrl)
			conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
			k8sAPI.EXPECT().GetNode(NodeName).Return(test.currentNode, test.getNodeErr)
			if test.cniNode != nil {
				k8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).Return(test.cniNode, nil)
			}
			if test.cachedInstanceID != "" {
				fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
				provider.trunkENICache[NodeName] = fakeTrunk
				fakeTrunk.EXPECT().InstanceID().Return(test.cachedInstanceID)
				if test.cachedInstanceID == instanceID {
					fakeTrunk.EXPECT().NeedsPreparation().Return(false)
				}
			}
			node := restoredNodeForTest(NodeName, instanceID)
			if test.instanceType != "" {
				node.instanceType = test.instanceType
			}
			limiter := rate.NewLimiter(rate.Every(time.Hour), 1)
			checkCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			require.NoError(t, runRestoredChecks(checkCtx, provider, limiter, node))
			require.NoError(t, checkCtx.Err(), "the node stayed in the retry loop")
			require.True(t, limiter.Allow(), "dropping a node consumed a rate-limit token")
		})
	}
}

func TestBranchENIProvider_RestoredNodeCheckWaitsForNodeInitialization(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, podAPI, k8sAPI, conditions := restoredCheckProvider(ctrl)
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	instanceID := "i-restored"
	currentNode := branchEligibleNode(NodeName, instanceID)
	delete(currentNode.Labels, config.HasTrunkAttachedLabel)
	k8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).Return(&rcv1alpha1.CNINode{
		Spec: rcv1alpha1.CNINodeSpec{Features: []rcv1alpha1.Feature{{Name: rcv1alpha1.SecurityGroupsForPods}}},
	}, nil).Times(3)
	fakeTrunk := expectPreparation(ctrl, provider, podAPI, NodeName, instanceID, nil)
	// The trunk is not cached on the first pass, so the node waits and is checked again.
	delete(provider.trunkENICache, NodeName)
	gomock.InOrder(
		k8sAPI.EXPECT().GetNode(NodeName).Return(currentNode, nil),
		k8sAPI.EXPECT().GetNode(NodeName).DoAndReturn(func(string) (*v1.Node, error) {
			require.NoError(t, provider.addTrunkToCache(NodeName, fakeTrunk))
			return currentNode, nil
		}),
		k8sAPI.EXPECT().GetNode(NodeName).Return(currentNode, nil),
	)

	require.NoError(t, runRestoredChecks(t.Context(), provider, rate.NewLimiter(rate.Inf, 1),
		restoredNodeForTest(NodeName, instanceID)))
}

func TestBranchENIProvider_RestoredNodeCheckWaitsForPodDataStoreSync(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, podAPI, k8sAPI, conditions := restoredCheckProvider(ctrl)
	podDataStoreSynced := false
	gomock.InOrder(
		conditions.EXPECT().GetPodDataStoreSyncStatus().Return(false),
		conditions.EXPECT().GetPodDataStoreSyncStatus().DoAndReturn(func() bool {
			podDataStoreSynced = true
			return true
		}),
	)
	instanceID := "i-restored"
	k8sAPI.EXPECT().GetNode(NodeName).Return(branchEligibleNode(NodeName, instanceID), nil).Times(2)
	expectPreparation(ctrl, provider, podAPI, NodeName, instanceID, func() error {
		require.True(t, podDataStoreSynced, "preparation ran before the pod datastore synced")
		return nil
	})

	require.NoError(t, runRestoredChecks(t.Context(), provider, rate.NewLimiter(rate.Every(time.Hour), 1),
		restoredNodeForTest(NodeName, instanceID)))
}

func TestBranchENIProvider_RestoredNodeCheckPacesThreePerSecond(t *testing.T) {
	require.Equal(t, 10*time.Minute, RestoredNodeCheckGracePeriod)
	require.Equal(t, 3, RestoredNodeCheckRatePerSecond)

	ctrl := gomock.NewController(t)
	provider, podAPI, k8sAPI, conditions := restoredCheckProvider(ctrl)
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	var restoredNodes []restoredNode
	var preparedAt []time.Time
	for index := range 4 {
		nodeName := fmt.Sprintf("node-%d", index)
		instanceID := fmt.Sprintf("i-%d", index)
		restoredNodes = append(restoredNodes, restoredNodeForTest(nodeName, instanceID))
		k8sAPI.EXPECT().GetNode(nodeName).Return(branchEligibleNode(nodeName, instanceID), nil).Times(2)
		expectPreparation(ctrl, provider, podAPI, nodeName, instanceID, func() error {
			preparedAt = append(preparedAt, time.Now())
			return nil
		})
	}

	require.NoError(t, runRestoredChecks(t.Context(), provider,
		rate.NewLimiter(rate.Limit(RestoredNodeCheckRatePerSecond), restoredNodeCheckBurst), restoredNodes...))
	require.Len(t, preparedAt, 4)
	require.GreaterOrEqual(t, preparedAt[3].Sub(preparedAt[0]), 900*time.Millisecond)
}

// A node replaced while the pass waits for its token is classified again and dropped, not prepared.
func TestBranchENIProvider_RestoredNodeCheckRechecksAfterToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, _, k8sAPI, conditions := restoredCheckProvider(ctrl)
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	instanceID := "i-restored"
	oldTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	replacementTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = oldTrunk
	checked := make(chan struct{})
	k8sAPI.EXPECT().GetNode(NodeName).Return(branchEligibleNode(NodeName, instanceID), nil).Times(2)
	oldTrunk.EXPECT().InstanceID().Return(instanceID)
	oldTrunk.EXPECT().NeedsPreparation().DoAndReturn(func() bool {
		close(checked)
		return true
	})
	replacementTrunk.EXPECT().InstanceID().Return("i-replacement").AnyTimes()
	limiter := rate.NewLimiter(rate.Every(200*time.Millisecond), 1)
	require.True(t, limiter.Allow())
	done := make(chan error, 1)
	go func() {
		done <- runRestoredChecks(t.Context(), provider, limiter, restoredNodeForTest(NodeName, instanceID))
	}()

	<-checked
	provider.lock.Lock()
	provider.trunkENICache[NodeName] = replacementTrunk
	provider.lock.Unlock()
	require.NoError(t, <-done)
}
