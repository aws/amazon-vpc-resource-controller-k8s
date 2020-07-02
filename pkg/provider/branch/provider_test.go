/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package branch

import (
	"encoding/json"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: Commenting because no Interface is exposed by the cache helper. Ask hao to fix
//

//	"testing"
//	"time"
//
//	mockec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
//	mockk8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
//	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
//	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
//	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
//	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
//
//	"github.com/golang/mock/gomock"
//	"github.com/google/go-cmp/cmp"
//	"github.com/stretchr/testify/assert"
//	v1 "k8s.io/api/core/v1"
//	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
//	"sigs.k8s.io/controller-runtime/pkg/log/zap"
//)

var (
	mockPodName      = "pod_name"
	mockPodNamespace = "pod_namespace"
	mockPod          = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        mockPodName,
			Namespace:   mockPodNamespace,
			Annotations: make(map[string]string),
		},
		Spec:   v1.PodSpec{NodeName: nodeName},
		Status: v1.PodStatus{},
	}

	createJob = worker.NewOnDemandCreateJob(mockPodNamespace, mockPodName, 1)
	deleteJob = worker.NewOnDemandDeleteJob(mockPodNamespace, mockPodName)

	branch1annotation, _ = json.Marshal([]*BranchENI{branch1})

	fakeInstance = ec2.NewEC2Instance(nodeName, instanceId, config.OSLinux)
)

//
//// getMockPodWithResourceAnnotation returns a mock pod with annotation of the branch ENI
//func getMockPodWithResourceAnnotation(branches []*BranchENI) *v1.Pod {
//	jsonVal, _ := json.Marshal(branches)
//	return &v1.Pod{
//		TypeMeta:   metav1.TypeMeta{},
//		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{config.ResourceNamePodENI: string(jsonVal)}},
//		Spec:       v1.PodSpec{NodeName: nodeName},
//		Status:     v1.PodStatus{},
//	}
//}
//
//// getMockProviderAndK9sWrapper returns the mock provider
//func getProviderAndMockK8sWrapper(ctrl *gomock.Controller) (branchENIProvider, *mockk8s.MockK8sWrapper) {
//	log := zap.New(zap.UseDevMode(true)).WithName("branch provider")
//	k8sWrapper := mockk8s.NewMockK8sWrapper(ctrl)
//
//	return branchENIProvider{
//		k8s:           k8sWrapper,
//		log:           log,
//		trunkENICache: make(map[string]TrunkENI),
//	}, k8sWrapper
//}
//
//type FakeTrunkENI struct {
//	DeleteInvocation int
//	InitInvocation   int
//	CreateInvocation int
//	errCreate        error
//	errDelete        error
//	errInit          error
//}
//
//// NewFakeTrunkENIWithError creates a new Fake TrunkENI object which returns the
//func NewFakeTrunkENIWithError(errCreate error, errDelete error, errInit error) *FakeTrunkENI {
//	return &FakeTrunkENI{errCreate: errCreate, errDelete: errDelete, errInit: errInit}
//}
//
//// NewFakeTrunkENI creates a new Fake TrunkENI object which returns the provided error
//func NewFakeTrunkENI() *FakeTrunkENI {
//	return &FakeTrunkENI{}
//}
//
//func (f *FakeTrunkENI) InitTrunk(_ ec2.EC2Instance) error {
//	f.InitInvocation++
//	return f.errInit
//}
//
//func (f *FakeTrunkENI) CreateAndAssociateBranchToTrunk(_ []*string) (*BranchENI, error) {
//	f.CreateInvocation++
//	return branch1, f.errCreate
//}
//
//func (f *FakeTrunkENI) DeleteBranchNetworkInterface(branchENI *BranchENI) error {
//	f.DeleteInvocation++
//	if cmp.Equal(branch1, branchENI) {
//		return nil
//	}
//	return f.errDelete
//}
//
//func getProvider() branchENIProvider {
//	log := zap.New(zap.UseDevMode(true)).WithName("branch provider")
//
//	return branchENIProvider{
//		log:           log,
//		trunkENICache: make(map[string]TrunkENI),
//	}
//}
//
//// TestBranchENIProvider_getTrunkFromCache tests Trunk ENI is returned when the trunk is present in the cache
//func TestBranchENIProvider_getTrunkFromCache(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	provider := getProvider()
//	fakeTrunk := NewFakeTrunkENI()
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	trunk, present := provider.getTrunkFromCache(nodeName)
//	assert.True(t, present)
//	assert.Equal(t, fakeTrunk, trunk)
//}
//
//// TestBranchENIProvider_getTrunkFromCache_NotExist tests that false is returned when Trunk ENI doesn't exists in the cache
//func TestBranchENIProvider_getTrunkFromCache_NotExist(t *testing.T) {
//	provider := getProvider()
//
//	trunk, present := provider.getTrunkFromCache(nodeName)
//	assert.False(t, present)
//	assert.Nil(t, trunk)
//}
//
//// TestBranchENIProvider_removeTrunkFromCache tests that once trunk ENI is removed from cache it's actually removed from
//// memory
//func TestBranchENIProvider_removeTrunkFromCache(t *testing.T) {
//	provider := getProvider()
//
//	fakeTrunk := NewFakeTrunkENI()
//
//	provider.trunkENICache[nodeName] = fakeTrunk
//	provider.removeTrunkFromCache(nodeName)
//
//	_, ok := provider.trunkENICache[nodeName]
//	assert.False(t, ok)
//}
//
//// TestBranchENIProvider_removeTrunkFromCache_NotExists tests delete doesn't panic if entry doesn't exist in cache
//func TestBranchENIProvider_removeTrunkFromCache_NotExists(t *testing.T) {
//	provider := getProvider()
//
//	// Should not throw an error
//	provider.removeTrunkFromCache(nodeName)
//}
//
//// TestBranchENIProvider_addTrunkToCache tests entry is added to cache
//func TestBranchENIProvider_addTrunkToCache(t *testing.T) {
//	provider := getProvider()
//
//	fakeTrunk := NewFakeTrunkENI()
//	err := provider.addTrunkToCache(nodeName, fakeTrunk)
//
//	assert.NoError(t, err)
//	trunk, ok := provider.trunkENICache[nodeName]
//
//	assert.True(t, ok)
//	assert.Equal(t, fakeTrunk, trunk)
//}
//
//// TestBranchENIProvider_addTrunkToCache_AlreadyExist tests error is thrown if adding an entry that already exists
//// in the memory
//func TestBranchENIProvider_addTrunkToCache_AlreadyExist(t *testing.T) {
//	provider := getProvider()
//
//	fakeTrunk := NewFakeTrunkENI()
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	err := provider.addTrunkToCache(nodeName, fakeTrunk)
//
//	assert.NotNil(t, err)
//}
//
//// TestBranchENIProvider_deleteBranchInterfaces tests the delete branch is invoked equal to the number of branch enis
//// provided to delete
//func TestBranchENIProvider_deleteBranchInterfaces(t *testing.T) {
//	provider := getProvider()
//
//	fakeTrunk := NewFakeTrunkENI()
//
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	err := provider.deleteBranchInterfaces(nodeName, fakeTrunk, []*BranchENI{branch1})
//
//	assert.Equal(t, 1, fakeTrunk.DeleteInvocation)
//	assert.Nil(t, err)
//}
//
//// TestBranchENIProvider_deleteBranchInterfaces_Err tests that err is returned if delete branch ENI fails
//func TestBranchENIProvider_deleteBranchInterfaces_Err(t *testing.T) {
//	provider := getProvider()
//
//	fakeTrunk := NewFakeTrunkENIWithError(nil, mockError, nil)
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	err := provider.deleteBranchInterfaces(nodeName, fakeTrunk, []*BranchENI{branch2})
//
//	assert.Equal(t, 1, fakeTrunk.DeleteInvocation)
//	assert.NotNil(t, err)
//}
//
//// TestBranchENIProvider_DeleteResources tests that delete resource invokes delete operation equal to the number of
//// branches in pod annotation
//func TestBranchENIProvider_DeleteResources(t *testing.T) {
//	provider := getProvider()
//
//	fakeTrunk := NewFakeTrunkENI()
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	_, err := provider.DeleteResources(getMockPodWithResourceAnnotation([]*BranchENI{branch1}))
//
//	assert.NoError(t, err)
//	assert.Equal(t, 1, fakeTrunk.DeleteInvocation)
//}
//
//// TestBranchENIProvider_DeleteResources_Error tests that error is returned when delete call fails
//func TestBranchENIProvider_DeleteResources_Error(t *testing.T) {
//	provider := getProvider()
//	fakeTrunk := NewFakeTrunkENI()
//
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	_, err := provider.DeleteResources(getMockPodWithResourceAnnotation([]*BranchENI{branch2}))
//
//	assert.Error(t, mockError, err)
//	assert.Equal(t, 1, fakeTrunk.DeleteInvocation)
//}
//
//// TestBranchENIProvider_handleCreateFailed tests that delete operation is called for each branch ENI to be deleted
//func TestBranchENIProvider_handleCreateFailed(t *testing.T) {
//	provider := getProvider()
//	fakeTrunk := NewFakeTrunkENI()
//
//	_, err := provider.handleCreateFailed(mockError, nodeName, fakeTrunk, []*BranchENI{branch1, branch2})
//	assert.NotNil(t, err)
//	assert.Equal(t, 2, fakeTrunk.DeleteInvocation)
//}
//
//// TestBranchENIProvider_DeInitResources verifies that resources is removed from cache after calling de init workflow
//func TestBranchENIProvider_DeInitResources(t *testing.T) {
//	provider := getProvider()
//	fakeTrunk := NewFakeTrunkENI()
//
//	provider.trunkENICache[nodeName] = fakeTrunk
//	err := provider.DeInitResource(fakeInstance)
//
//	_, ok := provider.trunkENICache[nodeName]
//
//	assert.NoError(t, err)
//	assert.False(t, ok)
//}
//
//// TestBranchENIProvider_GetResourceCapacity tests that the correct capacity is returned for supported instance types
//func TestBranchENIProvider_GetResourceCapacity(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	provider, mockK8sWrapper := getProviderAndMockK8sWrapper(ctrl)
//	mockInstance := mockec2.NewMockEC2Instance(ctrl)
//
//	supportedInstanceType := "c5.xlarge"
//
//	mockInstance.EXPECT().Type().Return(supportedInstanceType)
//	mockInstance.EXPECT().Name().Return(nodeName)
//	mockK8sWrapper.EXPECT().AdvertiseCapacityIfNotSet(nodeName, config.ResourceNamePodENI,
//		vpc.InstanceBranchENIsAvailable[supportedInstanceType])
//
//	err := provider.UpdateResourceCapacity(mockInstance)
//	assert.NoError(t, err)
//}
//
//// TestBranchENIProvider_GetResourceCapacity_NotSupported tests that 0 is returned for non supported instance types
//func TestBranchENIProvider_GetResourceCapacity_NotSupported(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	provider := getProvider()
//	mockInstance := mockec2.NewMockEC2Instance(ctrl)
//
//	supportedInstanceType := "t3.medium"
//
//	mockInstance.EXPECT().Type().Return(supportedInstanceType)
//
//	err := provider.UpdateResourceCapacity(mockInstance)
//	assert.NoError(t, err)
//}
//
//// TestBranchENIProvider_CreateAndAnnotateResources tests that create is invoked equal to the number of resources to
//// be created
//func TestBranchENIProvider_CreateAndAnnotateResources(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	provider, mockK8sWrapper := getProviderAndMockK8sWrapper(ctrl)
//
//	resCount := 2
//	expectedAnnotation, _ := json.Marshal([]*BranchENI{branch1, branch1})
//	fakeTrunk := NewFakeTrunkENI()
//
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	mockK8sWrapper.EXPECT().AnnotatePod(mockPodNamespace, mockPodName, config.ResourceNamePodENI, string(expectedAnnotation))
//
//	_, err := provider.CreateAndAnnotateResources(mockPod, resCount)
//
//	assert.NoError(t, err)
//	assert.Equal(t, resCount, fakeTrunk.CreateInvocation)
//}
//
//// TestBranchENIProvider_CreateAndAnnotateResources_ErrCreate tests that error is returned if the create call fails
//func TestBranchENIProvider_CreateAndAnnotateResources_ErrCreate(t *testing.T) {
//	provider := getProvider()
//	fakeTrunk := NewFakeTrunkENIWithError(mockError, nil, nil)
//
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	_, err := provider.CreateAndAnnotateResources(mockPod, 2)
//	assert.Error(t, mockError, err)
//	assert.Equal(t, 1, fakeTrunk.CreateInvocation)
//}
//
//// TestBranchENIProvider_ProcessAsyncJob_Create tests E2E processing of a Create job
//func TestBranchENIProvider_ProcessAsyncJob_Create(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	provider, mockK8sWrapper := getProviderAndMockK8sWrapper(ctrl)
//	worker.NewOnDemandCreateJob(mockPodNamespace, mockPodName, 1)
//
//	fakeTrunk := NewFakeTrunkENI()
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	mockK8sWrapper.EXPECT().GetPod(mockPodNamespace, mockPodName).Return(mockPod, nil)
//	mockK8sWrapper.EXPECT().AnnotatePod(mockPodNamespace, mockPodName, config.ResourceNamePodENI, string(branch1annotation))
//
//	_, err := provider.ProcessAsyncJob(createJob)
//	assert.Error(t, mockError, err)
//	assert.Equal(t, 1, fakeTrunk.CreateInvocation)
//}
//
//// TestBranchENIProvider_ProcessAsyncJob_Delete tests E2E processing a Delete Job
//func TestBranchENIProvider_ProcessAsyncJob_Delete(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	provider, mockK8sWrapper := getProviderAndMockK8sWrapper(ctrl)
//	worker.NewOnDemandCreateJob(mockPodNamespace, mockPodName, 1)
//
//	fakeTrunk := NewFakeTrunkENI()
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	deletedPod := mockPod.DeepCopy()
//	deletedPod.DeletionTimestamp = &metav1.Time{Time: time.Now()}
//	deletedPod.Annotations[config.ResourceNamePodENI] = string(branch1annotation)
//
//	mockK8sWrapper.EXPECT().GetPod(mockPodNamespace, mockPodName).Return(deletedPod, nil)
//
//	_, err := provider.ProcessAsyncJob(deleteJob)
//	assert.Error(t, mockError, err)
//	assert.Equal(t, 1, fakeTrunk.DeleteInvocation)
//}
//
//func TestBranchENIProvider_ProcessAsyncJob_PodDeletedBeforeRequest(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	provider, mockK8sWrapper := getProviderAndMockK8sWrapper(ctrl)
//	worker.NewOnDemandCreateJob(mockPodNamespace, mockPodName, 1)
//
//	fakeTrunk := NewFakeTrunkENI()
//	provider.trunkENICache[nodeName] = fakeTrunk
//
//	mockK8sWrapper.EXPECT().GetPod(mockPodNamespace, mockPodName).Return(nil, mockError)
//
//	_, err := provider.ProcessAsyncJob(createJob)
//	assert.Error(t, mockError, err)
//}
