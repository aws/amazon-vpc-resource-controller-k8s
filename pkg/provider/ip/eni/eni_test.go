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

package eni

import (
	"fmt"
	"reflect"
	"testing"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	instanceID = "i-000000000000001"

	eniID1 = "eni-000000000000001"
	eniID2 = "eni-000000000000002"

	subnetID = "subnet-0000000001"
	instanceSG = []string{"sg-1"}

	ip1 = "192.168.1.0"
	ip2 = "192.168.1.1"
	ip3 = "192.168.3.0"
	ip4 = "192.168.1.1"
	ip5 = "192.168.3.3"
	ip6 = "192.168.3.4"

	mockError = fmt.Errorf("mock-error")

	instanceType = "t3.small"
	t3SmallIPCapacity = vpc.InstanceIPsAvailable[instanceType]

	nwInterfaces = []*ec2.NetworkInterface{
		{
			NetworkInterfaceId: &eniID1,
			PrivateIpAddresses: []*ec2.NetworkInterfacePrivateIpAddress{
				{PrivateIpAddress: &ip1},
				{PrivateIpAddress: &ip2},
			},
		},
		{
			NetworkInterfaceId: &eniID2,
			PrivateIpAddresses: []*ec2.NetworkInterfacePrivateIpAddress{
				{PrivateIpAddress: &ip3},
			},
		},
	}

	networkInterface1 = &ec2.NetworkInterface{
		NetworkInterfaceId: &eniID2,
		PrivateIpAddresses: []*ec2.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: &ip1, Primary: aws.Bool(true)},
			{PrivateIpAddress: &ip2, Primary: aws.Bool(false)},
			{PrivateIpAddress: &ip3, Primary: aws.Bool(false)},
			{PrivateIpAddress: &ip4, Primary: aws.Bool(false)},
		},
	}
	networkInterface2 = &ec2.NetworkInterface{
		NetworkInterfaceId: &eniID2,
		PrivateIpAddresses: []*ec2.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: &ip5, Primary: aws.Bool(true)},
			{PrivateIpAddress: &ip6, Primary: aws.Bool(false)},
		},
	}

	log = zap.New(zap.UseDevMode(true)).WithName("eni manager")
)

func createENIDetails(eniID string, remainingCapacity int) *eniDetails {
	return &eniDetails{
		eniID: eniID,
		remainingCapacity: remainingCapacity,
	}
}

func TestNewENIManager(t *testing.T) {
	eniManager := NewENIManager(nil)
	assert.NotNil(t, eniManager)
}

func getMockManager(ctrl *gomock.Controller) (eniManager, *mock_ec2.MockEC2Instance, *mock_api.MockEC2APIHelper){
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockEc2APIHelper := mock_api.NewMockEC2APIHelper(ctrl)
	return eniManager{
		ipToENIMap: map[string]*eniDetails{},
		instance:   mockInstance,
	}, mockInstance, mockEc2APIHelper
}

// TestEni_InitResources tests Init resource re builds the in memory state from syncing with EC2 API and returns the
// list of IPs present
func TestEni_InitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().InstanceID().Return(instanceID)

	mockEc2APIHelper.EXPECT().GetNetworkInterfaceOfInstance(&instanceID).Return(nwInterfaces, nil)

	// Capacity is 4 and already present 2, so remaining capacity = 4-2=2
	expectedENIDetails1 := createENIDetails(eniID1, 2)
	// Capacity is 4 and already present 1, so remaining capacity = 4-1=3
	expectedENIDetails2 := createENIDetails(eniID2, 3)

	allIPs, err := manager.InitResources(mockEc2APIHelper)

	assert.NoError(t, err)
	// Assert all the IPs are returned
	assert.Equal(t, []string{ip1, ip2, ip3} ,allIPs)
	// Assert ENIs are added to the list of available ENIs
	assert.Equal(t, []*eniDetails{expectedENIDetails1, expectedENIDetails2}, manager.availENIs)
	// Assert the ENI to IP mapping is as expected
	assert.True(t, reflect.DeepEqual(map[string]*eniDetails{ip1: expectedENIDetails1, ip2: expectedENIDetails1,
		ip3: expectedENIDetails2}, manager.ipToENIMap))
}

// TestEni_InitResources_Error tests that error is returned if the ec2 api fails to describe the instance
func TestEni_InitResources_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	mockInstance.EXPECT().InstanceID().Return(instanceID)

	mockEc2APIHelper.EXPECT().GetNetworkInterfaceOfInstance(&instanceID).Return(nwInterfaces, mockError)

	_, err := manager.InitResources(mockEc2APIHelper)

	assert.Error(t, mockError, err)
}

// TestEniManager_CreateIPV4Address_FromSingleENI tests IP are created using a single ENI when it has the desired
// capacity
func TestEniManager_CreateIPV4Address_FromSingleENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	manager.availENIs = []*eniDetails{createENIDetails(eniID1, 2)}

	mockEc2APIHelper.EXPECT().AssignIPv4AddressesAndWaitTillReady(eniID1, 2).Return([]string{ip1, ip2}, nil)
	mockInstance.EXPECT().Type().Return(instanceType).Times(2)

	ips, err := manager.CreateIPV4Address(2, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Equal(t, []string{ip1, ip2} ,ips)
	assert.Equal(t, 0, manager.availENIs[0].remainingCapacity)
}

// TestEniManager_CreateIPV4Address_FromMultipleENI tests IPs are assigned from multiple different ENIs
func TestEniManager_CreateIPV4Address_FromMultipleENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	manager.availENIs = []*eniDetails{createENIDetails(eniID1, 1),
		createENIDetails(eniID2, 3)}

	gomock.InOrder(
		mockEc2APIHelper.EXPECT().AssignIPv4AddressesAndWaitTillReady(eniID1, 1).Return([]string{ip1}, nil),
		mockEc2APIHelper.EXPECT().AssignIPv4AddressesAndWaitTillReady(eniID2, 1).Return([]string{ip2}, nil),
	)

	mockInstance.EXPECT().Type().Return(instanceType).Times(2)

	ips, err := manager.CreateIPV4Address(2, mockEc2APIHelper, log)

	assert.NoError(t, err)
	// Assert returned Ips are as expected
	assert.Equal(t, []string{ip1, ip2} ,ips)
	// Assert the remaining capacity for the eni manager is reduced
	assert.Equal(t, 0, manager.availENIs[0].remainingCapacity)
	assert.Equal(t, 2, manager.availENIs[1].remainingCapacity)
	// Assert the mapping from IP to ENI  is created
	assert.Equal(t,  manager.availENIs[0], manager.ipToENIMap[ip1])
	assert.Equal(t,  manager.availENIs[1], manager.ipToENIMap[ip2])
}

// TestEniManager_CreateIPV4Address_FromNewENI tests if existing ENIs cannot supply new IP, IPs are allocated from a new
// ENI
func TestEniManager_CreateIPV4Address_FromNewENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID1, 0)
	manager.availENIs = []*eniDetails{existingENI}

	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(2)
	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(2)
	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(2)
	mockInstance.EXPECT().InstanceSecurityGroup().Return(instanceSG).Times(2)

	gomock.InOrder(
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG , aws.Int64(3),
			&ENIDescription, nil, 3).Return(networkInterface1, nil),
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG , aws.Int64(3),
			&ENIDescription, nil, 1).Return(networkInterface2, nil),
	)

	ips, err := manager.CreateIPV4Address(4, mockEc2APIHelper, log)

	expectedNewENI1 := createENIDetails(*networkInterface1.NetworkInterfaceId, 0)
	expectedNewENI2 := createENIDetails(*networkInterface2.NetworkInterfaceId, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ip2, ip3, ip4, ip6}, ips)
	assert.Equal(t, []*eniDetails{existingENI, expectedNewENI1, expectedNewENI2}, manager.availENIs)
	assert.Equal(t, map[string]*eniDetails{ip2: expectedNewENI1, ip3: expectedNewENI1, ip4: expectedNewENI1, ip6: expectedNewENI2}, manager.ipToENIMap)
}

// TestEniManager_CreateIPV4Address_InBetweenENIFail test that if some of the IP creation fails, the ones that succeeded
// are returned
func TestEniManager_CreateIPV4Address_InBetweenENIFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID1, 0)
	manager.availENIs = []*eniDetails{existingENI}

	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(2)
	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(2)
	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(2)
	mockInstance.EXPECT().InstanceSecurityGroup().Return(instanceSG).Times(2)

	gomock.InOrder(
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG , aws.Int64(3),
			&ENIDescription, nil, 3).Return(networkInterface1, nil),
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG , aws.Int64(3),
			&ENIDescription, nil, 1).Return(nil, mockError),
	)

	ips, err := manager.CreateIPV4Address(4, mockEc2APIHelper, log)

	expectedNewENI1 := createENIDetails(*networkInterface1.NetworkInterfaceId, 0)

	assert.Error(t, mockError, err)
	assert.Equal(t, []string{ip2, ip3, ip4}, ips)
	assert.Equal(t, []*eniDetails{existingENI, expectedNewENI1}, manager.availENIs)
	assert.Equal(t, map[string]*eniDetails{ip2: expectedNewENI1, ip3: expectedNewENI1, ip4: expectedNewENI1}, manager.ipToENIMap)
}

// TestEniManager_DeleteIPV4Address tests ips are un assigned and network interface without any secondary IP is deleted
func TestEniManager_DeleteIPV4Address(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	eniDetails1 := createENIDetails(eniID1, 1)
	eniDetails2 := createENIDetails(eniID2, 2)

	manager.ipToENIMap = map[string]*eniDetails{ip1: eniDetails1, ip2: eniDetails1, ip3:eniDetails2}
	manager.availENIs = []*eniDetails{eniDetails1, eniDetails2}

	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)
	// Unassign the IPs from interface 1 and 2
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID1, ip1).Return(nil)
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID2, ip3).Return(nil)
	// Delete the network interface 2 as it has no more secondary IP left
	mockEc2APIHelper.EXPECT().DeleteNetworkInterface(&eniID2).Return(nil)

	failedToDelete, err := manager.DeleteIPV4Address([]string{ip3, ip1}, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Empty(t, failedToDelete)
	assert.Equal(t, []*eniDetails{eniDetails1}, manager.availENIs)
	assert.NotContains(t, manager.ipToENIMap, ip1)
	assert.NotContains(t, manager.ipToENIMap, ip3)
}

// TestEniManager_DeleteIPV4Address tests ips are un assigned and network interface without any secondary IP is deleted
func TestEniManager_DeleteIPV4Address_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	eniDetails1 := createENIDetails(eniID1, 1)
	eniDetails2 := createENIDetails(eniID2, 2)

	manager.ipToENIMap = map[string]*eniDetails{ip1: eniDetails1, ip2: eniDetails1, ip3:eniDetails2}
	manager.availENIs = []*eniDetails{eniDetails1, eniDetails2}

	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)
	// Unassign the IPs from interface 1 and 2
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID1, ip1).Return(mockError)
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID2, ip3).Return(nil)
	// Delete the network interface 2 as it has no more secondary IP left
	mockEc2APIHelper.EXPECT().DeleteNetworkInterface(&eniID2).Return(nil)

	failedToDelete, err := manager.DeleteIPV4Address([]string{ip1, ip3}, mockEc2APIHelper, log)

	assert.NotNil(t, err)
	assert.Equal(t, []string{ip1}, failedToDelete)
	assert.Equal(t, []*eniDetails{eniDetails1}, manager.availENIs)
	assert.Contains(t, manager.ipToENIMap, ip1)
	assert.NotContains(t, manager.ipToENIMap, ip3)
}

//TODO: Add more test cases