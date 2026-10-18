package trunk

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
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
