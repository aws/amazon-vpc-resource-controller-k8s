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
	"fmt"
	"strconv"
	"testing"
	"time"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/aws/aws-sdk-go/aws"
	awsEc2 "github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	// Instance details
	InstanceId            = "i-00000000000000000"
	InstanceType          = "c5.xlarge"
	SubnetId              = "subnet-00000000000000000"
	SubnetCidrBlock       = "192.168.0.0/16"
	NodeName              = "test-node"
	FakeInstance          = ec2.NewEC2Instance(NodeName, InstanceId, config.OSLinux)
	InstanceSecurityGroup = []string{"sg-1", "sg-2"}

	// Mock Pod 1
	MockPodName1      = "pod_name"
	MockPodNamespace1 = "pod_namespace"
	//PodNamespacedName1 = "pod_namespace/pod_name"
	PodUID      = "uid-1"
	MockPodUID1 = types.UID(PodUID)
	MockPod1    = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			UID:       MockPodUID1,
			Name:      MockPodName1,
			Namespace: MockPodNamespace1,
			Annotations: map[string]string{config.ResourceNamePodENI: "[{\"eniId\":\"eni-00000000000000000\",\"ifAddress\":\"FF:FF:FF:FF:FF:FF\",\"privateIp\":\"192.168.0.15\"" +
				",\"vlanId\":1,\"subnetCidr\":\"192.168.0.0/16\"},{\"eniId\":\"eni-00000000000000001\",\"ifAddress\":\"" +
				"FF:FF:FF:FF:FF:F9\",\"privateIp\":\"192.168.0.16\",\"vlanId\":2,\"subnetCidr\":\"192.168.0.0/16\"}]"}},
		Spec:   v1.PodSpec{NodeName: NodeName},
		Status: v1.PodStatus{},
	}

	// Mock Pod 2
	MockPodName2        = "pod_name_2"
	MockPodNamespace2   = ""
	MockNamespacedName2 = "default/pod_name_2"
	PodUID2             = "uid-2"
	MockPodUID2         = types.UID(PodUID2)

	MockPod2 = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			UID:         MockPodUID2,
			Name:        MockPodName2,
			Namespace:   MockPodNamespace2,
			Annotations: make(map[string]string),
		},
		Spec:   v1.PodSpec{NodeName: NodeName},
		Status: v1.PodStatus{},
	}

	// Security Groups
	SecurityGroup1 = "sg-0000000000000"
	SecurityGroup2 = "sg-0000000000000"
	SecurityGroups = []string{SecurityGroup1, SecurityGroup2}

	// Branch Interface 1
	Branch1Id = "eni-00000000000000000"
	MacAddr1  = "FF:FF:FF:FF:FF:FF"
	BranchIp1 = "192.168.0.15"
	VlanId1   = 1

	EniDetails1 = &ENIDetails{
		ID:         Branch1Id,
		MACAdd:     MacAddr1,
		IPV4Addr:   BranchIp1,
		VlanID:     VlanId1,
		SubnetCIDR: SubnetCidrBlock,
	}

	branchENIs1 = []*ENIDetails{EniDetails1}

	BranchInterface1 = &awsEc2.NetworkInterface{
		MacAddress:         &MacAddr1,
		NetworkInterfaceId: &Branch1Id,
		PrivateIpAddress:   &BranchIp1,
	}

	// Branch Interface 2
	Branch2Id = "eni-00000000000000001"
	MacAddr2  = "FF:FF:FF:FF:FF:F9"
	BranchIp2 = "192.168.0.16"
	VlanId2   = 2

	EniDetails2 = &ENIDetails{
		ID:         Branch2Id,
		MACAdd:     MacAddr2,
		IPV4Addr:   BranchIp2,
		VlanID:     VlanId2,
		SubnetCIDR: SubnetCidrBlock,
	}

	BranchInterface2 = &awsEc2.NetworkInterface{
		MacAddress:         &MacAddr2,
		NetworkInterfaceId: &Branch2Id,
		PrivateIpAddress:   &BranchIp2,
	}

	branchENIs2 = []*ENIDetails{EniDetails2}

	// Trunk Interface
	trunkId        = "eni-00000000000000002"
	trunkInterface = &awsEc2.NetworkInterface{NetworkInterfaceId: &trunkId}

	trunkIDTag = &awsEc2.Tag{
		Key:   aws.String(config.TrunkENIIDTag),
		Value: &trunkId,
	}

	vlan1Tag = []*awsEc2.Tag{{
		Key:   aws.String(config.VLandIDTag),
		Value: aws.String(strconv.Itoa(VlanId1)),
	}, trunkIDTag}

	vlan2Tag = []*awsEc2.Tag{{
		Key:   aws.String(config.VLandIDTag),
		Value: aws.String(strconv.Itoa(VlanId2)),
	}, trunkIDTag}

	instanceNwInterfaces = []*awsEc2.InstanceNetworkInterface{
		{
			InterfaceType:      aws.String("trunk"),
			NetworkInterfaceId: &trunkId,
		},
	}

	branchInterfaces = []*awsEc2.NetworkInterface{
		{
			InterfaceType:      aws.String("branch"),
			NetworkInterfaceId: &EniDetails1.ID,
			TagSet:             vlan1Tag,
		},
		{
			InterfaceType:      aws.String("branch"),
			NetworkInterfaceId: &EniDetails2.ID,
			TagSet:             vlan2Tag,
		},
	}

	trunkAssociationsBranch1And2 = []*awsEc2.TrunkInterfaceAssociation{
		{
			BranchInterfaceId: &EniDetails1.ID,
			VlanId:            aws.Int64(int64(EniDetails1.VlanID)),
		},
		{
			BranchInterfaceId: &EniDetails2.ID,
			VlanId:            aws.Int64(int64(EniDetails2.VlanID)),
		},
	}

	MockError = fmt.Errorf("mock error")
)

func getMockHelperInstanceAndTrunkObject(ctrl *gomock.Controller) (*trunkENI, *mock_api.MockEC2APIHelper,
	*mock_ec2.MockEC2Instance) {
	mockHelper := mock_api.NewMockEC2APIHelper(ctrl)
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[0] = true
	trunkENI.ec2ApiHelper = mockHelper
	trunkENI.instance = mockInstance

	// Clean up
	EniDetails1.deletionTimeStamp = time.Time{}
	EniDetails2.deletionTimeStamp = time.Time{}
	EniDetails1.deleteRetryCount = 0
	EniDetails2.deleteRetryCount = 0

	return &trunkENI, mockHelper, mockInstance
}

func getMockTrunk() trunkENI {
	log := zap.New(zap.UseDevMode(true)).WithName("node manager")
	return trunkENI{
		log:               log,
		usedVlanIds:       make([]bool, MaxAllocatableVlanIds),
		uidToBranchENIMap: map[string][]*ENIDetails{},
	}
}

func TestNewTrunkENI(t *testing.T) {
	trunkENI := NewTrunkENI(zap.New(), nil, nil)
	assert.NotNil(t, trunkENI)
}

// TestTrunkENI_assignVlanId tests that Vlan ids are assigned till the Max capacity is reached and after that assign
// call will return an error
func TestTrunkENI_assignVlanId(t *testing.T) {
	trunkENI := getMockTrunk()

	for i := 0; i < MaxAllocatableVlanIds; i++ {
		id, err := trunkENI.assignVlanId()
		assert.NoError(t, err)
		assert.Equal(t, i, id)
	}

	// Try allocating one more Vlan Id after breaching max capacity
	_, err := trunkENI.assignVlanId()
	assert.NotNil(t, err)
}

// TestTrunkENI_freeVlanId tests if a vlan id is freed it can be re assigned
func TestTrunkENI_freeVlanId(t *testing.T) {
	trunkENI := getMockTrunk()

	// Assign single Vlan Id
	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 0, id)

	// Free the vlan Id
	trunkENI.freeVlanId(0)

	// Assign single Vlan Id again
	id, err = trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 0, id)
}

func TestTrunkENI_markVlanAssigned(t *testing.T) {
	trunkENI := getMockTrunk()

	// Mark a Vlan as assigned
	trunkENI.markVlanAssigned(0)

	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, 1, id)
}

// TestTrunkENI_getBranchFromCache tests branch eni is returned when present in the cache
func TestTrunkENI_getBranchFromCache(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.uidToBranchENIMap[PodUID] = branchENIs1

	branchFromCache, isPresent := trunkENI.getBranchFromCache(PodUID)

	assert.True(t, isPresent)
	assert.Equal(t, branchENIs1, branchFromCache)
}

// TestTrunkENI_getBranchFromCache_NotPresent tests false is returned if the branch eni is not present in cache
func TestTrunkENI_getBranchFromCache_NotPresent(t *testing.T) {
	trunkENI := getMockTrunk()

	_, isPresent := trunkENI.getBranchFromCache(PodUID)

	assert.False(t, isPresent)
}

// TestTrunkENI_addBranchToCache tests branch is added to the cache
func TestTrunkENI_addBranchToCache(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.addBranchToCache(PodUID, branchENIs1)

	branchFromCache, ok := trunkENI.uidToBranchENIMap[PodUID]
	assert.True(t, ok)
	assert.Equal(t, branchENIs1, branchFromCache)
}

// TestTrunkENI_pushENIToDeleteQueue tests pushing to delete queue the data is stored in FIFO strategy
func TestTrunkENI_pushENIToDeleteQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	trunkENI.pushENIToDeleteQueue(EniDetails2)

	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[1])
}

// TestTrunkENI_pushENIsToFrontOfDeleteQueue tests ENIs are pushed to the front of the queue instead of the back
func TestTrunkENI_pushENIsToFrontOfDeleteQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	trunkENI.PushENIsToFrontOfDeleteQueue(nil, []*ENIDetails{EniDetails2})

	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[1])
}

// TestTrunkENI_pushENIsToFrontOfDeleteQueue_RemovePodFromCache tests pod is removed from cache and ENI
// are added to delete queue
func TestTrunkENI_pushENIsToFrontOfDeleteQueue_RemovePodFromCache(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails2}

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	trunkENI.PushENIsToFrontOfDeleteQueue(MockPod1, []*ENIDetails{EniDetails2})

	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[1])
	assert.NotContains(t, PodUID, trunkENI.uidToBranchENIMap)
}

// TestTrunkENI_popENIFromDeleteQueue tests if the queue has ENIs it must be removed from the queue on pop operation
func TestTrunkENI_popENIFromDeleteQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.pushENIToDeleteQueue(EniDetails1)
	eniDetails, hasENI := trunkENI.popENIFromDeleteQueue()

	assert.True(t, hasENI)
	assert.Equal(t, EniDetails1, eniDetails)

	_, hasENI = trunkENI.popENIFromDeleteQueue()
	assert.False(t, hasENI)
}

// TestTrunkENI_GetBranchInterfacesFromEC2 tests get branch interface from ec2 returns the branch interface with the
// eni id and vlan id populated
func TestTrunkENI_GetBranchInterfacesFromEC2(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	ec2APIHelper.EXPECT().DescribeTrunkInterfaceAssociation(&trunkId).Return(trunkAssociationsBranch1And2, nil)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)

	eniDetails, err := trunkENI.GetBranchInterfacesFromEC2()

	assert.NoError(t, err)

	assert.Equal(t, EniDetails1.ID, eniDetails[0].ID)
	assert.Equal(t, EniDetails1.VlanID, eniDetails[0].VlanID)

	assert.Equal(t, EniDetails2.ID, eniDetails[1].ID)
	assert.Equal(t, EniDetails2.VlanID, eniDetails[1].VlanID)
}

// TestTrunkENI_GetBranchInterfacesFromEC2_Error tests that error is returned if the operation fails
func TestTrunkENI_GetBranchInterfacesFromEC2_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	ec2APIHelper.EXPECT().DescribeTrunkInterfaceAssociation(&trunkId).Return(nil, MockError)

	_, err := trunkENI.GetBranchInterfacesFromEC2()
	assert.Error(t, MockError, err)
}

// TestTrunkENI_GetBranchInterfacesFromEC2_NoBranch tests that error is not returned when there is no branch associated
// with the trunk
func TestTrunkENI_GetBranchInterfacesFromEC2_NoBranch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	ec2APIHelper.EXPECT().DescribeTrunkInterfaceAssociation(&trunkId).Return(nil, nil)

	eniDetails, err := trunkENI.GetBranchInterfacesFromEC2()
	assert.NoError(t, err)
	assert.Nil(t, eniDetails)
}

// TestTrunkENI_getBranchInterfacesUsedByPod tests that branch interface are returned if present in pod annotation
func TestTrunkENI_getBranchInterfacesUsedByPod(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs := trunkENI.getBranchInterfacesUsedByPod(MockPod1)

	assert.Equal(t, 2, len(branchENIs))
	assert.Equal(t, EniDetails1, branchENIs[0])
	assert.Equal(t, EniDetails2, branchENIs[1])
}

// TestTrunkENI_getBranchInterfacesUsedByPod_MissingAnnotation tests that empty slice is returned if the pod has no branch
// eni annotation
func TestTrunkENI_getBranchInterfacesUsedByPod_MissingAnnotation(t *testing.T) {
	trunkENI := getMockTrunk()
	branchENIs := trunkENI.getBranchInterfacesUsedByPod(MockPod2)

	assert.Equal(t, 0, len(branchENIs))
}

// TestTrunkENI_getBranchInterfaceMap tests that the branch interface map is returned for the given branch interface slice
func TestTrunkENI_getBranchInterfaceMap(t *testing.T) {
	trunkENI := getMockTrunk()

	branchENIsMap := trunkENI.getBranchInterfaceMap([]*ENIDetails{EniDetails1})
	assert.Equal(t, EniDetails1, branchENIsMap[EniDetails1.ID])
}

// TestTrunkENI_getBranchInterfaceMap_EmptyList tests that empty map is returned if empty list is passed
func TestTrunkENI_getBranchInterfaceMap_EmptyList(t *testing.T) {
	trunkENI := getMockTrunk()

	branchENIsMap := trunkENI.getBranchInterfaceMap([]*ENIDetails{})
	assert.NotNil(t, branchENIsMap)
	assert.Zero(t, len(branchENIsMap))
}

// TestTrunkENI_deleteENI tests the trunk is deleted and vlan ID freed in case of no errors
func TestTrunkENI_deleteENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.markVlanAssigned(VlanId1)

	ec2APIHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(nil)

	err := trunkENI.deleteENI(EniDetails1)
	assert.NoError(t, err)
	assert.False(t, trunkENI.usedVlanIds[VlanId1])
}

// TestTrunkENI_deleteENI_Fail tests if the ENI deletion fails then the vlan ID is not freed
func TestTrunkENI_deleteENI_Fail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.markVlanAssigned(VlanId1)

	ec2APIHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(MockError)

	err := trunkENI.deleteENI(EniDetails1)
	assert.Error(t, MockError, err)
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
}

// TestTrunkENI_DeleteCooledDownENIs_NotCooledDown tests that ENIs that have not cooled down are not deleted
func TestTrunkENI_DeleteCooledDownENIs_NotCooledDown(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI := getMockTrunk()

	EniDetails1.deletionTimeStamp = time.Now()
	EniDetails2.deletionTimeStamp = time.Now()
	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	trunkENI.DeleteCooledDownENIs()
	assert.Equal(t, 2, len(trunkENI.deleteQueue))
}

// TestTrunkENI_DeleteCooledDownENIs_NoDeletionTimeStamp tests that ENIs are deleted if they don't have any deletion timestamp
func TestTrunkENI_DeleteCooledDownENIs_NoDeletionTimeStamp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)

	EniDetails1.deletionTimeStamp = time.Time{}
	EniDetails2.deletionTimeStamp = time.Now().Add(-time.Second * 34)
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.usedVlanIds[VlanId2] = true

	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails1.ID).Return(nil)
	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails2.ID).Return(nil)

	trunkENI.DeleteCooledDownENIs()
	assert.Equal(t, 0, len(trunkENI.deleteQueue))
}

// TestTrunkENI_DeleteCooledDownENIs_CooledDownResource tests that cooled down resources are deleted
func TestTrunkENI_DeleteCooledDownENIs_CooledDownResource(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	EniDetails1.deletionTimeStamp = time.Now().Add(-time.Second * 30)
	EniDetails2.deletionTimeStamp = time.Now().Add(-time.Second * 24)
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.usedVlanIds[VlanId2] = true

	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails1.ID).Return(nil)

	trunkENI.DeleteCooledDownENIs()
	assert.Equal(t, 1, len(trunkENI.deleteQueue))
	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[0])
}

// TestTrunkENI_DeleteCooledDownENIs_DeleteFailed tests that when delete fails item is requeued into the delete queue for
// the retry count
func TestTrunkENI_DeleteCooledDownENIs_DeleteFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, ec2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	EniDetails1.deletionTimeStamp = time.Now().Add(-time.Second * 31)
	EniDetails2.deletionTimeStamp = time.Now().Add(-time.Second * 32)
	trunkENI.usedVlanIds[VlanId1] = true
	trunkENI.usedVlanIds[VlanId2] = true

	trunkENI.deleteQueue = append(trunkENI.deleteQueue, EniDetails1, EniDetails2)

	gomock.InOrder(
		ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails1.ID).Return(MockError).Times(MaxDeleteRetries),
		ec2APIHelper.EXPECT().DeleteNetworkInterface(&EniDetails2.ID).Return(nil),
	)

	trunkENI.DeleteCooledDownENIs()
	assert.Zero(t, len(trunkENI.deleteQueue))
}

// TestTrunkENI_PushBranchENIsToCoolDownQueue tests that ENIs are pushed to the delete queue if the pod is being deleted
func TestTrunkENI_PushBranchENIsToCoolDownQueue(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails1, EniDetails2}

	trunkENI.PushBranchENIsToCoolDownQueue(PodUID)
	_, isPresent := trunkENI.uidToBranchENIMap[PodUID]

	assert.Equal(t, 2, len(trunkENI.deleteQueue))
	assert.Equal(t, EniDetails1, trunkENI.deleteQueue[0])
	assert.Equal(t, EniDetails2, trunkENI.deleteQueue[1])
	assert.False(t, isPresent)
}

// TestTrunkENI_Reconcile tests that resources used by  pods that no longer exists are cleaned up
func TestTrunkENI_Reconcile(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails1, EniDetails2}

	// Pod 1 doesn't exist anymore
	podList := []v1.Pod{*MockPod2}

	err := trunkENI.Reconcile(podList)
	assert.NoError(t, err)
	_, isPresent := trunkENI.uidToBranchENIMap[PodUID]

	assert.Equal(t, []*ENIDetails{EniDetails1, EniDetails2}, trunkENI.deleteQueue)
	assert.False(t, isPresent)
}

// TestTrunkENI_Reconcile_NoStateChange tests that no resources are deleted in case the pod still exist in the API server
func TestTrunkENI_Reconcile_NoStateChange(t *testing.T) {
	trunkENI := getMockTrunk()
	trunkENI.uidToBranchENIMap[PodUID] = []*ENIDetails{EniDetails1, EniDetails2}

	podList := []v1.Pod{*MockPod1, *MockPod2}

	err := trunkENI.Reconcile(podList)
	assert.NoError(t, err)

	_, isPresent := trunkENI.uidToBranchENIMap[PodUID]
	assert.Zero(t, trunkENI.deleteQueue)
	assert.True(t, isPresent)
}

// TestTrunkENI_InitTrunk_TrunkNotExists verifies that trunk is created if it doesn't exists
func TestTrunkENI_InitTrunk_TrunkNotExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	freeIndex := int64(2)

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockInstance.EXPECT().InstanceSecurityGroup().Return(SecurityGroups)
	mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return([]*awsEc2.InstanceNetworkInterface{}, nil)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(freeIndex, nil)
	mockInstance.EXPECT().SubnetID().Return(SubnetId)
	mockEC2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&InstanceId, &SubnetId, SecurityGroups, nil,
		&freeIndex, &TrunkEniDescription, &InterfaceTypeTrunk, nil).Return(trunkInterface, nil)

	err := trunkENI.InitTrunk(mockInstance, []v1.Pod{*MockPod2})

	assert.NoError(t, err)
	assert.Equal(t, trunkId, trunkENI.trunkENIId)
}

// TestTrunkENI_InitTrunk_ErrWhen_EmptyNWInterfaceResponse tests error is returned if an network
// interface without an interface type is returned
func TestTrunkENI_InitTrunk_ErrWhen_EmptyNWInterfaceResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(
		[]*awsEc2.InstanceNetworkInterface{{InterfaceType: nil}}, nil)

	err := trunkENI.InitTrunk(mockInstance, []v1.Pod{*MockPod2})

	assert.NotNil(t, err)
}

// TestTrunkENI_InitTrunk_GetTrunkError tests that error is returned if the get trunk call fails
func TestTrunkENI_InitTrunk_GetTrunkError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(nil, MockError)

	err := trunkENI.InitTrunk(mockInstance, []v1.Pod{*MockPod2})

	assert.Error(t, MockError, err)
}

// TestTrunkENI_InitTrunk_GetFreeIndexFail tests that error is returned if there are no free index
func TestTrunkENI_InitTrunk_GetFreeIndexFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return([]*awsEc2.InstanceNetworkInterface{}, nil)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(0), MockError)

	err := trunkENI.InitTrunk(mockInstance, []v1.Pod{*MockPod2})

	assert.Error(t, MockError, err)
}

// TestTrunkENI_InitTrunk_TrunkExists_WithBranches tests that no error is returned when trunk exists with branches
func TestTrunkENI_InitTrunk_TrunkExists_WithBranches(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(branchInterfaces, nil)
	err := trunkENI.InitTrunk(FakeInstance, []v1.Pod{*MockPod1, *MockPod2})
	branchENIs, isPresent := trunkENI.uidToBranchENIMap[PodUID]

	assert.NoError(t, err)
	assert.True(t, isPresent)

	// Assert eni details are correct
	assert.Equal(t, Branch1Id, branchENIs[0].ID)
	assert.Equal(t, Branch2Id, branchENIs[1].ID)
	assert.Equal(t, VlanId1, branchENIs[0].VlanID)
	assert.Equal(t, VlanId2, branchENIs[1].VlanID)

	// Assert that Vlan ID's are marked as used and if you retry using then you get error
	assert.True(t, trunkENI.usedVlanIds[EniDetails1.VlanID])
	assert.True(t, trunkENI.usedVlanIds[EniDetails2.VlanID])

	// Assert no entry for pod that didn't have a branch ENI
	_, isPresent = trunkENI.uidToBranchENIMap[MockNamespacedName2]
	assert.False(t, isPresent)
}

// TestTrunkENI_InitTrunk_TrunkExists_DanglingENIs tests that enis are pushed to delete queue for which there is no
// pod
func TestTrunkENI_InitTrunk_TrunkExists_DanglingENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	mockEC2APIHelper.EXPECT().GetInstanceNetworkInterface(&InstanceId).Return(instanceNwInterfaces, nil)
	mockEC2APIHelper.EXPECT().GetBranchNetworkInterface(&trunkId).Return(branchInterfaces, nil)

	err := trunkENI.InitTrunk(FakeInstance, []v1.Pod{*MockPod2})
	assert.NoError(t, err)

	_, isPresent := trunkENI.uidToBranchENIMap[PodUID]
	assert.False(t, isPresent)
	_, isPresent = trunkENI.uidToBranchENIMap[MockNamespacedName2]
	assert.False(t, isPresent)

	assert.ElementsMatch(t, []string{EniDetails1.ID, EniDetails2.ID},
		[]string{trunkENI.deleteQueue[0].ID, trunkENI.deleteQueue[1].ID})
}

// TestTrunkENI_DeleteAllBranchENIs tests all branch ENI associated with the trunk are deleted
func TestTrunkENI_DeleteAllBranchENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, _ := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.uidToBranchENIMap[PodUID] = branchENIs1
	trunkENI.uidToBranchENIMap[PodUID2] = branchENIs2
	trunkENI.deleteQueue = append(trunkENI.deleteQueue, branchENIs1[0])

	// Since we added the same branch ENIs in the cool down queue and in the pod to eni map
	mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&Branch1Id).Return(nil).Times(2)
	mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&Branch2Id).Return(nil)

	trunkENI.DeleteAllBranchENIs()
}

// TestTrunkENI_CreateAndAssociateBranchENIs test branch is created and associated with the trunk and valid eni details
// are returned
func TestTrunkENI_CreateAndAssociateBranchENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
		vlan1Tag, nil, nil).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(nil, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, vlan2Tag,
		nil, nil).Return(BranchInterface2, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

	assert.NoError(t, err)
	// VLan ID are marked as used
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	// The returned content is as expected
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
}

// TestTrunkENI_CreateAndAssociateBranchENIs_InstanceSecurityGroup test branch is created and with instance security group
// if no security group is passed.
func TestTrunkENI_CreateAndAssociateBranchENIs_InstanceSecurityGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)
	mockInstance.EXPECT().InstanceSecurityGroup().Return(InstanceSecurityGroup)

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, InstanceSecurityGroup,
		vlan1Tag, nil, nil).Return(BranchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(nil, nil)
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, InstanceSecurityGroup,
		vlan2Tag, nil, nil).Return(BranchInterface2, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, nil)

	eniDetails, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, []string{}, 2)
	expectedENIDetails := []*ENIDetails{EniDetails1, EniDetails2}

	assert.NoError(t, err)
	// VLan ID are marked as used
	assert.True(t, trunkENI.usedVlanIds[VlanId1])
	assert.True(t, trunkENI.usedVlanIds[VlanId2])
	// The returned content is as expected
	assert.Equal(t, expectedENIDetails, eniDetails)
	assert.Equal(t, expectedENIDetails, trunkENI.uidToBranchENIMap[PodUID2])
}

// TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate tests if error is returned on associate then the created interfaces
// are pushed to the delete queue
func TestTrunkENI_CreateAndAssociateBranchENIs_ErrorAssociate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(2)

	gomock.InOrder(
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			vlan1Tag, nil, nil).Return(BranchInterface1, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(nil, nil),
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups,
			vlan2Tag, nil, nil).Return(BranchInterface2, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch2Id, VlanId2).Return(nil, MockError),
	)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	assert.Error(t, MockError, err)
	assert.Equal(t, []*ENIDetails{EniDetails1, EniDetails2}, trunkENI.deleteQueue)
}

// TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate tests if error is returned on associate then the created interfaces
// are pushed to the delete queue
func TestTrunkENI_CreateAndAssociateBranchENIs_ErrorCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId

	mockInstance.EXPECT().Type().Return(InstanceType)
	mockInstance.EXPECT().SubnetID().Return(SubnetId).Times(2)
	mockInstance.EXPECT().SubnetCidrBlock().Return(SubnetCidrBlock).Times(1)

	gomock.InOrder(
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, vlan1Tag,
			nil, nil).Return(BranchInterface1, nil),
		mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &Branch1Id, VlanId1).Return(nil, nil),
		mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &SubnetId, SecurityGroups, vlan2Tag,
			nil, nil).Return(nil, MockError),
	)

	_, err := trunkENI.CreateAndAssociateBranchENIs(MockPod2, SecurityGroups, 2)
	assert.Error(t, MockError, err)
	assert.Equal(t, []*ENIDetails{EniDetails1}, trunkENI.deleteQueue)
}

func TestTrunkENI_Introspect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, _, mockInstance := getMockHelperInstanceAndTrunkObject(ctrl)
	trunkENI.trunkENIId = trunkId
	trunkENI.uidToBranchENIMap[PodUID] = branchENIs1

	mockInstance.EXPECT().InstanceID().Return(InstanceId)
	response := trunkENI.Introspect()
	assert.Equal(t, response, IntrospectResponse{
		TrunkENIID:     trunkId,
		InstanceID:     InstanceId,
		PodToBranchENI: map[string][]ENIDetails{PodUID: {*EniDetails1}}},
	)
}
