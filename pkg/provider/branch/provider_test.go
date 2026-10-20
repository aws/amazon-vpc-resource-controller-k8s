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
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_pod "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/pod"
	mock_trunk "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/trunk"
	mock_utils "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/utils"
	mock_worker "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	k8sCtrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	NodeName = "test-node"

	MockPodName1      = "pod_name"
	MockPodNamespace1 = "pod_namespace"
	PodUID1           = "uid-1"
	MockPodUID1       = types.UID("uid-1")

	MockPod1 = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			UID:         MockPodUID1,
			Name:        MockPodName1,
			Namespace:   MockPodNamespace1,
			Annotations: make(map[string]string),
		},
		Spec:   v1.PodSpec{NodeName: NodeName},
		Status: v1.PodStatus{},
	}

	SecurityGroups = []string{"sg-1", "sg-2"}

	EniDetails = []*trunk.ENIDetails{{ID: "test-id"}}

	MockError = fmt.Errorf("mock error")
	ctx       = context.TODO()
)

// getProviderAndMockK8sWrapperAndHelper returns the mock provider along with the k8s wrapper and helper
func getProviderAndMocks(ctrl *gomock.Controller) (branchENIProvider, *mock_pod.MockPodClientAPIWrapper,
	*mock_utils.MockSecurityGroupForPodsAPI, *mock_k8s.MockK8sWrapper) {
	log := zap.New(zap.UseDevMode(true)).WithName("branch provider")
	mockPodAPI := mock_pod.NewMockPodClientAPIWrapper(ctrl)
	mockSGPAPI := mock_utils.NewMockSecurityGroupForPodsAPI(ctrl)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)

	return branchENIProvider{
		apiWrapper: api.Wrapper{
			PodAPI: mockPodAPI,
			SGPAPI: mockSGPAPI,
			K8sAPI: mockK8sAPI,
		},
		log:           log,
		trunkENICache: make(map[string]trunk.TrunkENI),
		ctx:           ctx,
	}, mockPodAPI, mockSGPAPI, mockK8sAPI
}

// getProviderAndMockK8sWrapper returns the mock provider along with the k8s wrapper
func getProviderAndMockK8sWrapper(ctrl *gomock.Controller) (branchENIProvider, *mock_k8s.MockK8sWrapper) {
	log := zap.New(zap.UseDevMode(true)).WithName("branch provider")
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)

	return branchENIProvider{
		apiWrapper: api.Wrapper{
			K8sAPI: mockK8sWrapper,
		},
		log:           log,
		trunkENICache: make(map[string]trunk.TrunkENI),
	}, mockK8sWrapper
}

func getProviderWithMockWorker(ctrl *gomock.Controller) (branchENIProvider, *mock_worker.MockWorker) {
	mockWorker := mock_worker.NewMockWorker(ctrl)
	return branchENIProvider{
		log:        zap.New(zap.UseDevMode(true)).WithName("branch provider"),
		workerPool: mockWorker,
	}, mockWorker
}

func getProvider() branchENIProvider {
	log := zap.New(zap.UseDevMode(true)).WithName("branch provider")
	return branchENIProvider{
		log:           log,
		trunkENICache: make(map[string]trunk.TrunkENI),
	}
}

func writePrometheusMetric(t *testing.T, metric prometheus.Metric) *dto.Metric {
	t.Helper()
	value := &dto.Metric{}
	assert.NoError(t, metric.Write(value))
	return value
}

func prepareWithPods(
	t *testing.T,
	wantPods []v1.Pod,
	result error,
) func(func() ([]v1.Pod, error)) (ec2.EC2Instance, error) {
	t.Helper()
	return func(listRunningPods func() ([]v1.Pod, error)) (ec2.EC2Instance, error) {
		pods, err := listRunningPods()
		assert.NoError(t, err)
		assert.Equal(t, wantPods, pods)
		return nil, result
	}
}

func branchEligibleNode(nodeName, instanceID string) *v1.Node {
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				config.NodeLabelOS:           config.OSLinux,
				v1.LabelInstanceTypeStable:   "m5.large",
				config.HasTrunkAttachedLabel: config.BooleanTrue,
			},
		},
		Spec: v1.NodeSpec{ProviderID: "aws:///us-west-2a/" + instanceID},
	}
}

func restoredNodeForTest(nodeName, instanceID string) restoredNode {
	return restoredNode{
		name:         nodeName,
		instanceID:   instanceID,
		instanceType: "m5.large",
	}
}

func prepareInitResourceSuccess(
	ctrl *gomock.Controller,
	checkpointErr error,
) (*branchENIProvider, *mock_ec2.MockEC2Instance, rcv1alpha1.NodeNetworkState, string) {
	instanceID := "i-00000000000000000"
	subnetID := "subnet-00000000000000000"
	trunkENIID := "eni-00000000000000000"
	securityGroups := []string{"sg-00000000000000000"}
	freeIndex := int32(2)
	state := rcv1alpha1.NodeNetworkState{
		InstanceID:   instanceID,
		InstanceType: "m5.large",
		SubnetID:     subnetID,
	}

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockEC2API := mock_api.NewMockEC2APIHelper(ctrl)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockPodAPI := mock_pod.NewMockPodClientAPIWrapper(ctrl)
	mockWorker := mock_worker.NewMockWorker(ctrl)
	provider := branchENIProvider{
		apiWrapper: api.Wrapper{
			EC2API: mockEC2API,
			K8sAPI: mockK8sAPI,
			PodAPI: mockPodAPI,
		},
		log:           zap.New(zap.UseDevMode(true)).WithName("branch provider"),
		workerPool:    mockWorker,
		trunkENICache: make(map[string]trunk.TrunkENI),
	}

	mockInstance.EXPECT().Name().Return(NodeName)
	mockInstance.EXPECT().RestoredTrunkENIID().Return("").AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(instanceID).AnyTimes()
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	mockEC2API.EXPECT().GetInstanceNetworkInterface(&instanceID).
		Return([]ec2types.InstanceNetworkInterface{}, nil)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(freeIndex, nil)
	mockInstance.EXPECT().SubnetID().Return(subnetID)
	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(securityGroups)
	mockEC2API.EXPECT().CreateAndAttachNetworkInterface(
		&instanceID,
		&subnetID,
		securityGroups,
		gomock.Any(),
		&freeIndex,
		gomock.Any(),
		gomock.Any(),
		nil,
		nil,
	).Return(&ec2types.NetworkInterface{NetworkInterfaceId: aws.String(trunkENIID)}, nil)
	mockInstance.EXPECT().BuildNodeNetworkState().Return(state)
	mockK8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, state, trunkENIID).Return(checkpointErr)
	mockWorker.EXPECT().SubmitJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
	mockK8sAPI.EXPECT().GetNode(NodeName).Return(node, nil)
	mockK8sAPI.EXPECT().BroadcastEvent(
		gomock.Any(),
		utils.NodeTrunkInitiatedReason,
		"The node has trunk interface initialized successfully",
		v1.EventTypeNormal,
	)

	return &provider, mockInstance, state, trunkENIID
}

func TestBranchENIProvider_InitResourcePatchesCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, mockInstance, _, _ := prepareInitResourceSuccess(ctrl, nil)

	assert.NoError(t, provider.InitResource(mockInstance))
}

func TestBranchENIProvider_InitResourceCheckpointFailureIsBestEffort(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, mockInstance, _, _ := prepareInitResourceSuccess(ctrl, MockError)
	assert.Contains(
		t,
		cniNodeCheckpointPersistErrCount.Desc().String(),
		`fqName: "cninode_checkpoint_persist_error_total"`,
	)
	before := checkpointPersistErrorCount(t)

	assert.NoError(t, provider.InitResource(mockInstance))
	assert.Equal(t, before+1, checkpointPersistErrorCount(t))
}

func TestBranchENIProvider_InitResourceRestoredCheckpointPersistsCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instanceID := "i-00000000000000000"
	subnetID := "subnet-00000000000000000"
	trunkENIID := "eni-00000000000000000"
	state := rcv1alpha1.NodeNetworkState{
		InstanceID:   instanceID,
		InstanceType: "m5.large",
		SubnetID:     subnetID,
	}
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockEC2API := mock_api.NewMockEC2APIHelper(ctrl)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockPodAPI := mock_pod.NewMockPodClientAPIWrapper(ctrl)
	mockWorker := mock_worker.NewMockWorker(ctrl)
	provider := branchENIProvider{
		apiWrapper: api.Wrapper{
			EC2API: mockEC2API,
			K8sAPI: mockK8sAPI,
			PodAPI: mockPodAPI,
		},
		log:           zap.New(zap.UseDevMode(true)).WithName("branch provider"),
		workerPool:    mockWorker,
		trunkENICache: make(map[string]trunk.TrunkENI),
	}

	mockInstance.EXPECT().Name().Return(NodeName)
	mockInstance.EXPECT().RestoredTrunkENIID().Return(trunkENIID).AnyTimes()
	mockInstance.EXPECT().InstanceID().Return(instanceID).AnyTimes()
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	mockInstance.EXPECT().BuildNodeNetworkState().Return(state)
	mockK8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, state, trunkENIID).Return(nil)
	mockWorker.EXPECT().SubmitJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))
	node := &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
	mockK8sAPI.EXPECT().GetNode(NodeName).Return(node, nil)
	mockK8sAPI.EXPECT().BroadcastEvent(
		gomock.Any(),
		utils.NodeTrunkInitiatedReason,
		"The node has trunk interface initialized successfully",
		v1.EventTypeNormal,
	)

	assert.NoError(t, provider.InitResource(mockInstance))
}

func TestBranchENIProvider_RestoredTrunkFallbackRewritesCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	mockEC2API := mock_api.NewMockEC2APIHelper(ctrl)
	provider.apiWrapper.EC2API = mockEC2API
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	state := rcv1alpha1.NodeNetworkState{InstanceID: "i-current"}
	pods := []v1.Pod{*MockPod1}
	trunkENIID := "eni-current-trunk"

	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(pods, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
		DoAndReturn(func(listPods func() ([]v1.Pod, error)) (ec2.EC2Instance, error) {
			observed, err := listPods()
			assert.NoError(t, err)
			assert.Equal(t, pods, observed)
			return mockInstance, nil
		})
	gomock.InOrder(
		mockInstance.EXPECT().BuildNodeNetworkState().Return(state),
		fakeTrunk.EXPECT().TrunkENIID().Return(trunkENIID),
		mockK8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, state, trunkENIID).Return(nil),
	)

	assert.NoError(t, provider.prepareTrunkForAllocation(NodeName, fakeTrunk))
}

func TestBranchENIProvider_PrepareFallbackCheckpointWriteFailureDoesNotBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	mockEC2API := mock_api.NewMockEC2APIHelper(ctrl)
	provider.apiWrapper.EC2API = mockEC2API
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	state := rcv1alpha1.NodeNetworkState{InstanceID: "i-current"}
	pods := []v1.Pod{*MockPod1}
	trunkENIID := "eni-current-trunk"
	persistErrorsBefore := checkpointPersistErrorCount(t)

	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(pods, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
		DoAndReturn(func(listPods func() ([]v1.Pod, error)) (ec2.EC2Instance, error) {
			observed, err := listPods()
			assert.NoError(t, err)
			assert.Equal(t, pods, observed)
			return mockInstance, nil
		})
	gomock.InOrder(
		mockInstance.EXPECT().BuildNodeNetworkState().Return(state),
		fakeTrunk.EXPECT().TrunkENIID().Return(trunkENIID),
		mockK8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, state, trunkENIID).Return(MockError),
	)

	assert.NoError(t, provider.prepareTrunkForAllocation(NodeName, fakeTrunk))
	assert.Equal(t, persistErrorsBefore+1, checkpointPersistErrorCount(t))
}

func checkpointPersistErrorCount(t *testing.T) float64 {
	t.Helper()
	return writePrometheusMetric(t, cniNodeCheckpointPersistErrCount).GetCounter().GetValue()
}

// TestBranchENIProvider_getTrunkFromCache tests Trunk ENI is returned when the trunk is present in the cache
func TestBranchENIProvider_getTrunkFromCache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	trunkENI, present := provider.getTrunkFromCache(NodeName)
	assert.True(t, present)
	assert.Equal(t, fakeTrunk, trunkENI)
}

// TestBranchENIProvider_getTrunkFromCache_NotExist tests that false is returned when Trunk ENI doesn't exists in the cache
func TestBranchENIProvider_getTrunkFromCache_NotExist(t *testing.T) {
	provider := getProvider()

	trunkENI, present := provider.getTrunkFromCache(NodeName)
	assert.False(t, present)
	assert.Nil(t, trunkENI)
}

// De-initializing a node clears its checkpoint, so a node that becomes managed again cold-inits.
func TestBranchENIProvider_DeleteNodeClearsTheCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
	provider.trunkENICache[NodeName] = mock_trunk.NewMockTrunkENI(ctrl)
	mockK8sAPI.EXPECT().ClearCNINodeCheckpoint(NodeName).Return(nil)

	result, err := provider.DeleteNode(NodeName)

	assert.NoError(t, err)
	assert.Equal(t, k8sCtrl.Result{}, result)
	assert.NotContains(t, provider.trunkENICache, NodeName)
}

// A CNINode deleted with its node, after the clear's cache check, is no failure.
func TestBranchENIProvider_DeleteNodeIgnoresAMissingCNINode(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
	provider.trunkENICache[NodeName] = mock_trunk.NewMockTrunkENI(ctrl)
	mockK8sAPI.EXPECT().ClearCNINodeCheckpoint(NodeName).
		Return(apierrors.NewNotFound(schema.GroupResource{Resource: "cninodes"}, NodeName))
	failuresBefore := checkpointPersistErrorCount(t)

	_, err := provider.DeleteNode(NodeName)

	assert.NoError(t, err)
	assert.Equal(t, failuresBefore, checkpointPersistErrorCount(t))
}

// A failed clear leaves a stale checkpoint, so it is counted, yet DeleteNode still succeeds: a retry
// would only fail on the trunk it has already dropped.
func TestBranchENIProvider_DeleteNodeCountsAFailedClear(t *testing.T) {
	ctrl := gomock.NewController(t)
	provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
	provider.trunkENICache[NodeName] = mock_trunk.NewMockTrunkENI(ctrl)
	mockK8sAPI.EXPECT().ClearCNINodeCheckpoint(NodeName).
		Return(apierrors.NewServiceUnavailable("apiserver unavailable"))
	failuresBefore := checkpointPersistErrorCount(t)

	result, err := provider.DeleteNode(NodeName)

	assert.NoError(t, err)
	assert.Equal(t, k8sCtrl.Result{}, result)
	assert.NotContains(t, provider.trunkENICache, NodeName)
	assert.Equal(t, failuresBefore+1, checkpointPersistErrorCount(t))
}

// TestBranchENIProvider_removeTrunkFromCache tests that once trunk ENI is removed from cache it's actually removed from
// memory
func TestBranchENIProvider_removeTrunkFromCache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()

	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk
	provider.removeTrunkFromCache(NodeName)

	_, ok := provider.trunkENICache[NodeName]
	assert.False(t, ok)
}

// TestBranchENIProvider_removeTrunkFromCache_NotExists tests delete doesn't panic if entry doesn't exist in cache
func TestBranchENIProvider_removeTrunkFromCache_NotExists(t *testing.T) {
	provider := getProvider()

	// Should not throw an error
	provider.removeTrunkFromCache(NodeName)
}

// TestBranchENIProvider_addTrunkToCache tests entry is added to cache
func TestBranchENIProvider_addTrunkToCache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()

	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	err := provider.addTrunkToCache(NodeName, fakeTrunk)

	assert.NoError(t, err)
	trunkENI, ok := provider.trunkENICache[NodeName]

	assert.True(t, ok)
	assert.Equal(t, fakeTrunk, trunkENI)
}

// TestBranchENIProvider_addTrunkToCache_AlreadyExist tests error is thrown if adding an entry that already exists
// in the memory
func TestBranchENIProvider_addTrunkToCache_AlreadyExist(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()

	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	err := provider.addTrunkToCache(NodeName, fakeTrunk)

	assert.ErrorIs(t, err, ErrTrunkExistInCache)
}

// TestBranchENIProvider_DeleteBranchUsedByPods tests that ENIs used by pods are pushed to the Cool down Queue by the
// respective trunk with the associated branch ENI
func TestBranchENIProvider_DeleteBranchUsedByPods(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()

	fakeTrunk1 := mock_trunk.NewMockTrunkENI(ctrl)
	fakeTrunk2 := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk1
	provider.trunkENICache[NodeName+"2"] = fakeTrunk2

	fakeTrunk1.EXPECT().PushBranchENIsToCoolDownQueue(PodUID1)

	_, err := provider.DeleteBranchUsedByPods(NodeName, PodUID1)

	assert.NoError(t, err)
}

// TestBranchENIProvider_DeleteBranchUsedByPods_PodNotFound tests that error is returned if no trunk eni can process
// delete pod event
func TestBranchENIProvider_DeleteBranchUsedByPods_PodNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()

	fakeTrunk1 := mock_trunk.NewMockTrunkENI(ctrl)
	fakeTrunk2 := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk1
	provider.trunkENICache[NodeName+"2"] = fakeTrunk2

	fakeTrunk1.EXPECT().PushBranchENIsToCoolDownQueue(PodUID1)

	_, err := provider.DeleteBranchUsedByPods(NodeName, PodUID1)

	assert.Nil(t, err)
}

// A pod delete that a restored-trunk preparation defers is retried, never dropped.
func TestBranchENIProvider_DeleteBranchUsedByPodsRequeuesADeferredDelete(t *testing.T) {
	for _, deferred := range []bool{true, false} {
		t.Run(fmt.Sprintf("deferred %v", deferred), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			provider := getProvider()
			fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
			provider.trunkENICache[NodeName] = fakeTrunk
			fakeTrunk.EXPECT().PushBranchENIsToCoolDownQueue(PodUID1).Return(deferred)
			want := k8sCtrl.Result{}
			if deferred {
				want = k8sCtrl.Result{RequeueAfter: preparationRetryPeriod, Requeue: true}
			}

			result, err := provider.DeleteBranchUsedByPods(NodeName, PodUID1)

			assert.NoError(t, err)
			assert.Equal(t, want, result)
		})
	}
}

// TestBranchENIProvider_DeInitResources verifies that resources is removed from cache after calling de init workflow
func TestBranchENIProvider_DeInitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockWorker := getProviderWithMockWorker(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	mockInstance.EXPECT().Name().Return(NodeName)
	mockWorker.EXPECT().SubmitJobAfter(worker.NewOnDemandDeleteNodeJob(NodeName), NodeDeleteRequeueRequestDelay)

	err := provider.DeInitResource(mockInstance)

	assert.NoError(t, err)
}

func TestBranchENIProvider_RestoredNodeCheckWaitsForGrace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	instanceID := "i-restored"
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk
	restoredNodes := []restoredNode{restoredNodeForTest(NodeName, instanceID)}

	mockK8sAPI.EXPECT().GetNode(NodeName).
		Return(branchEligibleNode(NodeName, instanceID), nil).
		Times(2)
	fakeTrunk.EXPECT().InstanceID().Return(instanceID).Times(2)
	fakeTrunk.EXPECT().NeedsPreparation().Return(true).Times(2)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	prepared := make(chan struct{})
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
		listRunningPods func() ([]v1.Pod, error),
	) (ec2.EC2Instance, error) {
		_, err := listRunningPods()
		close(prepared)
		return nil, err
	})

	done := make(chan error, 1)
	go func() {
		done <- provider.runRestoredNodeChecks(
			context.Background(),
			restoredNodes,
			25*time.Millisecond,
			time.Millisecond,
			rate.NewLimiter(rate.Inf, 1),
		)
	}()

	select {
	case <-prepared:
		t.Fatal("restored node was checked before the grace period")
	case <-time.After(10 * time.Millisecond):
	}
	select {
	case <-prepared:
	case <-time.After(time.Second):
		t.Fatal("restored node was not checked after the grace period")
	}
	assert.NoError(t, <-done)
}

func TestBranchENIProvider_StartRetriesRestoredNodeCapture(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
	startContext, cancelStart := context.WithCancel(context.Background())
	gomock.InOrder(
		mockK8sAPI.EXPECT().ListCNINodes().Return(nil, MockError),
		mockK8sAPI.EXPECT().ListCNINodes().DoAndReturn(func() ([]*rcv1alpha1.CNINode, error) {
			cancelStart()
			return nil, nil
		}),
	)

	assert.NoError(t, provider.Start(startContext))
}

func TestBranchENIProvider_RestoredNodeCheckSkipsWithoutToken(t *testing.T) {
	tests := map[string]struct {
		cachedInstanceID string
		currentNode      *v1.Node
		getNodeError     error
	}{
		"verified": {
			cachedInstanceID: "i-restored",
		},
		"removed": {
			getNodeError: apierrors.NewNotFound(
				schema.GroupResource{Resource: "nodes"},
				NodeName,
			),
		},
		"replaced in cache": {
			cachedInstanceID: "i-replacement",
		},
		"replaced before initialization": {
			currentNode: &v1.Node{
				Spec: v1.NodeSpec{ProviderID: "aws:///us-west-2a/i-replacement"},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
			conditions := mock_condition.NewMockConditions(ctrl)
			provider.conditions = conditions
			conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
			instanceID := "i-restored"
			currentNode := test.currentNode
			if currentNode == nil && test.getNodeError == nil {
				currentNode = branchEligibleNode(NodeName, instanceID)
			}
			mockK8sAPI.EXPECT().GetNode(NodeName).Return(currentNode, test.getNodeError)
			if test.cachedInstanceID != "" {
				fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
				provider.trunkENICache[NodeName] = fakeTrunk
				fakeTrunk.EXPECT().InstanceID().Return(test.cachedInstanceID)
				if test.cachedInstanceID == instanceID {
					fakeTrunk.EXPECT().NeedsPreparation().Return(false)
				}
			}
			limiter := rate.NewLimiter(rate.Every(time.Hour), 1)

			assert.NoError(t, provider.runRestoredNodeChecks(
				context.Background(),
				[]restoredNode{restoredNodeForTest(NodeName, instanceID)},
				0,
				time.Millisecond,
				limiter,
			))
			assert.True(t, limiter.Allow(), "skip consumed a rate-limit token")
		})
	}
}

func TestBranchENIProvider_RestoredNodeCheckWaitsForNodeInitialization(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	instanceID := "i-restored"
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	currentNode := branchEligibleNode(NodeName, instanceID)
	delete(currentNode.Labels, config.HasTrunkAttachedLabel)
	currentCNINode := &rcv1alpha1.CNINode{
		Spec: rcv1alpha1.CNINodeSpec{
			Features: []rcv1alpha1.Feature{{Name: rcv1alpha1.SecurityGroupsForPods}},
		},
	}
	gomock.InOrder(
		mockK8sAPI.EXPECT().GetNode(NodeName).Return(currentNode, nil),
		mockK8sAPI.EXPECT().GetNode(NodeName).DoAndReturn(func(string) (*v1.Node, error) {
			assert.NoError(t, provider.addTrunkToCache(NodeName, fakeTrunk))
			return currentNode, nil
		}),
		mockK8sAPI.EXPECT().GetNode(NodeName).Return(currentNode, nil),
	)
	mockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).
		Return(currentCNINode, nil).
		Times(3)
	fakeTrunk.EXPECT().InstanceID().Return(instanceID).Times(2)
	fakeTrunk.EXPECT().NeedsPreparation().Return(true).Times(2)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
		listRunningPods func() ([]v1.Pod, error),
	) (ec2.EC2Instance, error) {
		_, err := listRunningPods()
		return nil, err
	})

	assert.NoError(t, provider.runRestoredNodeChecks(
		context.Background(),
		[]restoredNode{restoredNodeForTest(NodeName, instanceID)},
		0,
		time.Millisecond,
		rate.NewLimiter(rate.Inf, 1),
	))
}

func TestBranchENIProvider_RestoredNodeCheckSkipsStaleCacheEntryAfterReplacement(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	oldInstanceID := "i-restored"
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk
	mockK8sAPI.EXPECT().GetNode(NodeName).
		Return(branchEligibleNode(NodeName, "i-replacement"), nil)
	limiter := rate.NewLimiter(rate.Every(time.Hour), 1)

	assert.NoError(t, provider.runRestoredNodeChecks(
		context.Background(),
		[]restoredNode{restoredNodeForTest(NodeName, oldInstanceID)},
		0,
		time.Millisecond,
		limiter,
	))
	assert.True(t, limiter.Allow(), "replaced node consumed a rate-limit token")
}

func TestBranchENIProvider_RestoredNodeCheckStopsForIneligibleNode(t *testing.T) {
	instanceID := "i-restored"
	tests := map[string]struct {
		currentNode    *v1.Node
		currentCNINode *rcv1alpha1.CNINode
		restoredNode   restoredNode
	}{
		"windows": {
			currentNode: func() *v1.Node {
				node := branchEligibleNode(NodeName, instanceID)
				node.Labels[config.NodeLabelOS] = config.OSWindows
				return node
			}(),
			restoredNode: restoredNodeForTest(NodeName, instanceID),
		},
		"unsupported instance type": {
			currentNode: branchEligibleNode(NodeName, instanceID),
			restoredNode: func() restoredNode {
				node := restoredNodeForTest(NodeName, instanceID)
				node.instanceType = "unsupported"
				return node
			}(),
		},
		"not selected for branch management": {
			currentNode: func() *v1.Node {
				node := branchEligibleNode(NodeName, instanceID)
				delete(node.Labels, config.HasTrunkAttachedLabel)
				return node
			}(),
			currentCNINode: &rcv1alpha1.CNINode{},
			restoredNode:   restoredNodeForTest(NodeName, instanceID),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
			conditions := mock_condition.NewMockConditions(ctrl)
			provider.conditions = conditions
			conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
			mockK8sAPI.EXPECT().GetNode(NodeName).Return(test.currentNode, nil).AnyTimes()
			if test.currentCNINode != nil {
				mockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).
					Return(test.currentCNINode, nil).
					AnyTimes()
			}
			checkContext, cancelCheck := context.WithCancel(context.Background())
			defer cancelCheck()
			limiter := rate.NewLimiter(rate.Every(time.Hour), 1)
			done := make(chan error, 1)

			go func() {
				done <- provider.runRestoredNodeChecks(
					checkContext,
					[]restoredNode{test.restoredNode},
					0,
					5*time.Millisecond,
					limiter,
				)
			}()

			select {
			case err := <-done:
				assert.NoError(t, err)
			case <-time.After(50 * time.Millisecond):
				cancelCheck()
				<-done
				t.Fatal("ineligible node remained in the idle retry loop")
			}
			assert.True(t, limiter.Allow(), "ineligible node consumed a rate-limit token")
		})
	}
}

func TestBranchENIProvider_RestoredNodeCheckWaitsForPodDataStoreSync(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	podDataStoreSynced := false
	gomock.InOrder(
		conditions.EXPECT().GetPodDataStoreSyncStatus().Return(false),
		conditions.EXPECT().GetPodDataStoreSyncStatus().DoAndReturn(func() bool {
			podDataStoreSynced = true
			return true
		}),
	)
	instanceID := "i-restored"
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk
	mockK8sAPI.EXPECT().GetNode(NodeName).
		Return(branchEligibleNode(NodeName, instanceID), nil).
		Times(2)
	fakeTrunk.EXPECT().InstanceID().Return(instanceID).Times(2)
	fakeTrunk.EXPECT().NeedsPreparation().Return(true).Times(2)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
		listRunningPods func() ([]v1.Pod, error),
	) (ec2.EC2Instance, error) {
		assert.True(t, podDataStoreSynced, "preparation ran before the pod datastore synced")
		_, err := listRunningPods()
		return nil, err
	})
	limiter := rate.NewLimiter(rate.Every(time.Hour), 1)

	assert.NoError(t, provider.runRestoredNodeChecks(
		context.Background(),
		[]restoredNode{restoredNodeForTest(NodeName, instanceID)},
		0,
		time.Millisecond,
		limiter,
	))
}

func TestBranchENIProvider_RestoredNodeCheckRetriesFailedPreparation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	instanceID := "i-restored"
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk
	mockK8sAPI.EXPECT().GetNode(NodeName).
		Return(branchEligibleNode(NodeName, instanceID), nil).
		Times(4)
	fakeTrunk.EXPECT().InstanceID().Return(instanceID).Times(4)
	fakeTrunk.EXPECT().NeedsPreparation().Return(true).Times(4)
	gomock.InOrder(
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
			Return(nil, fmt.Errorf("transient preparation failure")),
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
			listRunningPods func() ([]v1.Pod, error),
		) (ec2.EC2Instance, error) {
			_, err := listRunningPods()
			return nil, err
		}),
	)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)

	retryPeriod := 10 * time.Millisecond
	start := time.Now()
	assert.NoError(t, provider.runRestoredNodeChecks(
		context.Background(),
		[]restoredNode{restoredNodeForTest(NodeName, instanceID)},
		0,
		retryPeriod,
		rate.NewLimiter(rate.Inf, 1),
	))
	assert.GreaterOrEqual(t, time.Since(start), retryPeriod)
}

func TestBranchENIProvider_RestoredNodeCheckPacesThreePerSecond(t *testing.T) {
	assert.Equal(t, 10*time.Minute, RestoredNodeCheckGracePeriod)
	assert.Equal(t, 3, RestoredNodeCheckRatePerSecond)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	var restoredNodes []restoredNode
	var preparedAt []time.Time
	for index := range 4 {
		nodeName := fmt.Sprintf("node-%d", index)
		instanceID := fmt.Sprintf("i-%d", index)
		restoredNodes = append(restoredNodes, restoredNodeForTest(nodeName, instanceID))
		fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
		provider.trunkENICache[nodeName] = fakeTrunk
		mockK8sAPI.EXPECT().GetNode(nodeName).
			Return(branchEligibleNode(nodeName, instanceID), nil).
			Times(2)
		fakeTrunk.EXPECT().InstanceID().Return(instanceID).Times(2)
		fakeTrunk.EXPECT().NeedsPreparation().Return(true).Times(2)
		mockPodAPI.EXPECT().GetRunningPodsOnNode(nodeName).Return(nil, nil)
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
			listRunningPods func() ([]v1.Pod, error),
		) (ec2.EC2Instance, error) {
			_, err := listRunningPods()
			preparedAt = append(preparedAt, time.Now())
			return nil, err
		})
	}

	assert.NoError(t, provider.runRestoredNodeChecks(
		context.Background(),
		restoredNodes,
		0,
		time.Millisecond,
		rate.NewLimiter(rate.Limit(RestoredNodeCheckRatePerSecond), restoredNodeCheckBurst),
	))
	if assert.Len(t, preparedAt, 4) {
		assert.GreaterOrEqual(t, preparedAt[3].Sub(preparedAt[0]), 900*time.Millisecond)
	}
}

func TestBranchENIProvider_RestoredNodeCheckRechecksAfterToken(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sAPI := getProviderAndMockK8sWrapper(ctrl)
	conditions := mock_condition.NewMockConditions(ctrl)
	provider.conditions = conditions
	conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	instanceID := "i-restored"
	oldTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	replacementTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = oldTrunk

	checked := make(chan struct{})
	mockK8sAPI.EXPECT().GetNode(NodeName).
		Return(branchEligibleNode(NodeName, instanceID), nil).
		Times(2)
	oldTrunk.EXPECT().InstanceID().Return(instanceID)
	oldTrunk.EXPECT().NeedsPreparation().DoAndReturn(func() bool {
		close(checked)
		return true
	})
	replacementTrunk.EXPECT().InstanceID().Return("i-replacement").AnyTimes()

	limiter := rate.NewLimiter(rate.Every(200*time.Millisecond), 1)
	assert.True(t, limiter.Allow())
	done := make(chan error, 1)
	go func() {
		done <- provider.runRestoredNodeChecks(
			context.Background(),
			[]restoredNode{restoredNodeForTest(NodeName, instanceID)},
			0,
			time.Millisecond,
			limiter,
		)
	}()

	<-checked
	provider.lock.Lock()
	provider.trunkENICache[NodeName] = replacementTrunk
	provider.lock.Unlock()

	assert.NoError(t, <-done)
}

// TestBranchENIProvider_GetResourceCapacity tests that the correct capacity is returned for supported instance types
func TestBranchENIProvider_GetResourceCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper := getProviderAndMockK8sWrapper(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	supportedInstanceType := "c5.xlarge"

	mockInstance.EXPECT().Type().Return(supportedInstanceType)
	mockInstance.EXPECT().Name().Return(NodeName)
	mockK8sWrapper.EXPECT().AdvertiseCapacityIfNotSet(NodeName, config.ResourceNamePodENI,
		vpc.Limits[supportedInstanceType].BranchInterface)

	err := provider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestBranchENIProvider_GetResourceCapacity_NotSupported tests that 0 is returned for non supported instance types
func TestBranchENIProvider_GetResourceCapacity_NotSupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	supportedInstanceType := "t3.medium"

	mockInstance.EXPECT().Name().Return(NodeName)
	mockInstance.EXPECT().Type().Return(supportedInstanceType)

	err := provider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

func TestBranchENIProvider_Supported_LabelNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, _ := getProviderAndMockK8sWrapper(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	supportedInstanceType := "c5.large"
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   NodeName,
			Labels: map[string]string{config.HasTrunkAttachedLabel: config.BooleanFalse},
		},
	}

	mockInstance.EXPECT().Os().Return("linux")
	mockInstance.EXPECT().Type().Return(supportedInstanceType)

	supported := provider.IsInstanceSupported(mockInstance)
	assert.True(t, supported)
	// not updating the label if the instance is supported
	assert.True(t, node.Labels[config.HasTrunkAttachedLabel] == config.BooleanFalse)
}

// TestBranchENIProvider_CreateAndAnnotateResources tests that create is invoked equal to the number of resources to
// be created
func TestBranchENIProvider_CreateAndAnnotateResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, mockSGPAPI, mockK8sAPI := getProviderAndMocks(ctrl)

	resCount := 1
	expectedAnnotation, _ := json.Marshal(EniDetails)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockSGPAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).Return(SecurityGroups, nil)
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonSecurityGroupRequested, gomock.Any(), v1.EventTypeNormal)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	gomock.InOrder(
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
			DoAndReturn(prepareWithPods(t, nil, nil)),
		fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, resCount).Return(EniDetails, nil),
	)
	mockPodAPI.EXPECT().AnnotatePod(MockPodNamespace1, MockPodName1, MockPodUID1, config.ResourceNamePodENI,
		string(expectedAnnotation)).Return(nil)
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonResourceAllocated, gomock.Any(), v1.EventTypeNormal)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
}

// The create_branch_eni latency must measure the allocation, not the wait for a restored trunk.
func TestBranchENIProvider_CreateBranchENILatencyExcludesPreparation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		provider, mockPodAPI, mockSGPAPI, mockK8sAPI := getProviderAndMocks(ctrl)
		fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
		provider.trunkENICache[NodeName] = fakeTrunk
		expectedAnnotation, err := json.Marshal(EniDetails)
		assert.NoError(t, err)

		mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
			DoAndReturn(func(func() ([]v1.Pod, error)) (ec2.EC2Instance, error) {
				time.Sleep(10 * time.Second)
				return nil, nil
			})
		mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
		mockSGPAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).Return(SecurityGroups, nil)
		mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonSecurityGroupRequested, gomock.Any(), v1.EventTypeNormal)
		fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, 1).
			DoAndReturn(func(*v1.Pod, []string, int) ([]*trunk.ENIDetails, error) {
				time.Sleep(2 * time.Second)
				return EniDetails, nil
			})
		mockPodAPI.EXPECT().AnnotatePod(MockPodNamespace1, MockPodName1, MockPodUID1, config.ResourceNamePodENI,
			string(expectedAnnotation)).Return(nil)
		mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonResourceAllocated, gomock.Any(), v1.EventTypeNormal)
		latency := branchProviderOperationLatency.WithLabelValues(operationCreateBranchENI, "1").(prometheus.Metric)
		before := writePrometheusMetric(t, latency).GetSummary().GetSampleSum()

		_, err = provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)

		assert.NoError(t, err)
		// The summary carries sums from earlier tests, so compare the delta, not the total.
		assert.InDelta(t, 2, writePrometheusMetric(t, latency).GetSummary().GetSampleSum()-before, 0.001)
	})
}

func TestBranchENIProvider_CreateAndAnnotateResources_PrepareFailureRequeuesAllocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, mockK8sAPI := getProviderAndMocks(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
		DoAndReturn(prepareWithPods(t, nil, MockError))
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonBranchAllocationFailed, gomock.Any(), v1.EventTypeWarning)

	result, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)

	assert.True(t, result.Requeue)
	assert.Equal(t, preparationRetryPeriod, result.RequeueAfter)
	assert.NoError(t, err)
}

func TestBranchENIProvider_PreparationDeferralUsesSharedDelay(t *testing.T) {
	for _, attemptError := range []error{nil, MockError} {
		name := "waiting"
		if attemptError != nil {
			name = "failed attempt"
		}
		t.Run(name, func(t *testing.T) {
			controller := gomock.NewController(t)
			provider, podAPI, _, k8sAPI := getProviderAndMocks(controller)
			trunkENI := mock_trunk.NewMockTrunkENI(controller)
			provider.trunkENICache[NodeName] = trunkENI
			podAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
			trunkENI.EXPECT().PrepareForAllocation(gomock.Any()).Return(nil, &trunk.PreparationDeferred{
				After: 8 * time.Second,
				Err:   attemptError,
			})
			if attemptError != nil {
				k8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonBranchAllocationFailed, gomock.Any(), v1.EventTypeWarning)
			}

			result, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)
			assert.NoError(t, err)
			assert.True(t, result.Requeue)
			assert.Equal(t, 8*time.Second, result.RequeueAfter)
		})
	}
}

func TestBranchENIProvider_CreateAndAnnotateResources_PreparationRetriesPastWorkerErrorLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, mockSGPAPI, mockK8sAPI := getProviderAndMocks(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk
	preparationFailures := config.WorkQueueDefaultMaxRetries + 1
	totalAttempts := preparationFailures + 1

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil).Times(totalAttempts)
	// Only the attempt that gets past preparation reads the API server and matches the SGP.
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockSGPAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).Return(SecurityGroups, nil)
	mockK8sAPI.EXPECT().BroadcastEvent(
		MockPod1,
		ReasonSecurityGroupRequested,
		gomock.Any(),
		v1.EventTypeNormal,
	)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil).Times(totalAttempts)
	prepareAttempts := 0
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
		listRunningPods func() ([]v1.Pod, error),
	) (ec2.EC2Instance, error) {
		_, err := listRunningPods()
		assert.NoError(t, err)
		prepareAttempts++
		if prepareAttempts <= preparationFailures {
			return nil, MockError
		}
		return nil, nil
	}).Times(totalAttempts)
	mockK8sAPI.EXPECT().BroadcastEvent(
		MockPod1,
		ReasonBranchAllocationFailed,
		gomock.Any(),
		v1.EventTypeWarning,
	).Times(preparationFailures)
	fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, 1).Return(EniDetails, nil)
	expectedAnnotation, err := json.Marshal(EniDetails)
	assert.NoError(t, err)
	mockPodAPI.EXPECT().AnnotatePod(
		MockPodNamespace1,
		MockPodName1,
		MockPodUID1,
		config.ResourceNamePodENI,
		string(expectedAnnotation),
	).Return(nil)
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonResourceAllocated, gomock.Any(), v1.EventTypeNormal)

	for attempt := 0; attempt < totalAttempts; attempt++ {
		result, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)
		assert.NoError(t, err)
		if attempt < preparationFailures {
			assert.True(t, result.Requeue)
			assert.Positive(t, result.RequeueAfter)
			continue
		}
		assert.False(t, result.Requeue)
	}
}

func TestBranchENIProvider_CreateAndAnnotateResources_AlreadyAnnotated_Cache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodWithAnnotation := MockPod1.DeepCopy()
	mockPodWithAnnotation.Annotations[config.ResourceNamePodENI] = "EniDetails"

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(mockPodWithAnnotation, nil)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_AlreadyAnnotatedFromAPIServer tests that if the pod is already
// annotated after getting the results from the API server no new ENIs will be created for it
func TestBranchENIProvider_CreateAndAnnotateResources_AlreadyAnnotated_APIServer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodWithAnnotation := MockPod1.DeepCopy()
	mockPodWithAnnotation.Annotations[config.ResourceNamePodENI] = "EniDetails"

	gomock.InOrder(
		mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil),
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).Return(nil, nil),
		mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).
			Return(mockPodWithAnnotation, nil),
	)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
}

// A same-name pod can be re-created on another node while this job waits, and the trunk is prepared from the
// cached pod, so the allocation must not land on the node the cache still points at.
func TestBranchENIProvider_CreateAndAnnotateResources_RequeuesWhenThePodMovedNodes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk
	movedPod := MockPod1.DeepCopy()
	movedPod.Spec.NodeName = "other-node"

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).Return(nil, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(movedPod, nil)
	fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	mockPodAPI.EXPECT().AnnotatePod(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	result, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)

	assert.NoError(t, err)
	assert.True(t, result.Requeue)
	assert.Equal(t, preparationRetryPeriod, result.RequeueAfter)
}

// TestBranchENIProvider_CreateAndAnnotateResources_GetPodError tests that error is returned if the get pod error fails
func TestBranchENIProvider_CreateAndAnnotateResources_GetPodError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).Return(nil, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(nil, MockError)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.Equal(t, MockError, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_TrunkNotPreset tests that if trunk is not present error is returned
// before the job reads the API server
func TestBranchENIProvider_CreateAndAnnotateResources_TrunkNotPreset(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	errCount := branchProviderOperationsErrCount.WithLabelValues("get_trunk_create")
	before := writePrometheusMetric(t, errCount).GetCounter().GetValue()

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)

	assert.EqualError(t, err, fmt.Sprintf("trunk not found for node %s", NodeName))
	assert.Equal(t, before+1, writePrometheusMetric(t, errCount).GetCounter().GetValue())
}

// TestBranchENIProvider_CreateAndAnnotateResources_GetSecurityGroup_Error tests that error is propagated if getting
// security group fails
func TestBranchENIProvider_CreateAndAnnotateResources_GetSecurityGroup_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, mockSGPAPI, _ := getProviderAndMocks(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).Return(nil, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockSGPAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).Return(nil, MockError)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.Error(t, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_Annotate_Error tests if annotate fails the ENIs are pushed back to
// the delete queue
func TestBranchENIProvider_CreateAndAnnotateResources_Annotate_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, mockSGPAPI, mockK8sAPI := getProviderAndMocks(ctrl)

	resCount := 1
	expectedAnnotation, _ := json.Marshal(EniDetails)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonSecurityGroupRequested, gomock.Any(), v1.EventTypeNormal)
	mockSGPAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).Return(SecurityGroups, nil)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	gomock.InOrder(
		fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
			DoAndReturn(prepareWithPods(t, nil, nil)),
		fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, resCount).Return(EniDetails, nil),
	)
	mockPodAPI.EXPECT().AnnotatePod(MockPodNamespace1, MockPodName1, MockPodUID1,
		config.ResourceNamePodENI, string(expectedAnnotation)).Return(MockError)
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonBranchENIAnnotationFailed, gomock.Any(), v1.EventTypeWarning)
	fakeTrunk.EXPECT().PushENIsToFrontOfDeleteQueue(MockPod1, EniDetails)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.Error(t, MockError, err)
}

// TestBranchENIProvider_ReconcileNode tests that the reconcile job returns no error and returns right results (with requeue after)
// when the trunk ENI is present in cache
func TestBranchENIProvider_ReconcileNode_NoLeak(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)

	fakeTrunk1 := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk1

	list := &v1.PodList{}
	mockPodAPI.EXPECT().ListPods(NodeName).Return(list, nil)

	fakeTrunk1.EXPECT().Reconcile(list.Items).Return(false)

	result := provider.ReconcileNode(NodeName)
	assert.False(t, result)
}

// TestBranchENIProvider_ReconcileNode tests that the reconcile job returns no error and returns right results (with requeue after)
// when the trunk ENI is present in cache
func TestBranchENIProvider_ReconcileNode_Leak(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)

	fakeTrunk1 := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk1

	list := &v1.PodList{}
	mockPodAPI.EXPECT().ListPods(NodeName).Return(list, nil)

	fakeTrunk1.EXPECT().Reconcile(list.Items).Return(true)

	result := provider.ReconcileNode(NodeName)
	assert.True(t, result)
}

// TestBranchENIProvider_ReconcileNode_TrunkENIDeleted tests that the reconcile job is removed once trunk eni is removed from
// the cache
func TestBranchENIProvider_ReconcileNode_TrunkENIDeleted(t *testing.T) {
	provider := getProvider()

	result := provider.ReconcileNode(NodeName)
	assert.True(t, result)
}

// TestBranchENIProvider_ProcessDeleteQueue_TrunkENIDeleted tests that the requeue job is removed once the trunk eni
// no longer exists in the cache
func TestBranchENIProvider_ProcessDeleteQueue_TrunkENIDeleted(t *testing.T) {
	provider := getProvider()

	result, err := provider.ProcessDeleteQueue(NodeName)
	assert.NoError(t, err)
	assert.Equal(t, k8sCtrl.Result{}, result)
}

// TestBranchENIProvider_ProcessDeleteQueue tests that the process delete queue job returns no error and right results
// when the trunk ENI is present in cache
func TestBranchENIProvider_ProcessDeleteQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()

	fakeTrunk1 := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk1

	fakeTrunk1.EXPECT().NeedsPreparation().Return(false)
	fakeTrunk1.EXPECT().DeleteCooledDownENIs()

	result, err := provider.ProcessDeleteQueue(NodeName)
	assert.NoError(t, err)
	assert.Equal(t, deleteQueueRequeueRequest, result)
}

// The delete tick prepares a restored trunk only when ENIs wait in its queue, so idle nodes cost no EC2 call.
func TestBranchENIProvider_ProcessDeleteQueuePreparesARestoredTrunkWithQueuedENIs(t *testing.T) {
	for _, pending := range []bool{true, false} {
		t.Run(fmt.Sprintf("pending deletions %v", pending), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)
			fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
			provider.trunkENICache[NodeName] = fakeTrunk
			calls := []*gomock.Call{
				fakeTrunk.EXPECT().NeedsPreparation().Return(true),
				fakeTrunk.EXPECT().HasPendingDeletions().Return(pending),
			}
			if pending {
				mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
				calls = append(calls, fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
					DoAndReturn(prepareWithPods(t, nil, nil)))
			}
			gomock.InOrder(append(calls, fakeTrunk.EXPECT().DeleteCooledDownENIs())...)

			result, err := provider.ProcessDeleteQueue(NodeName)

			assert.NoError(t, err)
			assert.Equal(t, deleteQueueRequeueRequest, result)
		})
	}
}

// A deferred preparation postpones the tick, counts only a failed attempt and sends no event, as a tick has no pod.
func TestBranchENIProvider_ProcessDeleteQueueWaitsForADeferredPreparation(t *testing.T) {
	for _, attemptError := range []error{nil, MockError} {
		t.Run(fmt.Sprintf("attempt error %v", attemptError), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			provider, _, _, _ := getProviderAndMocks(ctrl)
			fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
			provider.trunkENICache[NodeName] = fakeTrunk
			fakeTrunk.EXPECT().NeedsPreparation().Return(true)
			fakeTrunk.EXPECT().HasPendingDeletions().Return(true)
			fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).Return(nil, &trunk.PreparationDeferred{
				After: 4 * time.Second,
				Err:   attemptError,
			})
			failures := branchProviderOperationsErrCount.WithLabelValues("prepare_trunk_for_allocation")
			before := writePrometheusMetric(t, failures).GetCounter().GetValue()
			wantFailures := 0.0
			if attemptError != nil {
				wantFailures = 1
			}

			result, err := provider.ProcessDeleteQueue(NodeName)

			assert.NoError(t, err)
			assert.Equal(t, k8sCtrl.Result{RequeueAfter: 4 * time.Second, Requeue: true}, result)
			assert.Equal(t, before+wantFailures, writePrometheusMetric(t, failures).GetCounter().GetValue())
		})
	}
}

func TestBranchENIProvider_Introspect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getProvider()
	fakeTrunk1 := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk1

	expectedResponse := trunk.IntrospectResponse{}

	fakeTrunk1.EXPECT().Introspect().Return(expectedResponse)
	resp := provider.Introspect()
	assert.True(t, reflect.DeepEqual(resp,
		map[string]trunk.IntrospectResponse{NodeName: expectedResponse}))

	fakeTrunk1.EXPECT().Introspect().Return(expectedResponse)
	resp = provider.IntrospectNode(NodeName)
	assert.Equal(t, resp, expectedResponse)

	resp = provider.IntrospectNode("unregistered-node")
	assert.Equal(t, resp, struct{}{})
}

func TestUnSupportedNodeEvents_Linux(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, client := getProviderAndMockK8sWrapper(ctrl)

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	supportedInstanceType := "f5.large"
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   NodeName,
			Labels: map[string]string{config.HasTrunkAttachedLabel: config.BooleanFalse},
		},
	}

	mockInstance.EXPECT().Os().Return(config.OSLinux).Times(1)
	mockInstance.EXPECT().Type().Return(supportedInstanceType).Times(2)
	mockInstance.EXPECT().Name().Return(NodeName).Times(1)
	client.EXPECT().GetNode(node.Name).Return(node, nil).Times(1)
	client.EXPECT().BroadcastEvent(node, "Unsupported", gomock.Any(), v1.EventTypeWarning).Times(1)

	supported := provider.IsInstanceSupported(mockInstance)
	assert.False(t, supported)
}

func TestUnSupportedNodeEvents_Windows(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, client := getProviderAndMockK8sWrapper(ctrl)

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	supportedInstanceType := "m5.large"
	node := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   NodeName,
			Labels: map[string]string{config.HasTrunkAttachedLabel: config.BooleanFalse},
		},
	}

	mockInstance.EXPECT().Os().Return(config.OSWindows).Times(1)
	mockInstance.EXPECT().Type().Return(supportedInstanceType).Times(0)
	mockInstance.EXPECT().Name().Return(NodeName).Times(0)
	client.EXPECT().GetNode(node.Name).Return(node, nil).Times(0)
	client.EXPECT().BroadcastEvent(node, "Unsupported", gomock.Any(), v1.EventTypeWarning).Times(0)

	supported := provider.IsInstanceSupported(mockInstance)
	assert.False(t, supported)
}
