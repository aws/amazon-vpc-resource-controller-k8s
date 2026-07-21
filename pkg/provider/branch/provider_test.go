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

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_pod "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/pod"
	mock_trunk "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/trunk"
	mock_utils "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/utils"
	mock_worker "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	assert.NotNil(t, err)
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
	fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, resCount).Return(EniDetails, nil)
	mockPodAPI.EXPECT().AnnotatePod(MockPodNamespace1, MockPodName1, MockPodUID1, config.ResourceNamePodENI,
		string(expectedAnnotation)).Return(nil)
	mockK8sAPI.EXPECT().BroadcastEvent(MockPod1, ReasonResourceAllocated, gomock.Any(), v1.EventTypeNormal)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
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
	fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, resCount).Return(EniDetails, nil)
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

func TestBranchENIProvider_ReconcileUnassignedBranchENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockWorker := getProviderWithMockWorker(ctrl)
	provider.trunkENICache = make(map[string]trunk.TrunkENI)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	fakeTrunk.EXPECT().ReconcileUnassignedBranchENIs().Return(true, nil)
	mockWorker.EXPECT().SubmitJob(worker.NewOnDemandProcessDeleteQueueJob(NodeName))

	result, err := provider.ReconcileUnassignedBranchENIs(NodeName)

	assert.NoError(t, err)
	assert.Equal(t, k8sCtrl.Result{}, result)
}

func TestBranchENIProvider_InitTrunkFromCNINodeStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, _, _, mockK8sAPI := getProviderAndMocks(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	status := rcv1alpha1.CNINodeStatus{
		TrunkENI: rcv1alpha1.TrunkENIStatus{
			ID:       "eni-trunk",
			SubnetID: "subnet-1",
		},
	}

	mockInstance.EXPECT().LoadedFromCNINodeStatus().Return(true)
	mockInstance.EXPECT().Name().Return(NodeName)
	mockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).Return(&rcv1alpha1.CNINode{
		Status: status,
	}, nil)
	fakeTrunk.EXPECT().InitTrunkFromStatus(status.TrunkENI, []v1.Pod{*MockPod1}).Return(nil)

	initializedFromStatus, err := provider.initTrunk(mockInstance, fakeTrunk, []v1.Pod{*MockPod1}, provider.log)

	assert.NoError(t, err)
	assert.True(t, initializedFromStatus)
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

// healSnapshot is a fully-populated snapshot the self-heal reconstructs from the cached trunk.
func healSnapshot() (rcv1alpha1.TrunkENIStatus, rcv1alpha1.InstanceStatus) {
	trunkStatus := rcv1alpha1.TrunkENIStatus{ID: "eni-trunk-1", SubnetID: "subnet-1"}
	instanceStatus := rcv1alpha1.InstanceStatus{
		InstanceID:                "i-123",
		InstanceType:              "m5.large",
		InstanceSubnetID:          "subnet-1",
		CurrentSubnetID:           "subnet-1",
		PrimaryNetworkInterfaceID: "eni-primary-1",
	}
	return trunkStatus, instanceStatus
}

// TestBranchENIProvider_ReconcileCNINodeStatus_TrunkAbsent verifies the self-heal is a no-op when no
// trunk is cached for the node (nothing to persist).
func TestBranchENIProvider_ReconcileCNINodeStatus_TrunkAbsent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8s := getProviderAndMockK8sWrapper(ctrl)
	// No GetCNINode / UpdateCNINodeStatus should be called.
	mockK8s.EXPECT().GetCNINode(gomock.Any()).Times(0)
	mockK8s.EXPECT().UpdateCNINodeStatus(gomock.Any(), gomock.Any()).Times(0)

	provider.ReconcileCNINodeStatus(NodeName)
}

// TestBranchENIProvider_ReconcileCNINodeStatus_EmptyStatusPatched verifies that when the trunk is
// cached but the CNINode status is empty, the self-heal rebuilds and patches it (no EC2 calls).
func TestBranchENIProvider_ReconcileCNINodeStatus_EmptyStatusPatched(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8s := getProviderAndMockK8sWrapper(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	trunkStatus, instanceStatus := healSnapshot()
	fakeTrunk.EXPECT().CNINodeStatus().Return(trunkStatus).AnyTimes()
	fakeTrunk.EXPECT().InstanceStatus().Return(instanceStatus).AnyTimes()

	// CNINode exists but its status is empty -> patch expected.
	mockK8s.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).
		Return(&rcv1alpha1.CNINode{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}, nil).Times(1)
	mockK8s.EXPECT().UpdateCNINodeStatus(NodeName, gomock.Any()).DoAndReturn(
		func(_ string, status rcv1alpha1.CNINodeStatus) error {
			assert.Equal(t, rcv1alpha1.CNINodeStatusSnapshotVersion, status.SnapshotVersion)
			assert.Equal(t, "eni-trunk-1", status.TrunkENI.ID)
			assert.Equal(t, "i-123", status.Instance.InstanceID)
			return nil
		}).Times(1)

	provider.ReconcileCNINodeStatus(NodeName)
}

// TestBranchENIProvider_ReconcileCNINodeStatus_UpToDateSkipsPatch verifies that when the persisted
// status already matches the desired snapshot the self-heal does not re-patch.
func TestBranchENIProvider_ReconcileCNINodeStatus_UpToDateSkipsPatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8s := getProviderAndMockK8sWrapper(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	trunkStatus, instanceStatus := healSnapshot()
	fakeTrunk.EXPECT().CNINodeStatus().Return(trunkStatus).AnyTimes()
	fakeTrunk.EXPECT().InstanceStatus().Return(instanceStatus).AnyTimes()

	existing := &rcv1alpha1.CNINode{
		ObjectMeta: metav1.ObjectMeta{Name: NodeName},
		Status: rcv1alpha1.CNINodeStatus{
			SnapshotVersion: rcv1alpha1.CNINodeStatusSnapshotVersion,
			TrunkENI:        trunkStatus,
			Instance:        instanceStatus,
		},
	}
	mockK8s.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).Return(existing, nil).Times(1)
	// Already up to date -> no patch.
	mockK8s.EXPECT().UpdateCNINodeStatus(gomock.Any(), gomock.Any()).Times(0)

	provider.ReconcileCNINodeStatus(NodeName)
}

// TestBranchENIProvider_ReconcileCNINodeStatus_TrunkIDEmptySkips verifies that a cached trunk that
// has no ID yet is skipped (an ID-less snapshot cannot drive hydrate).
func TestBranchENIProvider_ReconcileCNINodeStatus_TrunkIDEmptySkips(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8s := getProviderAndMockK8sWrapper(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	fakeTrunk.EXPECT().CNINodeStatus().Return(rcv1alpha1.TrunkENIStatus{}).AnyTimes()
	fakeTrunk.EXPECT().InstanceStatus().Return(rcv1alpha1.InstanceStatus{}).AnyTimes()

	// Trunk ID empty -> neither read nor patch.
	mockK8s.EXPECT().GetCNINode(gomock.Any()).Times(0)
	mockK8s.EXPECT().UpdateCNINodeStatus(gomock.Any(), gomock.Any()).Times(0)

	provider.ReconcileCNINodeStatus(NodeName)
}

// TestBranchENIProvider_ReconcileCNINodeStatus_GetError verifies that a read error is swallowed
// (best-effort) and no patch is attempted, leaving the next reconcile to retry.
func TestBranchENIProvider_ReconcileCNINodeStatus_GetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8s := getProviderAndMockK8sWrapper(ctrl)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk

	trunkStatus, instanceStatus := healSnapshot()
	fakeTrunk.EXPECT().CNINodeStatus().Return(trunkStatus).AnyTimes()
	fakeTrunk.EXPECT().InstanceStatus().Return(instanceStatus).AnyTimes()

	mockK8s.EXPECT().GetCNINode(types.NamespacedName{Name: NodeName}).Return(nil, MockError).Times(1)
	mockK8s.EXPECT().UpdateCNINodeStatus(gomock.Any(), gomock.Any()).Times(0)

	provider.ReconcileCNINodeStatus(NodeName)
}

// fullHealSnapshot returns a fully-populated snapshot that mirrors every field
// HydrateFromCNINodeStatus validates/consumes, so cniNodeStatusUpToDate can be exercised on the
// same field set that hydrate reads back.
func fullHealSnapshot() rcv1alpha1.CNINodeStatus {
	tcp := int32(60)
	udpStream := int32(120)
	udp := int32(30)
	return rcv1alpha1.CNINodeStatus{
		SnapshotVersion: rcv1alpha1.CNINodeStatusSnapshotVersion,
		TrunkENI:        rcv1alpha1.TrunkENIStatus{ID: "eni-trunk-1", SubnetID: "subnet-1"},
		Instance: rcv1alpha1.InstanceStatus{
			InstanceID:                            "i-123",
			InstanceType:                          "m5.large",
			InstanceSubnetID:                      "subnet-1",
			InstanceSubnetCIDRBlock:               "10.0.0.0/24",
			InstanceSubnetV6CIDRBlock:             "2600:1f13::/64",
			CurrentSubnetID:                       "subnet-1",
			CurrentSubnetCIDRBlock:                "10.0.0.0/24",
			CurrentSubnetV6CIDRBlock:              "2600:1f13::/64",
			CurrentInstanceSecurityGroups:         []string{"sg-a", "sg-b"},
			SubnetMask:                            "24",
			SubnetV6Mask:                          "64",
			PrimaryNetworkInterfaceID:             "eni-primary-1",
			PrimaryNetworkInterfaceSecurityGroups: []string{"sg-a", "sg-b"},
			ConnectionTracking: &rcv1alpha1.ConnectionTrackingStatus{
				TCPEstablishedTimeout: &tcp,
				UDPStreamTimeout:      &udpStream,
				UDPTimeout:            &udp,
			},
		},
	}
}

// TestCNINodeStatusUpToDate verifies that the self-heal comparison covers exactly the fields that
// HydrateFromCNINodeStatus validates - including the security-group sets and identity/subnet fields
// that a previous version ignored - so a stale/partial snapshot is treated as out-of-date and gets
// re-patched instead of being wrongly skipped.
func TestCNINodeStatusUpToDate(t *testing.T) {
	base := fullHealSnapshot()

	// Identical snapshots are up to date.
	assert.True(t, cniNodeStatusUpToDate(base, base))

	// Security groups compared as sets: different order is still up to date.
	reordered := fullHealSnapshot()
	reordered.Instance.CurrentInstanceSecurityGroups = []string{"sg-b", "sg-a"}
	reordered.Instance.PrimaryNetworkInterfaceSecurityGroups = []string{"sg-b", "sg-a"}
	assert.True(t, cniNodeStatusUpToDate(base, reordered))

	// Each of these mutations to the persisted snapshot must make it out-of-date, proving the field
	// is actually compared (regression guard for the fields hydrate reads).
	mutations := map[string]func(s *rcv1alpha1.CNINodeStatus){
		"snapshot version":            func(s *rcv1alpha1.CNINodeStatus) { s.SnapshotVersion = "v0" },
		"trunk id":                    func(s *rcv1alpha1.CNINodeStatus) { s.TrunkENI.ID = "eni-other" },
		"trunk subnet":                func(s *rcv1alpha1.CNINodeStatus) { s.TrunkENI.SubnetID = "subnet-other" },
		"instance id":                 func(s *rcv1alpha1.CNINodeStatus) { s.Instance.InstanceID = "i-other" },
		"instance type":               func(s *rcv1alpha1.CNINodeStatus) { s.Instance.InstanceType = "m5.xlarge" },
		"instance subnet id":          func(s *rcv1alpha1.CNINodeStatus) { s.Instance.InstanceSubnetID = "subnet-other" },
		"instance subnet cidr":        func(s *rcv1alpha1.CNINodeStatus) { s.Instance.InstanceSubnetCIDRBlock = "10.1.0.0/24" },
		"instance subnet v6 cidr":     func(s *rcv1alpha1.CNINodeStatus) { s.Instance.InstanceSubnetV6CIDRBlock = "2600:1f14::/64" },
		"current subnet id":           func(s *rcv1alpha1.CNINodeStatus) { s.Instance.CurrentSubnetID = "subnet-other" },
		"current subnet cidr":         func(s *rcv1alpha1.CNINodeStatus) { s.Instance.CurrentSubnetCIDRBlock = "10.1.0.0/24" },
		"current subnet v6 cidr":      func(s *rcv1alpha1.CNINodeStatus) { s.Instance.CurrentSubnetV6CIDRBlock = "2600:1f14::/64" },
		"subnet mask":                 func(s *rcv1alpha1.CNINodeStatus) { s.Instance.SubnetMask = "16" },
		"subnet v6 mask":              func(s *rcv1alpha1.CNINodeStatus) { s.Instance.SubnetV6Mask = "56" },
		"primary eni id":              func(s *rcv1alpha1.CNINodeStatus) { s.Instance.PrimaryNetworkInterfaceID = "eni-other" },
		"current sg set changed":      func(s *rcv1alpha1.CNINodeStatus) { s.Instance.CurrentInstanceSecurityGroups = []string{"sg-a"} },
		"current sg emptied":          func(s *rcv1alpha1.CNINodeStatus) { s.Instance.CurrentInstanceSecurityGroups = nil },
		"primary sg set changed":      func(s *rcv1alpha1.CNINodeStatus) { s.Instance.PrimaryNetworkInterfaceSecurityGroups = []string{"sg-c"} },
		"primary sg emptied":          func(s *rcv1alpha1.CNINodeStatus) { s.Instance.PrimaryNetworkInterfaceSecurityGroups = nil },
		"connection tracking cleared": func(s *rcv1alpha1.CNINodeStatus) { s.Instance.ConnectionTracking = nil },
		"connection tracking changed": func(s *rcv1alpha1.CNINodeStatus) {
			v := int32(999)
			s.Instance.ConnectionTracking.TCPEstablishedTimeout = &v
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			persisted := fullHealSnapshot()
			mutate(&persisted)
			assert.False(t, cniNodeStatusUpToDate(persisted, base),
				"mutation %q should make the snapshot out of date", name)
		})
	}

	// A nil connection-tracking struct is equivalent to one with all-nil timeouts (both mean "no
	// override recorded"), so it must not trigger a needless re-patch.
	noConnTrackDesired := fullHealSnapshot()
	noConnTrackDesired.Instance.ConnectionTracking = nil
	noConnTrackPersisted := fullHealSnapshot()
	noConnTrackPersisted.Instance.ConnectionTracking = &rcv1alpha1.ConnectionTrackingStatus{}
	assert.True(t, cniNodeStatusUpToDate(noConnTrackPersisted, noConnTrackDesired))
}
