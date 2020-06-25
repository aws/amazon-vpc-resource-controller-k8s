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
	"fmt"
	"testing"

	mockEC2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mockEC2API "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	// Instance details
	instanceId      = "i-00000000000000000"
	subnetId        = "subnet-00000000000000000"
	subnetCidrBlock = "192.168.0.0/16"
	nodeName        = "test-node"

	// Security Groups
	securityGroup1 = "sg-0000000000000"
	securityGroup2 = "sg-0000000000000"
	securityGroups = []*string{&securityGroup1, &securityGroup2}

	// Branch Interface 1
	branch1Id = "eni-00000000000000000"
	macAddr1  = "FF:FF:FF:FF:FF:FF"
	branchIp1 = "192.168.0.15"
	vlanId1   = int64(1)

	branch1 = &BranchENI{
		BranchENId: &branch1Id,
		MacAdd:     &macAddr1,
		BranchIp:   &branchIp1,
		VlanId:     &vlanId1,
		SubnetCIDR: &subnetCidrBlock,
	}

	branchInterface1 = &ec2.NetworkInterface{
		MacAddress:         &macAddr1,
		NetworkInterfaceId: &branch1Id,
		PrivateIpAddress:   &branchIp1,
	}

	// Branch Interface 2
	branch2Id = "eni-00000000000000001"
	macAddr2  = "FF:FF:FF:FF:FF:F9"
	branchIp2 = "192.168.0.16"
	vlanId2   = int64(2)

	branch2 = &BranchENI{
		BranchENId: &branch2Id,
		MacAdd:     &macAddr2,
		BranchIp:   &branchIp2,
		VlanId:     &vlanId2,
		SubnetCIDR: &subnetCidrBlock,
	}

	branchInterface2 = &ec2.NetworkInterface{
		MacAddress:         &macAddr2,
		NetworkInterfaceId: &branch2Id,
		PrivateIpAddress:   &branchIp2,
	}

	// Trunk Interface
	trunkId        = "eni-00000000000000002"
	trunkInterface = &ec2.NetworkInterface{NetworkInterfaceId: &trunkId}

	trunkAssociationsBranch1Only = []*ec2.TrunkInterfaceAssociation{
		{
			BranchInterfaceId: branch1.BranchENId,
			VlanId:            branch1.VlanId,
		},
	}

	trunkAssociationsBranch1And2 = []*ec2.TrunkInterfaceAssociation{
		{
			BranchInterfaceId: branch1.BranchENId,
			VlanId:            branch1.VlanId,
		},
		{
			BranchInterfaceId: branch2.BranchENId,
			VlanId:            branch2.VlanId,
		},
	}

	mockError = fmt.Errorf("mock error")
)

func getMockHelperAndTrunkInterface(ctrl *gomock.Controller) (TrunkENI, *mockEC2API.MockEC2APIHelper) {
	log := zap.New(zap.UseDevMode(true)).WithName("node manager")
	mockHelper := mockEC2API.NewMockEC2APIHelper(ctrl)

	trunkENI := NewTrunkENI(log, instanceId, subnetId, subnetCidrBlock, mockHelper)

	return trunkENI, mockHelper
}

func getMockHelperAndTrunkObject(ctrl *gomock.Controller) (*trunkENI, *mockEC2API.MockEC2APIHelper) {
	mockHelper := mockEC2API.NewMockEC2APIHelper(ctrl)

	trunkENI := getMockTrunk()
	trunkENI.usedVlanIds[0] = true
	trunkENI.ec2ApiHelper = mockHelper

	return &trunkENI, mockHelper
}

func getMockTrunk() trunkENI {
	log := zap.New(zap.UseDevMode(true)).WithName("node manager")
	return trunkENI{
		subnetCidrBlock: &subnetCidrBlock,
		subnetId:        &subnetId,
		instanceId:      &instanceId,
		log:             log,
		usedVlanIds:     make([]bool, MaxAllocatableVlanIds),
		branchENIs:      map[string]*BranchENI{},
	}
}

func TestNewTrunkENI(t *testing.T) {
	trunkENI := NewTrunkENI(nil, instanceId, subnetId, subnetCidrBlock, nil)
	assert.NotNil(t, trunkENI)
}

// TestTrunkENI_assignVlanId tests that Vlan ids are assigned till the Max capacity is reached and after that assign
// call will return an error
func TestTrunkENI_assignVlanId(t *testing.T) {
	trunkENI := getMockTrunk()

	for i := 0; i < MaxAllocatableVlanIds; i++ {
		id, err := trunkENI.assignVlanId()
		assert.NoError(t, err)
		assert.Equal(t, int64(i), id)
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
	assert.Equal(t, int64(0), id)

	// Free the vlan Id
	trunkENI.freeVlanId(int64(0))

	// Assign single Vlan Id again
	id, err = trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, int64(0), id)
}

func TestTrunkENI_markVlanAssigned(t *testing.T) {
	trunkENI := getMockTrunk()

	// Mark a Vlan as assigned
	err := trunkENI.markVlanAssigned(int64(0))
	assert.NoError(t, err)

	id, err := trunkENI.assignVlanId()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), id)
}

// TestTrunkENI_markVlanAssigned_Error_AlreadyAssigned tests that error is returned in case we try to re assign an
// already assigned vlan id
func TestTrunkENI_markVlanAssigned_Error_AlreadyAssigned(t *testing.T) {
	trunkENI := getMockTrunk()

	// Mark a Vlan as assigned
	err := trunkENI.markVlanAssigned(int64(0))
	assert.NoError(t, err)
	err = trunkENI.markVlanAssigned(int64(0))
	assert.NotNil(t, err)
}

// TestTrunkENI_getBranchFromCache tests branch eni is returned when present in the cache
func TestTrunkENI_getBranchFromCache(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.branchENIs[branch1Id] = branch1

	branchFromCache, isPresent := trunkENI.getBranchFromCache(&branch1Id)

	assert.True(t, isPresent)
	assert.Equal(t, branch1, branchFromCache)
}

// TestTrunkENI_getBranchFromCache_NotPresent tests false is returned if the branch eni is not present in cache
func TestTrunkENI_getBranchFromCache_NotPresent(t *testing.T) {
	trunkENI := getMockTrunk()

	_, isPresent := trunkENI.getBranchFromCache(&branch1Id)

	assert.False(t, isPresent)
}

// TestTrunkENI_addBranchToCache tests branch is added to the cache
func TestTrunkENI_addBranchToCache(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.addBranchToCache(branch1)

	branchFromCache, ok := trunkENI.branchENIs[*branch1.BranchENId]
	assert.True(t, ok)
	assert.Equal(t, branch1, branchFromCache)
}

// TestTrunkENI_removeBranchFromCache tests that trunk is removed from the cache
func TestTrunkENI_removeBranchFromCache(t *testing.T) {
	trunkENI := getMockTrunk()

	trunkENI.branchENIs[branch1Id] = branch1

	trunkENI.removeBranchFromCache(&branch1Id)

	_, ok := trunkENI.branchENIs[branch1Id]
	assert.False(t, ok)
}

// TestTrunkENI_InitTrunk_TrunkNotExists verifies that trunk is created if it doesn't exists
func TestTrunkENI_InitTrunk_TrunkNotExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	mockInstance := mockEC2.NewMockEC2Instance(ctrl)
	freeIndex := int64(2)

	mockInstance.EXPECT().InstanceID().Return(instanceId)
	mockEC2APIHelper.EXPECT().GetTrunkInterface(&instanceId).Return(nil, nil)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(freeIndex, nil)
	mockEC2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceId, &subnetId, nil,
		&freeIndex, &TrunkEniDescription, &InterfaceTypeTrunk, 0).Return(trunkInterface, nil)

	err := trunkENI.InitTrunk(mockInstance)

	assert.NoError(t, err)
	assert.Equal(t, trunkId, *trunkENI.trunkENIId)
}

// TestTrunkENI_InitTrunk_GetTrunkError tests that error is returned if the get trunk call fails
func TestTrunkENI_InitTrunk_GetTrunkError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	mockInstance := mockEC2.NewMockEC2Instance(ctrl)

	mockInstance.EXPECT().InstanceID().Return(instanceId)
	mockEC2APIHelper.EXPECT().GetTrunkInterface(&instanceId).Return(nil, mockError)

	err := trunkENI.InitTrunk(mockInstance)

	assert.Error(t, mockError, err)
}

// TestTrunkENI_InitTrunk_GetFreeIndexFail tests that error is returned if there are no free index
func TestTrunkENI_InitTrunk_GetFreeIndexFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	mockInstance := mockEC2.NewMockEC2Instance(ctrl)

	mockInstance.EXPECT().InstanceID().Return(instanceId)
	mockEC2APIHelper.EXPECT().GetTrunkInterface(&instanceId).Return(nil, nil)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(0), mockError)

	err := trunkENI.InitTrunk(mockInstance)

	assert.Error(t, mockError, err)
}

// TestTrunkENI_InitTrunk_TrunkExists_NoBranches tests that no error is returned when trunk exists with no branches
func TestTrunkENI_InitTrunk_TrunkExists_NoBranches(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)

	mockEC2APIHelper.EXPECT().GetTrunkInterface(&instanceId).Return(trunkInterface, nil)
	mockEC2APIHelper.EXPECT().DescribeTrunkInterfaceAssociation(&trunkId).Return([]*ec2.TrunkInterfaceAssociation{}, nil)

	err := trunkENI.InitTrunk(fakeInstance)
	assert.NoError(t, err)
	assert.Equal(t, trunkId, *trunkENI.trunkENIId)
}

// TestTrunkENI_InitTrunk_TrunkExists_WithBranches tests that no error is returned when trunk exists with branches
func TestTrunkENI_InitTrunk_TrunkExists_WithBranches(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)

	mockEC2APIHelper.EXPECT().GetTrunkInterface(&instanceId).Return(trunkInterface, nil)
	mockEC2APIHelper.EXPECT().DescribeTrunkInterfaceAssociation(&trunkId).Return(trunkAssociationsBranch1And2, nil)
	mockEC2APIHelper.EXPECT().DescribeNetworkInterfaces([]*string{&branch1Id, &branch2Id}).
		Return([]*ec2.NetworkInterface{branchInterface1, branchInterface2}, nil)

	err := trunkENI.InitTrunk(fakeInstance)
	// Assert Trunk details are correct
	assert.NoError(t, err)
	assert.Equal(t, trunkId, *trunkENI.trunkENIId)

	// Assert that the branches are stored in the map
	assert.Equal(t, branch1, trunkENI.branchENIs[branch1Id])
	assert.Equal(t, branch2, trunkENI.branchENIs[branch2Id])

	// Assert that Vlan ID's are marked as used and if you retry using then you get error
	assert.True(t, trunkENI.usedVlanIds[*branch1.VlanId])
	assert.True(t, trunkENI.usedVlanIds[*branch2.VlanId])
}

// TestTrunkENI_CreateAndAssociateBranchToTrunk test branch is created and associated with valid input
func TestTrunkENI_CreateAndAssociateBranchToTrunk(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	trunkENI.trunkENIId = &trunkId

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &subnetId, securityGroups, 0, nil).
		Return(branchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &branch1Id, branch1.VlanId).Return(nil, nil)

	branch, err := trunkENI.CreateAndAssociateBranchToTrunk(securityGroups)

	assert.True(t, trunkENI.usedVlanIds[*branch1.VlanId])
	assert.NoError(t, err)
	assert.Equal(t, trunkENI.branchENIs[branch1Id], branch)
}

// TestTrunkENI_CreateAndAssociateBranchToTrunk_ErrorCreate tests error is returned if the create call fails
func TestTrunkENI_CreateAndAssociateBranchToTrunk_ErrorCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	trunkENI.trunkENIId = &trunkId

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &subnetId, securityGroups, 0, nil).
		Return(branchInterface1, mockError)

	_, err := trunkENI.CreateAndAssociateBranchToTrunk(securityGroups)
	_, exists := trunkENI.branchENIs[branch1Id]

	assert.False(t, trunkENI.usedVlanIds[*branch1.VlanId])
	assert.Error(t, mockError, err)
	assert.False(t, exists)
}

// TestTrunkENI_CreateAndAssociateBranchToTrunk_ErrorAssociate tests error is returned if the associate call fails
func TestTrunkENI_CreateAndAssociateBranchToTrunk_ErrorAssociate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	trunkENI.trunkENIId = &trunkId

	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &subnetId, securityGroups, 0, nil).
		Return(branchInterface1, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &branch1Id, branch1.VlanId).Return(nil, mockError)
	mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&branch1Id).Return(nil)

	_, err := trunkENI.CreateAndAssociateBranchToTrunk(securityGroups)
	_, exists := trunkENI.branchENIs[branch1Id]

	assert.False(t, trunkENI.usedVlanIds[*branch1.VlanId])
	assert.Error(t, mockError, err)
	assert.False(t, exists)
}

// TestTrunkENI_DeleteBranchNetworkInterface tests network interface is deleted when valid input is passed and
// ensures vlan id is freed
func TestTrunkENI_DeleteBranchNetworkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	trunkENI.trunkENIId = &trunkId

	trunkENI.usedVlanIds[vlanId1] = true
	trunkENI.branchENIs[branch1Id] = branch1

	mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&branch1Id).Return(nil)

	err := trunkENI.DeleteBranchNetworkInterface(branch1)
	assert.NoError(t, err)
	assert.False(t, trunkENI.usedVlanIds[vlanId1])
}

func TestTrunkENI_DeleteBranchNetworkInterface_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkObject(ctrl)
	trunkENI.trunkENIId = &trunkId

	trunkENI.usedVlanIds[vlanId1] = true
	trunkENI.branchENIs[branch1Id] = branch1

	mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&branch1Id).Return(mockError)

	err := trunkENI.DeleteBranchNetworkInterface(branch1)
	assert.Error(t, mockError, err)
	// Vlan should not be freed since failed to delete branch interface
	assert.True(t, trunkENI.usedVlanIds[vlanId1])
}

// TestTrunkENI_E2E is the end to end test that test the init workflow, add workflow and the delete workflow
func TestTrunkENI_E2E_Init_Create_Delete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	trunkENI, mockEC2APIHelper := getMockHelperAndTrunkInterface(ctrl)

	// Init Trunk API Calls
	mockEC2APIHelper.EXPECT().GetTrunkInterface(&instanceId).Return(trunkInterface, nil)
	mockEC2APIHelper.EXPECT().DescribeTrunkInterfaceAssociation(&trunkId).Return(trunkAssociationsBranch1Only, nil)
	mockEC2APIHelper.EXPECT().DescribeNetworkInterfaces([]*string{branch1.BranchENId}).
		Return([]*ec2.NetworkInterface{branchInterface1}, nil)

	// Create 2nd Branch ENI API Calls
	mockEC2APIHelper.EXPECT().CreateNetworkInterface(&BranchEniDescription, &subnetId, securityGroups, 0, nil).
		Return(branchInterface2, nil)
	mockEC2APIHelper.EXPECT().AssociateBranchToTrunk(&trunkId, &branch2Id, &vlanId2).Return(nil, nil)

	// Delete 1st and 2nd Branch ENI API Calls
	gomock.InOrder(
		mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&branch1Id).Return(nil),
		mockEC2APIHelper.EXPECT().DeleteNetworkInterface(&branch2Id).Return(nil),
	)

	// Init Trunk - With existing branch - Branch 1
	err := trunkENI.InitTrunk(fakeInstance)
	assert.NoError(t, err)

	// Create a new Branch ENI - Branch 2
	createdENI, err := trunkENI.CreateAndAssociateBranchToTrunk(securityGroups)
	assert.Equal(t, branch2, createdENI)
	assert.NoError(t, err)

	// Delete both branch ENI - Branch 1 and Branch 2
	err = trunkENI.DeleteBranchNetworkInterface(branch1)
	assert.NoError(t, err)
	err = trunkENI.DeleteBranchNetworkInterface(branch2)
	assert.NoError(t, err)
}
