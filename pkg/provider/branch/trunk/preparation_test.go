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
	"bytes"
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
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestPreparationBackoffIsSharedCappedAndReset(t *testing.T) {
	controller := gomock.NewController(t)
	trunk, helper, _ := newRestoredTrunk(t, controller, nil)
	delays := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, MockError).Times(len(delays))

	for attempt, delay := range delays {
		trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
		err := prepareTrunk(trunk, nil)
		var deferred *PreparationDeferred
		require.ErrorAs(t, err, &deferred)
		require.ErrorIs(t, err, MockError)
		require.Equal(t, delay, deferred.After)
		if attempt > 0 {
			continue
		}
		// Callers arriving during the backoff wait it out without listing pods or calling EC2.
		const callers = 32
		results := make(chan error, callers)
		var workers sync.WaitGroup
		for range callers {
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
			require.ErrorAs(t, err, &deferred)
			require.Nil(t, deferred.Err)
			require.Positive(t, deferred.After)
		}
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

func TestFailedPreparationKeepsProvisionalStateAndBacksOff(t *testing.T) {
	for _, stage := range []string{"interface check", "pod list", "branch query"} {
		t.Run(stage, func(t *testing.T) {
			controller := gomock.NewController(t)
			pod := recoveryPod("pod-uid", recoveryBranchENIID, 17)
			trunk, helper, _ := newRestoredTrunk(t, controller, []v1.Pod{pod})
			before := trunk.Introspect()
			switch stage {
			case "interface check":
				helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(nil, MockError)
			case "branch query":
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
			require.True(t, trunk.prepareMu.TryLock(), "a failed check kept the preparation lock")
			trunk.prepareMu.Unlock()
			var deferred *PreparationDeferred
			require.ErrorAs(t, prepareTrunk(trunk, nil), &deferred)
			require.Nil(t, deferred.Err)
			// No EC2 delete is expected: cleanup waits for a successful check.
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

// An ENI queued before preparation keeps its association and the rest of its cooldown, an orphan that preparation
// finds starts a fresh one, and the queue is in cooldown order so one tick deletes every cooled ENI.
func TestPreparationKeepsCarriedCooldownsAndStartsFreshOnesForOrphans(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controller := gomock.NewController(t)
		k8sAPI := mock_k8s.NewMockK8sWrapper(controller)
		k8sAPI.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(nil, MockError)
		cooldown.InitCoolDownPeriod(k8sAPI, zap.New())
		period := cooldown.DefaultCoolDownPeriod
		branchID := func(vlanID int) string { return fmt.Sprintf("eni-%017d", vlanID) }
		carriedVLANs := []int{20, 22}
		var pods []v1.Pod
		for _, vlanID := range carriedVLANs {
			pod := recoveryPod(fmt.Sprintf("p%d", vlanID), branchID(vlanID), vlanID)
			pod.Annotations[config.ResourceNamePodENI] = fmt.Sprintf(
				`[{"eniId":%q,"vlanId":%d,"associationID":"assoc-%d"}]`, branchID(vlanID), vlanID, vlanID)
			pods = append(pods, pod)
		}
		trunk, helper, _ := newRestoredTrunk(t, controller, pods)
		// Fresh orphans sit between the carried ENIs in ENI ID and VLAN order.
		var branches []*ec2types.NetworkInterface
		for vlanID := 19; vlanID <= 24; vlanID++ {
			branches = append(branches, taggedBranch(branchID(vlanID), vlanID))
		}
		expectDelete := func(vlanID int, associated bool) {
			if associated {
				helper.EXPECT().DisassociateTrunkInterface(aws.String(fmt.Sprintf("assoc-%d", vlanID))).Return(nil)
			}
			helper.EXPECT().DeleteNetworkInterface(aws.String(branchID(vlanID))).Return(nil)
		}

		trunk.PushBranchENIsToCoolDownQueue("p20")
		time.Sleep(period / 2)
		trunk.PushBranchENIsToCoolDownQueue("p22")
		time.Sleep(period/2 + time.Second)
		helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(validRestoredInterfaces(InstanceId), nil)
		helper.EXPECT().GetBranchNetworkInterface(gomock.Any()).Return(branches, nil)
		require.NoError(t, prepareTrunk(trunk, nil))

		expectDelete(20, true)
		trunk.DeleteCooledDownENIs()
		require.Len(t, trunk.deleteQueue, len(branches)-1, "preparation re-dated a cooled ENI or queued it behind an orphan")

		time.Sleep(period / 2)
		expectDelete(22, true)
		trunk.DeleteCooledDownENIs()
		require.Len(t, trunk.deleteQueue, len(branches)-2, "preparation reset the rest of a carried cooldown")

		time.Sleep(period/2 + time.Second)
		for _, vlanID := range []int{19, 21, 23, 24} {
			expectDelete(vlanID, false)
		}
		trunk.DeleteCooledDownENIs()
		require.Empty(t, trunk.deleteQueue)
	})
}

// A checkpoint that EC2 contradicts is cold-initialized, counted and logged once, and a failed fallback retries
// cold init without validating the checkpoint again.
func TestRestoredCheckpointMismatchColdInitializes(t *testing.T) {
	for _, failAt := range []string{"", "instance reload", "trunk lookup", "branch query"} {
		name := failAt
		if name == "" {
			name = "first attempt succeeds"
		}
		t.Run(name, func(t *testing.T) {
			controller := gomock.NewController(t)
			pod := recoveryPod("running-pod", recoveryBranchENIID, 17)
			listPods := func() ([]v1.Pod, error) { return []v1.Pod{pod}, nil }
			trunk, helper, instance := newRestoredTrunk(t, controller, []v1.Pod{pod})
			var logs bytes.Buffer
			trunk.log = zap.New(zap.WriteTo(&logs))
			detachedTrunk := validRestoredInterfaces(InstanceId)
			detachedTrunk[1].Attachment.Status = ec2types.AttachmentStatusDetached
			// EC2 reports a trunk other than the checkpoint one, so the test can tell which one is kept.
			ec2TrunkID := "eni-00000000000000009"
			ec2Trunk := []ec2types.InstanceNetworkInterface{{
				InterfaceType:      aws.String("trunk"),
				NetworkInterfaceId: aws.String(ec2TrunkID),
				Attachment:         &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: aws.Int32(2)},
			}}
			helper.EXPECT().DescribeNetworkInterfaces(gomock.Any()).Return(detachedTrunk, nil)
			var calls []*gomock.Call
			expectColdInit := func(failAt string) {
				reload := helper.EXPECT().GetInstanceDetails(aws.String(InstanceId))
				calls = append(calls, reload)
				if failAt == "instance reload" {
					reload.Return(nil, MockError)
					return
				}
				reload.Return(coldInstanceDetails(), nil)
				calls = append(calls, helper.EXPECT().GetSubnet(aws.String(SubnetId)).
					Return(&ec2types.Subnet{CidrBlock: aws.String(SubnetCidrBlock)}, nil))
				lookup := helper.EXPECT().GetInstanceNetworkInterface(aws.String(InstanceId))
				calls = append(calls, lookup)
				if failAt == "trunk lookup" {
					lookup.Return(nil, MockError)
					return
				}
				lookup.Return(ec2Trunk, nil)
				calls = append(calls, helper.EXPECT().WaitForNetworkInterfaceStatusChange(
					aws.String(ec2TrunkID), string(ec2types.AttachmentStatusAttached)).Return(nil))
				branches := helper.EXPECT().GetBranchNetworkInterface(aws.String(ec2TrunkID))
				calls = append(calls, branches)
				if failAt == "branch query" {
					branches.Return(nil, MockError)
					return
				}
				branches.Return([]*ec2types.NetworkInterface{taggedBranch(recoveryBranchENIID, 17)}, nil)
			}
			if failAt != "" {
				expectColdInit(failAt)
			}
			expectColdInit("")
			gomock.InOrder(calls...)
			mismatchCount := trunkENIOperationsErrCount.WithLabelValues("restored_checkpoint_mismatch")
			before := writeTrunkMetric(t, mismatchCount)

			if failAt != "" {
				_, err := trunk.PrepareForAllocation(listPods)
				require.ErrorIs(t, err, MockError)
				require.True(t, trunk.NeedsPreparation())
				require.True(t, trunk.prepareMu.TryLock(), "a failed fallback kept the preparation lock")
				trunk.prepareMu.Unlock()
				var deferred *PreparationDeferred
				require.ErrorAs(t, prepareTrunk(trunk, nil), &deferred)
				require.Nil(t, deferred.Err)
				trunk.nextPreparationAttempt = time.Now().Add(-time.Second)
			}
			checkpointInstance, err := trunk.PrepareForAllocation(listPods)

			require.NoError(t, err)
			require.Same(t, instance, checkpointInstance, "the caller must re-persist the checkpoint after cold init")
			require.False(t, trunk.NeedsPreparation())
			require.Equal(t, ec2TrunkID, trunk.TrunkENIID())
			owned := trunk.uidToBranchENIMap["running-pod"]
			require.Len(t, owned, 1)
			require.Equal(t, recoveryBranchENIID, owned[0].ID)
			require.True(t, trunk.usedVlanIds[17])
			require.Empty(t, trunk.deleteQueue)
			require.Equal(t, before+1, writeTrunkMetric(t, mismatchCount))
			errorLines := errorLogLines(&logs)
			require.Len(t, errorLines, 1)
			require.Contains(t, errorLines[0], recoveryTrunkENIID)
			require.Contains(t, errorLines[0], errNeedsColdInit.Error())
		})
	}
}
