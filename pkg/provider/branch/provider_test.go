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
	"encoding/json"
	"fmt"
	"testing"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_trunk "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/branch/trunk"
	mock_utils "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/utils"
	mock_worker "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
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
)

// getProviderAndMockK8sWrapperAndHelper returns the mock provider along with the k8s wrapper and helper
func getProviderAndMockK8sWrapperAndHelper(ctrl *gomock.Controller) (branchENIProvider, *mock_k8s.MockK8sWrapper,
	*mock_utils.MockK8sCacheHelper) {
	log := zap.New(zap.UseDevMode(true)).WithName("branch provider")
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockK8sCacheHelper := mock_utils.NewMockK8sCacheHelper(ctrl)

	return branchENIProvider{
		k8s:           mockK8sWrapper,
		k8sHelper:     mockK8sCacheHelper,
		log:           log,
		trunkENICache: make(map[string]trunk.TrunkENI),
	}, mockK8sWrapper, mockK8sCacheHelper
}

// getProviderAndMockK8sWrapper returns the mock provider along with the k8s wrapper
func getProviderAndMockK8sWrapper(ctrl *gomock.Controller) (branchENIProvider, *mock_k8s.MockK8sWrapper) {
	log := zap.New(zap.UseDevMode(true)).WithName("branch provider")
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)

	return branchENIProvider{
		k8s:           mockK8sWrapper,
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

	fakeTrunk1.EXPECT().PushBranchENIsToCoolDownQueue(PodUID1).Return(nil)

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

	fakeTrunk1.EXPECT().PushBranchENIsToCoolDownQueue(PodUID1).Return(MockError)

	_, err := provider.DeleteBranchUsedByPods(NodeName, PodUID1)

	assert.Error(t, MockError, err)
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

// TestBranchENIProvider_CreateAndAnnotateResources tests that create is invoked equal to the number of resources to
// be created
func TestBranchENIProvider_CreateAndAnnotateResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, k8sHelper := getProviderAndMockK8sWrapperAndHelper(ctrl)

	resCount := 1
	expectedAnnotation, _ := json.Marshal(EniDetails)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockK8sWrapper.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sWrapper.EXPECT().GetPodFromAPIServer(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	k8sHelper.EXPECT().GetPodSecurityGroups(MockPod1).Return(SecurityGroups, nil)
	mockK8sWrapper.EXPECT().BroadcastEvent(MockPod1, ReasonSecurityGroupRequested, gomock.Any(), v1.EventTypeNormal)
	fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, resCount).Return(EniDetails, nil)
	mockK8sWrapper.EXPECT().AnnotatePod(MockPodNamespace1, MockPodName1, config.ResourceNamePodENI,
		string(expectedAnnotation)).Return(nil)
	mockK8sWrapper.EXPECT().BroadcastEvent(MockPod1, ReasonResourceAllocated, gomock.Any(), v1.EventTypeNormal)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
}

func TestBranchENIProvider_CreateAndAnnotateResources_AlreadyAnnotated_Cache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, _ := getProviderAndMockK8sWrapperAndHelper(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodWithAnnotation := MockPod1.DeepCopy()
	mockPodWithAnnotation.Annotations[config.ResourceNamePodENI] = "EniDetails"

	mockK8sWrapper.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(mockPodWithAnnotation, nil)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_AlreadyAnnotatedFromAPIServer tests that if the pod is already
// annotated after getting the results from the API server no new ENIs will be created for it
func TestBranchENIProvider_CreateAndAnnotateResources_AlreadyAnnotated_APIServer(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, _ := getProviderAndMockK8sWrapperAndHelper(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockPodWithAnnotation := MockPod1.DeepCopy()
	mockPodWithAnnotation.Annotations[config.ResourceNamePodENI] = "EniDetails"

	mockK8sWrapper.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sWrapper.EXPECT().GetPodFromAPIServer(MockPodNamespace1, MockPodName1).Return(mockPodWithAnnotation, nil)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.NoError(t, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_GetPodError tests that error is returned if the get pod error fails
func TestBranchENIProvider_CreateAndAnnotateResources_GetPodError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, _ := getProviderAndMockK8sWrapperAndHelper(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockK8sWrapper.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sWrapper.EXPECT().GetPodFromAPIServer(MockPodNamespace1, MockPodName1).Return(nil, MockError)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.Equal(t, MockError, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_TrunkNotPreset tests that if trunk is not present error is returned
func TestBranchENIProvider_CreateAndAnnotateResources_TrunkNotPreset(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, _ := getProviderAndMockK8sWrapperAndHelper(ctrl)

	resCount := 1
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockK8sWrapper.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sWrapper.EXPECT().GetPodFromAPIServer(MockPodNamespace1, MockPodName1).Return(nil, MockError)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)
	assert.NotNil(t, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_GetSecurityGroup_Error tests that error is propagated if getting
// security group fails
func TestBranchENIProvider_CreateAndAnnotateResources_GetSecurityGroup_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, k8sHelper := getProviderAndMockK8sWrapperAndHelper(ctrl)

	resCount := 1

	mockK8sWrapper.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sWrapper.EXPECT().GetPodFromAPIServer(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	k8sHelper.EXPECT().GetPodSecurityGroups(MockPod1).Return(nil, MockError)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.Error(t, err)
}

// TestBranchENIProvider_CreateAndAnnotateResources_Annotate_Error tests if annotate fails the ENIs are pushed back to
// the delete queue
func TestBranchENIProvider_CreateAndAnnotateResources_Annotate_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, k8sHelper := getProviderAndMockK8sWrapperAndHelper(ctrl)

	resCount := 1
	expectedAnnotation, _ := json.Marshal(EniDetails)
	fakeTrunk := mock_trunk.NewMockTrunkENI(ctrl)

	provider.trunkENICache[NodeName] = fakeTrunk

	mockK8sWrapper.EXPECT().GetPod(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sWrapper.EXPECT().GetPodFromAPIServer(MockPodNamespace1, MockPodName1).Return(MockPod1, nil)
	mockK8sWrapper.EXPECT().BroadcastEvent(MockPod1, ReasonSecurityGroupRequested, gomock.Any(), v1.EventTypeNormal)
	k8sHelper.EXPECT().GetPodSecurityGroups(MockPod1).Return(SecurityGroups, nil)
	fakeTrunk.EXPECT().CreateAndAssociateBranchENIs(MockPod1, SecurityGroups, resCount).Return(EniDetails, nil)
	mockK8sWrapper.EXPECT().AnnotatePod(MockPodNamespace1, MockPodName1, config.ResourceNamePodENI, string(expectedAnnotation)).Return(MockError)
	mockK8sWrapper.EXPECT().BroadcastEvent(MockPod1, ReasonBranchENIAnnotationFailed, gomock.Any(), v1.EventTypeWarning)
	fakeTrunk.EXPECT().PushENIsToFrontOfDeleteQueue(MockPod1, EniDetails)

	_, err := provider.CreateAndAnnotateResources(MockPodNamespace1, MockPodName1, resCount)

	assert.Error(t, MockError, err)
}

// TestBranchENIProvider_ReconcileNode tests that the reconcile job returns no error and returns right results (with requeue after)
// when the trunk ENI is present in cache
func TestBranchENIProvider_ReconcileNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider, mockK8sWrapper, _ := getProviderAndMockK8sWrapperAndHelper(ctrl)

	fakeTrunk1 := mock_trunk.NewMockTrunkENI(ctrl)
	provider.trunkENICache[NodeName] = fakeTrunk1

	list := &v1.PodList{}
	mockK8sWrapper.EXPECT().ListPods(NodeName).Return(list, nil)

	fakeTrunk1.EXPECT().Reconcile(list.Items)

	result, err := provider.ReconcileNode(NodeName)
	assert.NoError(t, err)
	assert.Equal(t, reconcileRequeueRequest, result)
}

// TestBranchENIProvider_ReconcileNode_TrunkENIDeleted tests that the reconcile job is removed once trunk eni is removed from
// the cache
func TestBranchENIProvider_ReconcileNode_TrunkENIDeleted(t *testing.T) {
	provider := getProvider()

	result, err := provider.ReconcileNode(NodeName)
	assert.NoError(t, err)
	assert.Equal(t, k8sCtrl.Result{}, result)
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
