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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/golang/mock/gomock"
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

func prepareWithPods(
	t *testing.T,
	wantPods []v1.Pod,
	result error,
) func(func() ([]v1.Pod, error)) ([]v1.Pod, error) {
	t.Helper()
	return func(listRunningPods func() ([]v1.Pod, error)) ([]v1.Pod, error) {
		pods, err := listRunningPods()
		assert.NoError(t, err)
		assert.Equal(t, wantPods, pods)
		return pods, result
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
		DoAndReturn(prepareWithPods(t, pods, trunk.ErrNeedsColdInit))
	gomock.InOrder(
		fakeTrunk.EXPECT().ColdInit(pods).Return(mockInstance, nil),
		mockInstance.EXPECT().BuildNodeNetworkState().Return(state),
		fakeTrunk.EXPECT().TrunkENIID().Return(trunkENIID),
		mockK8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, state, trunkENIID).Return(nil),
		fakeTrunk.EXPECT().CompletePreparation(true),
	)

	assert.NoError(t, provider.prepareTrunkForAllocation(NodeName, fakeTrunk))
}

func TestBranchENIProvider_RestoredTrunkFallbackRequiresCheckpointRewrite(t *testing.T) {
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
		DoAndReturn(prepareWithPods(t, pods, trunk.ErrNeedsColdInit))
	gomock.InOrder(
		fakeTrunk.EXPECT().ColdInit(pods).Return(mockInstance, nil),
		mockInstance.EXPECT().BuildNodeNetworkState().Return(state),
		fakeTrunk.EXPECT().TrunkENIID().Return(trunkENIID),
		mockK8sAPI.EXPECT().PatchCNINodeCheckpoint(NodeName, state, trunkENIID).Return(MockError),
		fakeTrunk.EXPECT().CompletePreparation(false),
	)

	assert.ErrorIs(t, provider.prepareTrunkForAllocation(NodeName, fakeTrunk), MockError)
}

func checkpointPersistErrorCount(t *testing.T) float64 {
	t.Helper()
	metric := &dto.Metric{}
	assert.NoError(t, cniNodeCheckpointPersistErrCount.Write(metric))
	return metric.GetCounter().GetValue()
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
	) ([]v1.Pod, error) {
		pods, err := listRunningPods()
		close(prepared)
		return pods, err
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
	) ([]v1.Pod, error) {
		return listRunningPods()
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
	) ([]v1.Pod, error) {
		assert.True(t, podDataStoreSynced, "preparation ran before the pod datastore synced")
		return listRunningPods()
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
		) ([]v1.Pod, error) {
			return listRunningPods()
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
	assert.Equal(t, 5*time.Minute, RestoredNodeCheckGracePeriod)
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
		) ([]v1.Pod, error) {
			pods, err := listRunningPods()
			preparedAt = append(preparedAt, time.Now())
			return pods, err
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

func TestBranchENIProvider_CreateAndAnnotateResources_PrepareFailureRequeuesAllocation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, mockSGPAPI, mockK8sAPI := getProviderAndMocks(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockSGPAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).Return(SecurityGroups, nil)
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonSecurityGroupRequested, gomock.Any(), v1.EventTypeNormal)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil)
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).
		DoAndReturn(prepareWithPods(t, nil, MockError))
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonBranchAllocationFailed, gomock.Any(), v1.EventTypeWarning)

	result, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, 1)

	assert.True(t, result.Requeue)
	assert.Equal(t, preparationRetryPeriod, result.RequeueAfter)
	assert.NoError(t, err)
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
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).
		Return(MockPod1, nil).
		Times(totalAttempts)
	mockSGPAPI.EXPECT().GetMatchingSecurityGroupForPods(MockPod1).
		Return(SecurityGroups, nil).
		Times(totalAttempts)
	mockK8sAPI.EXPECT().BroadcastEvent(
		MockPod1,
		ReasonSecurityGroupRequested,
		gomock.Any(),
		v1.EventTypeNormal,
	).Times(totalAttempts)
	mockPodAPI.EXPECT().GetRunningPodsOnNode(NodeName).Return(nil, nil).Times(totalAttempts)
	prepareAttempts := 0
	fakeTrunk.EXPECT().PrepareForAllocation(gomock.Any()).DoAndReturn(func(
		listRunningPods func() ([]v1.Pod, error),
	) ([]v1.Pod, error) {
		pods, err := listRunningPods()
		assert.NoError(t, err)
		prepareAttempts++
		if prepareAttempts <= preparationFailures {
			return pods, MockError
		}
		return pods, nil
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

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(mockPodWithAnnotation, nil)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
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
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(nil, MockError)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.Equal(t, MockError, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_TrunkNotPreset tests that if trunk is not present error is returned
func TestBranchENIProvider_CreateAndAnnotateResources_TrunkNotPreset(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, _, _ := getProviderAndMocks(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockPodAPI.EXPECT().GetPodFromAPIServer(ctx, MockPodNamespace1, MockPodName1).Return(nil, MockError)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)
	assert.NotNil(t, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_GetSecurityGroup_Error tests that error is propagated if getting
// security group fails
func TestBranchENIProvider_CreateAndAnnotateResources_GetSecurityGroup_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockPodAPI, mockSGPAPI, _ := getProviderAndMocks(ctrl)

	resCount := 1

	mockPodAPI.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
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

	fakeTrunk1.EXPECT().DeleteCooledDownENIs()

	result, err := provider.ProcessDeleteQueue(NodeName)
	assert.NoError(t, err)
	assert.Equal(t, deleteQueueRequeueRequest, result)
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
