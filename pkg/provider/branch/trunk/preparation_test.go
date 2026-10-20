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

package trunk

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestPreparationFailureBackoffIsSharedAcrossCallers(t *testing.T) {
	controller := gomock.NewController(t)
	trunk, helper, _ := newRestoredTrunk(t, controller, nil)
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, MockError).Times(1)

	require.ErrorIs(t, prepareTrunk(trunk, nil), MockError)
	const callers = 32
	results := make(chan error, callers)
	var workers sync.WaitGroup
	for index := 0; index < callers; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) {
				t.Error("waiting caller listed pods during shared backoff")
				return nil, nil
			})
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	for err := range results {
		var deferred *PreparationDeferred
		require.ErrorAs(t, err, &deferred)
		require.Nil(t, deferred.Err)
		require.Positive(t, deferred.After)
	}
	require.True(t, trunk.NeedsPreparation())
}

func TestPreparationBackoffCapsAndResetsAfterSuccess(t *testing.T) {
	controller := gomock.NewController(t)
	trunk, helper, _ := newRestoredTrunk(t, controller, nil)
	delays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, MockError).Times(len(delays))

	for _, delay := range delays {
		trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
		err := prepareTrunk(trunk, nil)
		var deferred *PreparationDeferred
		require.ErrorAs(t, err, &deferred)
		require.ErrorIs(t, err, MockError)
		require.Equal(t, delay, deferred.After)
	}
	trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return(nil, nil)
	require.NoError(t, prepareTrunk(trunk, nil))
	require.False(t, trunk.NeedsPreparation())
	require.Zero(t, trunk.preparationBackoff)
	require.True(t, trunk.nextPreparationAttempt.IsZero())
	require.NoError(t, prepareTrunk(trunk, nil))
}

func TestBranchQueryFailureSharesBackoff(t *testing.T) {
	controller := gomock.NewController(t)
	trunk, helper, _ := newRestoredTrunk(t, controller, nil)
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return(nil, MockError)

	require.ErrorIs(t, prepareTrunk(trunk, nil), MockError)
	var deferred *PreparationDeferred
	require.ErrorAs(t, prepareTrunk(trunk, nil), &deferred)
	require.Nil(t, deferred.Err)
}

func TestPreparationColdFallbackOwnsLockAndReturnsCheckpointInstance(t *testing.T) {
	controller := gomock.NewController(t)
	trunk, helper, instance := getMockHelperInstanceAndTrunkObject(controller)
	trunk.prepared.Store(false)
	instance.EXPECT().RestoredTrunkENIID().Return(recoveryTrunkENIID)
	instance.EXPECT().PrimaryNetworkInterfaceID().Return(recoveryPrimaryENIID)
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, nil)
	instance.EXPECT().LoadDetails(helper).Return(nil)
	instance.EXPECT().InstanceID().Return(InstanceId)
	instance.EXPECT().GetCustomNetworkingSpec().Return("", nil)
	helper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).Return(nil)
	helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil)

	checkpointInstance, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) { return nil, nil })
	require.NoError(t, err)
	require.Same(t, instance, checkpointInstance)
	require.False(t, trunk.NeedsPreparation())
	require.True(t, trunk.prepareMu.TryLock())
	trunk.prepareMu.Unlock()
}

func TestPreparationColdFallbackFailureReleasesLockAndBacksOff(t *testing.T) {
	controller := gomock.NewController(t)
	trunk, helper, _ := newRestoredTrunk(t, controller, nil)
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, nil)
	helper.EXPECT().GetInstanceDetails(aws.String(InstanceId)).Return(nil, MockError)
	require.ErrorIs(t, prepareTrunk(trunk, nil), MockError)
	require.True(t, trunk.prepareMu.TryLock())
	trunk.prepareMu.Unlock()
	var deferred *PreparationDeferred
	require.ErrorAs(t, prepareTrunk(trunk, nil), &deferred)
	require.Nil(t, deferred.Err)
	require.True(t, trunk.NeedsPreparation())
}

func TestPreparationErrorsUseTheSameBackoff(t *testing.T) {
	failures := map[string]error{
		"throttling": &smithy.GenericAPIError{Code: "RequestLimitExceeded"},
		"permission": &smithy.GenericAPIError{Code: "UnauthorizedOperation"},
		"service":    &smithy.GenericAPIError{Code: "InternalError"},
		"timeout":    context.DeadlineExceeded,
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			controller := gomock.NewController(t)
			trunk, helper, _ := newRestoredTrunk(t, controller, nil)
			helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, failure)
			require.ErrorIs(t, prepareTrunk(trunk, nil), failure)
			var deferred *PreparationDeferred
			require.ErrorAs(t, prepareTrunk(trunk, nil), &deferred)
			require.Nil(t, deferred.Err)
			require.True(t, trunk.NeedsPreparation())
		})
	}
}

func TestFailedPreparationPreservesProvisionalBranchState(t *testing.T) {
	for _, stage := range []string{"pod list", "branch query"} {
		t.Run(stage, func(t *testing.T) {
			controller := gomock.NewController(t)
			pod := recoveryPod("pod-uid", recoveryBranchENIID, 17)
			trunk, helper, _ := newRestoredTrunk(t, controller, []v1.Pod{pod})
			before := trunk.Introspect()
			if stage == "branch query" {
				helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
				helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return(nil, MockError)
			}
			_, err := trunk.PrepareForAllocation(func() ([]v1.Pod, error) {
				if stage == "pod list" {
					return nil, MockError
				}
				return []v1.Pod{pod}, nil
			})
			require.ErrorIs(t, err, MockError)
			require.True(t, trunk.NeedsPreparation())
			require.Equal(t, before, trunk.Introspect())
			require.True(t, trunk.usedVlanIds[17])
			var deferred *PreparationDeferred
			require.ErrorAs(t, prepareTrunk(trunk, nil), &deferred)
			require.Nil(t, deferred.Err)
			trunk.DeleteCooledDownENIs()
		})
	}
}

const recoveryRunningBranchENIID = "eni-00000000000000006"

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

func taggedBranch(id string, vlanID int) *ec2types.NetworkInterface {
	return &ec2types.NetworkInterface{
		NetworkInterfaceId: aws.String(id),
		TagSet: []ec2types.Tag{
			{Key: aws.String(config.VLandIDTag), Value: aws.String(strconv.Itoa(vlanID))},
		},
	}
}

func TestPodDeleteDuringPreparationCleansTheRecoveredBranch(t *testing.T) {
	controller := gomock.NewController(t)
	pods := []v1.Pod{
		recoveryPod("deleted-pod", recoveryBranchENIID, 17),
		recoveryPod("running-pod", recoveryRunningBranchENIID, 18),
	}
	trunk, helper, _ := newRestoredTrunk(t, controller, pods)
	describeStarted := make(chan struct{})
	releaseDescribe := make(chan struct{})
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).
		DoAndReturn(func([]string) ([]ec2types.NetworkInterface, error) {
			close(describeStarted)
			<-releaseDescribe
			return validRestoredInterfaces(InstanceId), nil
		})
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return([]*ec2types.NetworkInterface{
		taggedBranch(recoveryBranchENIID, 17),
		taggedBranch(recoveryRunningBranchENIID, 18),
	}, nil)

	prepared := make(chan error, 1)
	go func() { prepared <- prepareTrunk(trunk, pods) }()
	awaitSignal(t, describeStarted, "the restored-trunk check to start")
	before := trunk.Introspect()
	// Neither caller waits for the check: each is told to retry and leaves branch state alone.
	retry := make(chan bool, 2)
	go func() { retry <- trunk.PushBranchENIsToCoolDownQueue("deleted-pod") }()
	go func() { retry <- trunk.Reconcile(nil) }()
	for range 2 {
		select {
		case deferred := <-retry:
			require.True(t, deferred, "a caller was not told to retry")
		case <-time.After(100 * time.Millisecond):
			close(releaseDescribe)
			t.Fatal("a pod delete or reconcile waited for the restored-trunk check")
		}
	}
	require.Equal(t, before, trunk.Introspect(), "branch state changed while the check was running")
	close(releaseDescribe)
	require.NoError(t, awaitSignal(t, prepared, "the restored-trunk check to finish"))
	// The worker retries the deferred delete, which now finds the recovered branch.
	require.False(t, trunk.PushBranchENIsToCoolDownQueue("deleted-pod"))

	require.NotContains(t, trunk.uidToBranchENIMap, "deleted-pod")
	require.Contains(t, trunk.uidToBranchENIMap, "running-pod")
	require.Len(t, trunk.deleteQueue, 1)
	require.Equal(t, recoveryBranchENIID, trunk.deleteQueue[0].ID)
	trunk.deleteQueue[0].deletionTimeStamp = time.Time{}
	helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryBranchENIID)).Return(nil)
	trunk.DeleteCooledDownENIs()
	require.Empty(t, trunk.deleteQueue)
	require.False(t, trunk.usedVlanIds[17])
	require.True(t, trunk.usedVlanIds[18])
}

func TestFailedPreparationDefersCleanupUntilTheCheckSucceeds(t *testing.T) {
	controller := gomock.NewController(t)
	deletedPod := recoveryPod("deleted-pod", recoveryBranchENIID, 17)
	runningPod := recoveryPod("running-pod", recoveryRunningBranchENIID, 18)
	trunk, helper, _ := newRestoredTrunk(t, controller, []v1.Pod{deletedPod, runningPod})
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, MockError)
	require.ErrorIs(t, prepareTrunk(trunk, []v1.Pod{deletedPod, runningPod}), MockError)

	trunk.PushBranchENIsToCoolDownQueue("deleted-pod")
	require.Len(t, trunk.deleteQueue, 1)
	trunk.deleteQueue[0].deletionTimeStamp = time.Time{}
	trunk.DeleteCooledDownENIs()
	require.Len(t, trunk.deleteQueue, 1)
	require.True(t, trunk.usedVlanIds[17], "VLAN of a branch awaiting cleanup was released before the check succeeded")

	trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
	helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return([]*ec2types.NetworkInterface{
		taggedBranch(recoveryBranchENIID, 17),
		taggedBranch(recoveryRunningBranchENIID, 18),
	}, nil)
	require.NoError(t, prepareTrunk(trunk, []v1.Pod{runningPod}))
	require.Len(t, trunk.deleteQueue, 1)
	require.Equal(t, recoveryBranchENIID, trunk.deleteQueue[0].ID)
	require.True(t, trunk.usedVlanIds[17])
	trunk.deleteQueue[0].deletionTimeStamp = time.Time{}
	helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryBranchENIID)).Return(nil)
	trunk.DeleteCooledDownENIs()
	require.Empty(t, trunk.deleteQueue)
	require.False(t, trunk.usedVlanIds[17])
	require.True(t, trunk.usedVlanIds[18])
}

// An ENI queued before preparation keeps its cooldown start and association, so preparation does not re-date it.
func TestPreparationKeepsTheCooldownOfAnAlreadyQueuedENI(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controller := gomock.NewController(t)
		k8sAPI := mock_k8s.NewMockK8sWrapper(controller)
		k8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(nil, MockError)
		cooldown.InitCoolDownPeriod(k8sAPI, zap.New())
		pod := recoveryPod("p1", recoveryBranchENIID, 17)
		pod.Annotations[config.ResourceNamePodENI] = fmt.Sprintf(
			`[{"eniId":%q,"vlanId":17,"associationID":"assoc-eni-A"}]`, recoveryBranchENIID)
		trunk, helper, _ := newRestoredTrunk(t, controller, []v1.Pod{pod})

		trunk.PushBranchENIsToCoolDownQueue("p1")
		time.Sleep(cooldown.DefaultCoolDownPeriod + time.Second)
		helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
		helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).
			Return([]*ec2types.NetworkInterface{taggedBranch(recoveryBranchENIID, 17)}, nil)
		require.NoError(t, prepareTrunk(trunk, nil))
		helper.EXPECT().DisassociateTrunkInterface(aws.String("assoc-eni-A")).Return(nil)
		helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryBranchENIID)).Return(nil)
		trunk.DeleteCooledDownENIs()

		require.Empty(t, trunk.deleteQueue, "preparation restarted the cooldown of an ENI queued before it")
		require.False(t, trunk.usedVlanIds[17])
	})
}

// Preparation returns the queue in cooldown order, so one tick deletes every cooled ENI instead of
// stopping at a fresh orphan queued ahead of one.
func TestPreparationQueuesCarriedENIsBeforeInterleavedOrphans(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controller := gomock.NewController(t)
		k8sAPI := mock_k8s.NewMockK8sWrapper(controller)
		k8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(nil, MockError)
		cooldown.InitCoolDownPeriod(k8sAPI, zap.New())
		carriedVLANs := []int{20, 22, 24}
		var pods []v1.Pod
		for _, vlanID := range carriedVLANs {
			pod := recoveryPod(fmt.Sprintf("p%d", vlanID), fmt.Sprintf("eni-%017d", vlanID), vlanID)
			pod.Annotations[config.ResourceNamePodENI] = fmt.Sprintf(
				`[{"eniId":"eni-%017d","vlanId":%d,"associationID":"assoc-%d"}]`, vlanID, vlanID, vlanID)
			pods = append(pods, pod)
		}
		trunk, helper, _ := newRestoredTrunk(t, controller, pods)
		// EC2 lists the three carried ENIs between fresh orphans, in ENI ID and VLAN order too, so only a
		// sort by cooldown start can put all three first.
		var branches []*ec2types.NetworkInterface
		for vlanID := 19; vlanID <= 26; vlanID++ {
			branches = append(branches, taggedBranch(fmt.Sprintf("eni-%017d", vlanID), vlanID))
		}

		for _, vlanID := range carriedVLANs {
			trunk.PushBranchENIsToCoolDownQueue(fmt.Sprintf("p%d", vlanID))
			time.Sleep(10 * time.Second)
		}
		time.Sleep(cooldown.DefaultCoolDownPeriod)
		helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
		helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return(branches, nil)
		require.NoError(t, prepareTrunk(trunk, nil))
		for _, vlanID := range carriedVLANs {
			helper.EXPECT().DisassociateTrunkInterface(aws.String(fmt.Sprintf("assoc-%d", vlanID))).Return(nil)
			helper.EXPECT().DeleteNetworkInterface(aws.String(fmt.Sprintf("eni-%017d", vlanID))).Return(nil)
		}

		trunk.DeleteCooledDownENIs()

		require.Len(t, trunk.deleteQueue, len(branches)-len(carriedVLANs),
			"the tick stopped at a fresh orphan queued before a cooled ENI")
	})
}

// An ENI queued shortly before preparation waits out the rest of its cooldown, not a new one, and not none.
func TestPreparationKeepsTheRestOfACarriedCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controller := gomock.NewController(t)
		k8sAPI := mock_k8s.NewMockK8sWrapper(controller)
		k8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(nil, MockError)
		cooldown.InitCoolDownPeriod(k8sAPI, zap.New())
		pod := recoveryPod("p1", recoveryBranchENIID, 17)
		pod.Annotations[config.ResourceNamePodENI] = fmt.Sprintf(
			`[{"eniId":%q,"vlanId":17,"associationID":"assoc-eni-A"}]`, recoveryBranchENIID)
		trunk, helper, _ := newRestoredTrunk(t, controller, []v1.Pod{pod})

		trunk.PushBranchENIsToCoolDownQueue("p1")
		time.Sleep(cooldown.DefaultCoolDownPeriod / 2)
		helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
		helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).
			Return([]*ec2types.NetworkInterface{taggedBranch(recoveryBranchENIID, 17)}, nil)
		require.NoError(t, prepareTrunk(trunk, nil))
		trunk.DeleteCooledDownENIs()
		require.Len(t, trunk.deleteQueue, 1, "preparation cut short the cooldown of an ENI queued before it")

		time.Sleep(cooldown.DefaultCoolDownPeriod/2 + time.Second)
		helper.EXPECT().DisassociateTrunkInterface(aws.String("assoc-eni-A")).Return(nil)
		helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryBranchENIID)).Return(nil)
		trunk.DeleteCooledDownENIs()

		require.Empty(t, trunk.deleteQueue)
	})
}

// An orphan that preparation finds starts a fresh cooldown, since its pod may have been deleted just now.
func TestPreparationStartsAFreshCooldownForANewOrphan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controller := gomock.NewController(t)
		k8sAPI := mock_k8s.NewMockK8sWrapper(controller)
		k8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(nil, MockError)
		cooldown.InitCoolDownPeriod(k8sAPI, zap.New())
		trunk, helper, _ := newRestoredTrunk(t, controller, nil)
		helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
		helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).
			Return([]*ec2types.NetworkInterface{taggedBranch(recoveryOrphanENIID, 18)}, nil)
		require.NoError(t, prepareTrunk(trunk, nil))

		trunk.DeleteCooledDownENIs()
		require.Len(t, trunk.deleteQueue, 1, "a new orphan was deleted before its cooldown")
		time.Sleep(cooldown.DefaultCoolDownPeriod + time.Second)
		helper.EXPECT().DeleteNetworkInterface(aws.String(recoveryOrphanENIID)).Return(nil)
		trunk.DeleteCooledDownENIs()
		require.Empty(t, trunk.deleteQueue)
	})
}

func TestColdFallbackRetryRecoversAfterFailure(t *testing.T) {
	listNoPods := func() ([]v1.Pod, error) { return nil, nil }
	t.Run("instance reload fails", func(t *testing.T) {
		controller := gomock.NewController(t)
		trunk, helper, instance := getMockHelperInstanceAndTrunkObject(controller)
		trunk.prepared.Store(false)
		gomock.InOrder(
			instance.EXPECT().RestoredTrunkENIID().Return(recoveryTrunkENIID),
			instance.EXPECT().PrimaryNetworkInterfaceID().Return(recoveryPrimaryENIID),
			helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, nil),
			instance.EXPECT().LoadDetails(helper).Return(MockError),
		)
		_, err := trunk.PrepareForAllocation(listNoPods)
		require.ErrorIs(t, err, MockError)
		require.True(t, trunk.NeedsPreparation())

		trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
		// The rejection is remembered, so the retry does not validate the checkpoint again.
		gomock.InOrder(
			instance.EXPECT().RestoredTrunkENIID().Return(recoveryTrunkENIID),
			instance.EXPECT().LoadDetails(helper).Return(nil),
			instance.EXPECT().InstanceID().Return(InstanceId),
			helper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil),
			helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).Return(nil),
			instance.EXPECT().GetCustomNetworkingSpec().Return("", nil),
			helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil),
		)
		checkpointInstance, err := trunk.PrepareForAllocation(listNoPods)
		require.NoError(t, err)
		require.Same(t, instance, checkpointInstance)
		require.False(t, trunk.NeedsPreparation())
		require.Equal(t, trunkId, trunk.TrunkENIID())
	})
	t.Run("cold initialization fails after reload", func(t *testing.T) {
		controller := gomock.NewController(t)
		trunk, helper, instance := getMockHelperInstanceAndTrunkObject(controller)
		trunk.prepared.Store(false)
		gomock.InOrder(
			instance.EXPECT().RestoredTrunkENIID().Return(recoveryTrunkENIID),
			instance.EXPECT().PrimaryNetworkInterfaceID().Return(recoveryPrimaryENIID),
			helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, nil),
			instance.EXPECT().LoadDetails(helper).Return(nil),
			instance.EXPECT().InstanceID().Return(InstanceId),
			helper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(nil, MockError),
		)
		_, err := trunk.PrepareForAllocation(listNoPods)
		require.ErrorIs(t, err, MockError)
		require.True(t, trunk.NeedsPreparation())

		trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
		// LoadDetails cleared the restored trunk identity, so the retry goes straight to cold init.
		gomock.InOrder(
			instance.EXPECT().RestoredTrunkENIID().Return(""),
			instance.EXPECT().LoadDetails(helper).Return(nil),
			instance.EXPECT().InstanceID().Return(InstanceId),
			helper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil),
			helper.EXPECT().WaitForNetworkInterfaceStatusChange(&trunkId, string(ec2types.AttachmentStatusAttached)).Return(nil),
			instance.EXPECT().GetCustomNetworkingSpec().Return("", nil),
			helper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(nil, nil),
		)
		checkpointInstance, err := trunk.PrepareForAllocation(listNoPods)
		require.NoError(t, err)
		require.Same(t, instance, checkpointInstance)
		require.False(t, trunk.NeedsPreparation())
		require.Equal(t, trunkId, trunk.TrunkENIID())
	})
}

func TestColdFallbackRetryKeepsRunningPodBranches(t *testing.T) {
	controller := gomock.NewController(t)
	pod := recoveryPod("running-pod", recoveryBranchENIID, 17)
	listPods := func() ([]v1.Pod, error) { return []v1.Pod{pod}, nil }
	trunk, helper, instance := newRestoredTrunk(t, controller, []v1.Pod{pod})
	detachedTrunk := validRestoredInterfaces(InstanceId)
	detachedTrunk[1].Attachment.Status = ec2types.AttachmentStatusDetached
	// EC2 reports a trunk other than the checkpoint one, so the test can tell which one is kept.
	ec2TrunkID := "eni-00000000000000009"
	ec2Trunk := []ec2types.InstanceNetworkInterface{{
		InterfaceType:      aws.String("trunk"),
		NetworkInterfaceId: aws.String(ec2TrunkID),
		Attachment:         &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: aws.Int32(2)},
	}}
	coldInstance := coldInstanceDetails()
	// Only the first attempt validates: the instance reload clears the restored trunk identity.
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(detachedTrunk, nil)
	helper.EXPECT().GetInstanceDetails(aws.String(InstanceId)).Return(coldInstance, nil).Times(2)
	helper.EXPECT().GetSubnet(aws.String(SubnetId)).
		Return(&ec2types.Subnet{CidrBlock: aws.String(SubnetCidrBlock)}, nil).Times(2)
	helper.EXPECT().GetInstanceNetworkInterface(aws.String(InstanceId)).Return(ec2Trunk, nil).Times(2)
	helper.EXPECT().WaitForNetworkInterfaceStatusChange(aws.String(ec2TrunkID), string(ec2types.AttachmentStatusAttached)).
		Return(nil).Times(2)
	gomock.InOrder(
		helper.EXPECT().GetBranchNetworkInterface(aws.String(ec2TrunkID)).Return(nil, MockError),
		helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).
			Return([]*ec2types.NetworkInterface{taggedBranch(recoveryBranchENIID, 17)}, nil),
	)

	_, err := trunk.PrepareForAllocation(listPods)
	require.ErrorIs(t, err, MockError)
	trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
	checkpointInstance, err := trunk.PrepareForAllocation(listPods)

	require.NoError(t, err)
	require.Equal(t, ec2TrunkID, trunk.TrunkENIID())
	owned := trunk.uidToBranchENIMap["running-pod"]
	require.Len(t, owned, 1)
	require.Equal(t, recoveryBranchENIID, owned[0].ID)
	require.True(t, trunk.usedVlanIds[17])
	require.Empty(t, trunk.deleteQueue)
	require.Same(t, instance, checkpointInstance)
	require.False(t, trunk.NeedsPreparation())
}
