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

package ec2

import (
	"fmt"
	"testing"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
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

	instanceType    = "c5.large"
	subnetCidrBlock = "192.168.0.0/16"

	deviceIndex0 = int64(0)
	deviceIndex2 = int64(2)

	nwInterfaces = &ec2.Instance{
		InstanceId:       &instanceID,
		InstanceType:     &instanceType,
		SubnetId:         &subnetID,
		PrivateIpAddress: &privateIPAddr,
		NetworkInterfaces: []*ec2.InstanceNetworkInterface{
			{
				PrivateIpAddress: &privateIPAddr,
				Groups: []*ec2.GroupIdentifier{
					{
						GroupId: &securityGroup1,
					},
					{
						GroupId: &securityGroup2,
					},
				},
				Attachment: &ec2.InstanceNetworkInterfaceAttachment{DeviceIndex: &deviceIndex0},
			},
			{
				PrivateIpAddress: aws.String("192.168.1.2"),
				Groups: []*ec2.GroupIdentifier{
					{
						GroupId: &securityGroup3,
					},
				},
				Attachment: &ec2.InstanceNetworkInterfaceAttachment{DeviceIndex: &deviceIndex2},
			},
		},
	}

	subnet = &ec2.Subnet{
		CidrBlock: &subnetCidrBlock,
	}

	mockError = fmt.Errorf("mock error")
)

func getMockInstanceInterface() EC2Instance {
	return NewEC2Instance(nodeName, instanceID, os)
}

func getMockInstance(ctrl *gomock.Controller) (ec2Instance, *mock_ec2.MockEC2APIHelper) {
	mockEC2ApiHelper := mock_ec2.NewMockEC2APIHelper(ctrl)
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

// TestEc2Instance_LoadDetails tests that load instance details loads all the instance details correctly by making calls
// to EC2 API Helper
func TestEc2Instance_LoadDetails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(subnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)
	assert.Equal(t, subnetID, ec2Instance.SubnetID())
	assert.Equal(t, subnetCidrBlock, ec2Instance.SubnetCidrBlock())
	assert.Equal(t, instanceType, ec2Instance.Type())
	assert.Equal(t, []bool{true, false, true}, ec2Instance.deviceIndexes)
	assert.Equal(t, []string{securityGroup1, securityGroup2}, ec2Instance.InstanceSecurityGroup())
}

// TestEc2Instance_LoadDetails_SubnetPreLoaded if the subnet is already loaded it's not set to the value of the instance's
// subnet
func TestEc2Instance_LoadDetails_SubnetPreLoaded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, mockEC2ApiHelper := getMockInstance(ctrl)
	customNWSubnet := "custom-networking"
	ec2Instance.subnetID = customNWSubnet

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&customNWSubnet).Return(subnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)
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

	unsupportedInstance := "c5.xlarge-2"

	nwInterfaces.InstanceType = &unsupportedInstance

	mockEC2ApiHelper.EXPECT().GetInstanceDetails(&instanceID).Return(nwInterfaces, nil)
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(subnet, nil)

	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NotNil(t, err)

	// Clean up
	nwInterfaces.InstanceType = &instanceType
}

// TestEc2Instance_GetHighestUnusedDeviceIndex tests that if a free index exists, it is returned
func TestEc2Instance_GetHighestUnusedDeviceIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2Instance, _ := getMockInstance(ctrl)
	ec2Instance.deviceIndexes = []bool{true, false, true}

	index, err := ec2Instance.GetHighestUnusedDeviceIndex()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), index)
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

	indexToFree := int64(2)
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
	mockEC2ApiHelper.EXPECT().GetSubnet(&subnetID).Return(subnet, nil)

	// Assert no error on loading the instance details
	err := ec2Instance.LoadDetails(mockEC2ApiHelper)
	assert.NoError(t, err)

	// Check index is not used, assign index and verify index is used now
	assert.False(t, ec2Instance.deviceIndexes[1])
	index, err := ec2Instance.GetHighestUnusedDeviceIndex()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), index)
	assert.True(t, ec2Instance.deviceIndexes[1])

	// Check index is used and then free that index
	assert.True(t, ec2Instance.deviceIndexes[1])
	ec2Instance.FreeDeviceIndex(deviceIndex0)
	assert.False(t, ec2Instance.deviceIndexes[deviceIndex0])
}
