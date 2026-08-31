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

package ec2

import (
	"fmt"
	"testing"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	nodeName      = "name"
	os            = "linux"
	instanceID    = "i-000000000000000"
	subnetID      = "subnet-id"
	privateIPAddr = "192.168.1.0"

	securityGroup1 = "sg-1"
	securityGroup2 = "sg-2"

	securityGroup3 = "sg-3"

	instanceType    = ec2types.InstanceTypeC5Large
	subnetCidrBlock = "192.168.0.0/16"

	primaryInterfaceID = "192.168.0.2"

	deviceIndex0 = int32(0)
	deviceIndex2 = int32(2)

	nwInterfaces = &ec2types.Instance{
		InstanceId:       &instanceID,
		InstanceType:     instanceType,
		SubnetId:         &subnetID,
		PrivateIpAddress: &privateIPAddr,
		NetworkInterfaces: []ec2types.InstanceNetworkInterface{
			{
				NetworkInterfaceId: &primaryInterfaceID,
				PrivateIpAddress:   &privateIPAddr,
				Groups: []ec2types.GroupIdentifier{
					{
						GroupId: &securityGroup1,
					},
					{
						GroupId: &securityGroup2,
					},
				},
				Attachment: &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: &deviceIndex0},
			},
			{
				PrivateIpAddress: aws.String("192.168.1.2"),
				Groups: []ec2types.GroupIdentifier{
					{
						GroupId: &securityGroup3,
					},
				},
				Attachment: &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: &deviceIndex2},
			},
		},
	}

	subnet = ec2types.Subnet{
		CidrBlock: &subnetCidrBlock,
	}

	mockError = fmt.Errorf("mock error")
)

func getMockInstanceInterface() EC2Instance {
	return NewEC2Instance(nodeName, instanceID, os, zap.New())
}

func getMockInstance(ctrl *gomock.Controller) (ec2Instance, *mock_api.MockEC2APIHelper) {
	mockEC2ApiHelper := mock_api.NewMockEC2APIHelper(ctrl)
	return ec2Instance{instanceID: instanceID, os: os, name: nodeName}, mockEC2ApiHelper
}

// TestNewEC2Instance tests that the returned instance is not nil and the passed information is initialized correctly
func TestNewEC2Instance(t *testing.T) {
	ec2Instance := getMockInstanceInterface()
	assert.NotNil(t, ec2Instance)
	assert.Equal(t, nodeName, ec2Instance.Name())
	assert.Equal(t, os, ec2Instance.Os())
	assert.Equal(t, instanceID, ec2Instance.InstanceID())
}

// TestEc2Instance_IsRestoredFromNodeNetworkState_Default tests a new instance
// has not been restored.
func TestEc2Instance_IsRestoredFromNodeNetworkState_Default(t *testing.T) {
	instance := getMockInstanceInterface()
	assert.False(t, instance.IsRestoredFromNodeNetworkState())
	assert.Equal(t, "", instance.RestoredTrunkENIID())
}

// TestEc2Instance_LoadFromNodeNetworkState tests that persisted EC2 values are
// loaded and effective values are derived without an EC2 call for a standard
// networking node.
func TestEc2Instance_LoadFromNodeNetworkState(t *testing.T) {
	instance := getMockInstanceInterface()

	tcpTimeout := int32(300)
	state := rcv1alpha1.NodeNetworkState{
		InstanceID:                            instanceID,
		InstanceType:                          string(instanceType),
		InstanceSubnetID:                      subnetID,
		InstanceSubnetCIDRBlock:               subnetCidrBlock,
		PrimaryNetworkInterfaceSecurityGroups: []string{securityGroup1, securityGroup2},
		ConnectionTracking: &rcv1alpha1.ConnectionTrackingStatus{
			TCPEstablishedTimeout: &tcpTimeout,
		},
	}

	instance.LoadFromNodeNetworkState(state, "eni-trunk")
	assert.NoError(t, instance.UpdateCurrentSubnetAndCidrBlock(nil))

	assert.True(t, instance.IsRestoredFromNodeNetworkState())
	assert.Equal(t, "eni-trunk", instance.RestoredTrunkENIID())
	assert.Equal(t, string(instanceType), instance.Type())
	assert.Equal(t, subnetID, instance.SubnetID())
	assert.Equal(t, subnetCidrBlock, instance.SubnetCidrBlock())
	assert.Equal(t, "16", instance.SubnetMask())
	assert.Equal(t, []string{securityGroup1, securityGroup2}, instance.CurrentInstanceSecurityGroups())
	gotTCP, _, _ := instance.GetConnectionTrackingSpec()
	assert.Equal(t, &tcpTimeout, gotTCP)
}

// TestEc2Instance_LoadFromNodeNetworkState_CustomNetworking tests that custom
// networking derives security groups from ENIConfig and resolves its subnet
// CIDR with one GetSubnet call.
func TestEc2Instance_LoadFromNodeNetworkState_CustomNetworking(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	instance := getMockInstanceInterface()
	mockEC2API := mock_api.NewMockEC2APIHelper(ctrl)

	customSubnetID := "subnet-custom"
	customCidr := "192.168.100.0/24"
	state := rcv1alpha1.NodeNetworkState{
		InstanceID:                            instanceID,
		InstanceType:                          string(instanceType),
		InstanceSubnetID:                      subnetID,
		InstanceSubnetCIDRBlock:               subnetCidrBlock,
		PrimaryNetworkInterfaceSecurityGroups: []string{securityGroup1, securityGroup2},
	}

	instance.SetNewCustomNetworkingSpec(customSubnetID, []string{securityGroup3})
	instance.LoadFromNodeNetworkState(state, "eni-trunk")
	mockEC2API.EXPECT().GetSubnet(&customSubnetID).
		Return(&ec2types.Subnet{CidrBlock: &customCidr}, nil).
		Times(1)
	assert.NoError(t, instance.UpdateCurrentSubnetAndCidrBlock(mockEC2API))

	assert.Equal(t, customSubnetID, instance.SubnetID())
	assert.Equal(t, customCidr, instance.SubnetCidrBlock())
	assert.Equal(t, []string{securityGroup3}, instance.CurrentInstanceSecurityGroups())
}

// TestEc2Instance_BuildNodeNetworkState tests that persisted EC2 values can
// restore a fresh instance.
func TestEc2Instance_BuildNodeNetworkState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)
	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)
	assert.NoError(t, ec2Instance.LoadDetails(mockEC2ApiHelper))

	state := ec2Instance.BuildNodeNetworkState()
	assert.Equal(t, instanceID, state.InstanceID)
	assert.Equal(t, string(instanceType), state.InstanceType)
	assert.Equal(t, subnetID, state.InstanceSubnetID)
	assert.Equal(t, subnetCidrBlock, state.InstanceSubnetCIDRBlock)
	assert.Equal(t, []string{securityGroup1, securityGroup2}, state.PrimaryNetworkInterfaceSecurityGroups)

	restored := getMockInstanceInterface()
	restored.LoadFromNodeNetworkState(state, "eni-trunk")
	assert.NoError(t, restored.UpdateCurrentSubnetAndCidrBlock(nil))
	assert.Equal(t, subnetID, restored.SubnetID())
	assert.Equal(t, subnetCidrBlock, restored.SubnetCidrBlock())
	assert.Equal(t, []string{securityGroup1, securityGroup2}, restored.CurrentInstanceSecurityGroups())
}

// TestEc2Instance_LoadDetails_OverwritesRestoredState verifies that an
// authoritative reload clears optional values from restored state.
func TestEc2Instance_LoadDetails_OverwritesRestoredState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	tcpTimeout := int32(300)
	ec2Instance.LoadFromNodeNetworkState(rcv1alpha1.NodeNetworkState{
		InstanceID:                            instanceID,
		InstanceType:                          string(instanceType),
		InstanceSubnetID:                      subnetID,
		InstanceSubnetCIDRBlock:               subnetCidrBlock,
		InstanceSubnetV6CIDRBlock:             "2600:1f13::/64",
		PrimaryNetworkInterfaceSecurityGroups: []string{"sg-stale-from-state"},
		ConnectionTracking:                    &rcv1alpha1.ConnectionTrackingStatus{TCPEstablishedTimeout: &tcpTimeout},
	}, "eni-trunk")
	assert.True(t, ec2Instance.IsRestoredFromNodeNetworkState())

	// The authoritative EC2 view has no IPv6 block and no connection tracking.
	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)
	assert.NoError(t, ec2Instance.LoadDetails(mockEC2ApiHelper))

	assert.False(t, ec2Instance.IsRestoredFromNodeNetworkState())
	assert.Equal(t, "", ec2Instance.RestoredTrunkENIID())
	assert.Equal(t, "", ec2Instance.SubnetV6CidrBlock(), "restored IPv6 CIDR must not survive EC2 initialization")
	assert.Equal(t, "", ec2Instance.instanceSubnetV6CidrBlock)
	assert.Nil(t, ec2Instance.tcpEstablishedTimeout, "restored connection tracking must not survive EC2 initialization")
	assert.Equal(t, []string{securityGroup1, securityGroup2}, ec2Instance.primaryENISecurityGroups,
		"restored security groups must not survive EC2 initialization")
	assert.Equal(t, primaryInterfaceID, ec2Instance.PrimaryNetworkInterfaceID())
}

// TestEc2Instance_LoadDetails tests that load instance details loads all the instance details correctly by making calls
// to EC2 API Helper
func TestEc2Instance_LoadDetails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)
	assert.Equal(t, subnetID, ec2Instance.SubnetID())
	assert.Equal(t, subnetCidrBlock, ec2Instance.SubnetCidrBlock())
	assert.Equal(t, string(instanceType), ec2Instance.Type())
	assert.Equal(t, []bool{true, false, true}, ec2Instance.deviceIndexes)
	assert.Equal(t, []string{securityGroup1, securityGroup2}, ec2Instance.CurrentInstanceSecurityGroups())
	assert.Equal(t, primaryInterfaceID, ec2Instance.PrimaryNetworkInterfaceID())
	// No connection tracking config set in test data
	assert.Nil(t, ec2Instance.tcpEstablishedTimeout)
	assert.Nil(t, ec2Instance.udpStreamTimeout)
	assert.Nil(t, ec2Instance.udpTimeout)
}

// TestEc2Instance_LoadDetails_WithConnectionTracking tests that connection tracking config
// from the primary ENI is loaded correctly
func TestEc2Instance_LoadDetails_WithConnectionTracking(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	tcpTimeout := int32(300)
	udpStreamTimeout := int32(120)
	udpTimeout := int32(30)

	nwInterfacesWithConnTracking := &ec2types.Instance{
		InstanceId:       &instanceID,
		InstanceType:     instanceType,
		SubnetId:         &subnetID,
		PrivateIpAddress: &privateIPAddr,
		NetworkInterfaces: []ec2types.InstanceNetworkInterface{
			{
				NetworkInterfaceId: &primaryInterfaceID,
				PrivateIpAddress:   &privateIPAddr,
				Groups: []ec2types.GroupIdentifier{
					{GroupId: &securityGroup1},
					{GroupId: &securityGroup2},
				},
				Attachment: &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: &deviceIndex0},
				ConnectionTrackingConfiguration: &ec2types.ConnectionTrackingSpecificationResponse{
					TcpEstablishedTimeout: &tcpTimeout,
					UdpStreamTimeout:      &udpStreamTimeout,
					UdpTimeout:            &udpTimeout,
				},
			},
			{
				PrivateIpAddress: aws.String("192.168.1.2"),
				Groups:           []ec2types.GroupIdentifier{{GroupId: &securityGroup3}},
				Attachment:       &ec2types.InstanceNetworkInterfaceAttachment{DeviceIndex: &deviceIndex2},
			},
		},
	}

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfacesWithConnTracking, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)
	assert.Equal(t, &tcpTimeout, ec2Instance.tcpEstablishedTimeout)
	assert.Equal(t, &udpStreamTimeout, ec2Instance.udpStreamTimeout)
	assert.Equal(t, &udpTimeout, ec2Instance.udpTimeout)

	// Verify via the public getter too
	gotTcp, gotUdpStream, gotUdp := ec2Instance.GetConnectionTrackingSpec()
	assert.Equal(t, &tcpTimeout, gotTcp)
	assert.Equal(t, &udpStreamTimeout, gotUdpStream)
	assert.Equal(t, &udpTimeout, gotUdp)
}

// TestEc2Instance_LoadDetails_InstanceDetailsIsNull tests error is returned if the instance details
// response from EC2 API is null
func TestEc2Instance_LoadDetails_InstanceDetailsIsNull(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nil, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NotNil(t, err)
}

// TestEc2Instance_LoadDetails_InstanceDetails_SubnetID_IsNull tests error is returned if the instance
// is not details null but the subnet is nil
func TestEc2Instance_LoadDetails_InstanceDetails_SubnetID_IsNull(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(&ec2types.Instance{}, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NotNil(t, err)
}

// TestEc2Instance_LoadDetails_InstanceSubnet_IsNull tests error is returned if the instance subnet
// response from EC2 API is null
func TestEc2Instance_LoadDetails_InstanceSubnet_IsNull(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(nil, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NotNil(t, err)
}

// TestEc2Instance_LoadDetails_InstanceSubnet_CidrBlock_IsNull tests error is returned if the instance
// subnet CIDR Block from GetSubnet response from EC2 API is null
func TestEc2Instance_LoadDetails_InstanceSubnet_CidrBlock_IsNull(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&ec2types.Subnet{}, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NotNil(t, err)
}

// TestEc2Instance_LoadDetails_SubnetPreLoaded if the subnet is already loaded it's not set to the value of the instance's
// subnet
func TestEc2Instance_LoadDetails_SubnetPreLoaded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)
	customNWSubnetID := "custom-networking"
	customNWSecurityGroups := []string{"sg-1"}
	customNWSubnetCidr := "192.2.0.0/24"

	// Set the instance subnet ID and CIDR block
	ec2Instance.instanceSubnetID = subnetID
	ec2Instance.instanceSubnetCidrBlock = subnetCidrBlock

	// Set the custom networking subnet ID and CIDR block
	ec2Instance.newCustomNetworkingSubnetID = customNWSubnetID
	ec2Instance.newCustomNetworkingSecurityGroups = customNWSecurityGroups

	customSubnet := &ec2types.Subnet{CidrBlock: &customNWSubnetCidr}

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&customNWSubnetID).Return(customSubnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)
	assert.Equal(t, customNWSubnetID, ec2Instance.currentSubnetID)
	assert.Equal(t, customNWSecurityGroups, ec2Instance.currentInstanceSecurityGroups)
	assert.Equal(t, customNWSubnetCidr, ec2Instance.currentSubnetCIDRBlock)
}

// TestEc2Instance_LoadDetails_ErrInstanceDetails tests that if error is returned in loading instance details then the
// operation fails
func TestEc2Instance_LoadDetails_ErrInstanceDetails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nil, mockError)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.Error(t, mockError, err)
}

// TestEc2Instance_LoadDetails_ErrGetSubnet tests that if error is returned in loading subnet details then the
// operation fails
func TestEc2Instance_LoadDetails_ErrGetSubnet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(nil, mockError)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.Error(t, mockError, err)
}

// TestEc2Instance_LoadDetails_InstanceENILimitNotFound tests that the instance ENI limit is not found then the operation
// fails
func TestEc2Instance_LoadDetails_InstanceENILimitNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	unsupportedInstance := ec2types.InstanceType("c5.xlarge-2")

	nwInterfaces.InstanceType = unsupportedInstance

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NotNil(t, err)
	// ensure the expected error is returned to trigger a node event
	assert.ErrorIs(t, err, utils.ErrNotFound)

	// Clean up
	nwInterfaces.InstanceType = instanceType
}

// TestEc2Instance_GetHighestUnusedDeviceIndex tests that if a free index exists, it is returned
func TestEc2Instance_GetHighestUnusedDeviceIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, _ := getMockInstance(ctrl)
	ec2Instance.deviceIndexes = []bool{true, false, true}

	index, err := ec2Instance.GetHighestUnusedDeviceIndex()
	assert.NoError(t, err)
	assert.Equal(t, int32(1), index)
}

// TestEc2Instance_GetHighestUnusedDeviceIndex_NoFreeIndex tests that error is returned if no free index exists
func TestEc2Instance_GetHighestUnusedDeviceIndex_NoFreeIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, _ := getMockInstance(ctrl)
	ec2Instance.deviceIndexes = []bool{true, true, true}

	_, err := ec2Instance.GetHighestUnusedDeviceIndex()
	assert.NotNil(t, err)
}

// TestEc2Instance_FreeDeviceIndex tests that index is freed after making the call
func TestEc2Instance_FreeDeviceIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, _ := getMockInstance(ctrl)
	ec2Instance.deviceIndexes = []bool{true, true, true}

	indexToFree := int32(2)
	ec2Instance.FreeDeviceIndex(indexToFree)

	assert.False(t, ec2Instance.deviceIndexes[2])
}

// TestEc2Instance_E2E tests end to end workflow of loading the instance details and then assigning a free index and
// finally releasing a used device index
func TestEc2Instance_E2E(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)

	// Assert no error on loading the instance details
	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)

	// Check index is not used, assign index and verify index is used now
	assert.False(t, ec2Instance.deviceIndexes[1])
	index, err := ec2Instance.GetHighestUnusedDeviceIndex()
	assert.NoError(t, err)
	assert.Equal(t, int32(1), index)
	assert.True(t, ec2Instance.deviceIndexes[1])

	// Check index is used and then free that index
	assert.True(t, ec2Instance.deviceIndexes[1])
	ec2Instance.FreeDeviceIndex(deviceIndex0)
	assert.False(t, ec2Instance.deviceIndexes[deviceIndex0])
}

// Tests instance details when custom networking is incorrectly configured- missing security groups
func TestEc2Instance_LoadDetails_InvalidCustomNetworkingConfiguration(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	// Set the instance subnet ID and CIDR block
	ec2Instance.instanceSubnetID = subnetID
	ec2Instance.instanceSubnetCidrBlock = subnetCidrBlock

	// Set the custom networking subnet ID and CIDR block
	customNWSubnetID := "custom-networking"
	ec2Instance.newCustomNetworkingSubnetID = customNWSubnetID
	ec2Instance.newCustomNetworkingSecurityGroups = []string{}

	customNWSubnetCidr := "192.2.0.0/24"
	customSubnet := &ec2types.Subnet{CidrBlock: &customNWSubnetCidr}

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&customNWSubnetID).Return(customSubnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)
	assert.Equal(t, customNWSubnetID, ec2Instance.currentSubnetID)
	// Expect the primary network interface security groups when ENIConfig SG is missing
	assert.Equal(t, []string{securityGroup1, securityGroup2}, ec2Instance.currentInstanceSecurityGroups)
	assert.Equal(t, customNWSubnetCidr, ec2Instance.currentSubnetCIDRBlock)
}

// TestEc2Instance_LoadDetails_CustomNetworking_AfterRestore verifies that EC2
// initialization refreshes values derived during restoration.
func TestEc2Instance_LoadDetails_CustomNetworking_AfterRestore(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	customSubnetID := "subnet-custom"
	staleCidr := "10.99.0.0/24"
	freshCidr := "192.168.100.0/24"

	ec2Instance.SetNewCustomNetworkingSpec(customSubnetID, []string{securityGroup3})
	ec2Instance.LoadFromNodeNetworkState(rcv1alpha1.NodeNetworkState{
		InstanceID:                            instanceID,
		InstanceType:                          string(instanceType),
		InstanceSubnetID:                      subnetID,
		InstanceSubnetCIDRBlock:               subnetCidrBlock,
		PrimaryNetworkInterfaceSecurityGroups: []string{securityGroup1, securityGroup2},
	}, "eni-trunk")
	mockEC2ApiHelper.EXPECT().GetSubnet(&customSubnetID).
		Return(&ec2types.Subnet{CidrBlock: &staleCidr}, nil)
	assert.NoError(t, ec2Instance.UpdateCurrentSubnetAndCidrBlock(mockEC2ApiHelper))
	assert.Equal(t, staleCidr, ec2Instance.SubnetCidrBlock())

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(&subnet, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&customSubnetID).Return(&ec2types.Subnet{CidrBlock: &freshCidr}, nil)

	assert.NoError(t, ec2Instance.LoadDetails(mockEC2ApiHelper))

	assert.False(t, ec2Instance.IsRestoredFromNodeNetworkState())
	assert.Equal(t, customSubnetID, ec2Instance.SubnetID())
	assert.Equal(t, freshCidr, ec2Instance.SubnetCidrBlock())
	assert.Equal(t, []string{securityGroup3}, ec2Instance.CurrentInstanceSecurityGroups())
	// IPv6 is not supported with custom networking.
	assert.Equal(t, "", ec2Instance.SubnetV6CidrBlock())
}
