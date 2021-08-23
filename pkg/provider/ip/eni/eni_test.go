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

package eni

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
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

	subnetID   = "subnet-0000000001"
	subnetMask = "16"
	instanceSG = []string{"sg-1"}

	ip1         = "192.168.1.0"
	ip1WithMask = ip1 + "/" + subnetMask
	ip2         = "192.168.1.1"
	ip2WithMask = ip2 + "/" + subnetMask
	ip3         = "192.168.1.2"
	ip3WithMask = ip3 + "/" + subnetMask
	ip4         = "192.168.2.0"
	ip4WithMask = ip4 + "/" + subnetMask
	ip5         = "192.168.2.1"
	ip6         = "192.168.2.2"
	ip6WithMask = ip6 + "/" + subnetMask

	mockError = fmt.Errorf("mock-error")

	instanceType = "t3.small"

	nwInterfaces = []*ec2.InstanceNetworkInterface{
		{
			NetworkInterfaceId: &eniID1,
			PrivateIpAddresses: []*ec2.InstancePrivateIpAddress{
				{PrivateIpAddress: &ip1, Primary: aws.Bool(false)},
				{PrivateIpAddress: &ip2, Primary: aws.Bool(false)},
			},
		},
		{
			NetworkInterfaceId: &eniID2,
			PrivateIpAddresses: []*ec2.InstancePrivateIpAddress{
				{PrivateIpAddress: &ip3, Primary: aws.Bool(false)},
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

func createENIDetails(eniID string, remainingCapacity int) *eni {
	return &eni{
		eniID:             eniID,
		remainingCapacity: remainingCapacity,
	}
}

func TestNewENIManager(t *testing.T) {
	eniManager := NewENIManager(nil)
	assert.NotNil(t, eniManager)
}

func getMockManager(ctrl *gomock.Controller) (eniManager, *mock_ec2.MockEC2Instance, *mock_api.MockEC2APIHelper) {
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockEc2APIHelper := mock_api.NewMockEC2APIHelper(ctrl)
	return eniManager{
		ipToENIMap: map[string]*eni{},
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
	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(3)

	mockEc2APIHelper.EXPECT().GetInstanceNetworkInterface(&instanceID).Return(nwInterfaces, nil)

	// Capacity is 4 and already present 2, so remaining capacity = 4-2=2
	expectedENIDetails1 := createENIDetails(eniID1, 2)
	// Capacity is 4 and already present 1, so remaining capacity = 4-1=3
	expectedENIDetails2 := createENIDetails(eniID2, 3)

	allIPs, err := manager.InitResources(mockEc2APIHelper)

	assert.NoError(t, err)
	// Assert all the IPs are returned
	assert.Equal(t, []string{ip1WithMask, ip2WithMask, ip3WithMask}, allIPs)
	// Assert ENIs are added to the list of available ENIs
	assert.Equal(t, []*eni{expectedENIDetails1, expectedENIDetails2}, manager.attachedENIs)
	// Assert the ENI to IP mapping is as expected
	assert.True(t, reflect.DeepEqual(map[string]*eni{ip1: expectedENIDetails1, ip2: expectedENIDetails1,
		ip3: expectedENIDetails2}, manager.ipToENIMap))
}

// TestEni_InitResources_Error tests that error is returned if the ec2 api fails to describe the instance
func TestEni_InitResources_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	mockInstance.EXPECT().InstanceID().Return(instanceID)

	mockEc2APIHelper.EXPECT().GetInstanceNetworkInterface(&instanceID).Return(nwInterfaces, mockError)

	_, err := manager.InitResources(mockEc2APIHelper)

	assert.Error(t, mockError, err)
}

// TestEniManager_CreateIPV4Address_FromSingleENI tests IP are created using a single ENI when it has the desired
// capacity
func TestEniManager_CreateIPV4Address_FromSingleENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	manager.attachedENIs = []*eni{createENIDetails(eniID1, 2)}

	mockEc2APIHelper.EXPECT().AssignIPv4AddressesAndWaitTillReady(eniID1, 2).Return([]string{ip1, ip2}, nil)
	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(2)
	mockInstance.EXPECT().Type().Return(instanceType).Times(2)

	ips, err := manager.CreateIPV4Address(2, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Equal(t, []string{ip1WithMask, ip2WithMask}, ips)
	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
}

// TestEniManager_CreateIPV4Address_FromMultipleENI tests IPs are assigned from multiple different ENIs
func TestEniManager_CreateIPV4Address_FromMultipleENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	manager.attachedENIs = []*eni{createENIDetails(eniID1, 1),
		createENIDetails(eniID2, 3)}

	gomock.InOrder(
		mockEc2APIHelper.EXPECT().AssignIPv4AddressesAndWaitTillReady(eniID1, 1).Return([]string{ip1}, nil),
		mockEc2APIHelper.EXPECT().AssignIPv4AddressesAndWaitTillReady(eniID2, 1).Return([]string{ip2}, nil),
	)

	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(2)

	ips, err := manager.CreateIPV4Address(2, mockEc2APIHelper, log)

	assert.NoError(t, err)
	// Assert returned Ips are as expected
	assert.Equal(t, []string{ip1WithMask, ip2WithMask}, ips)
	// Assert the remaining capacity for the eni manager is reduced
	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
	assert.Equal(t, 2, manager.attachedENIs[1].remainingCapacity)
	// Assert the mapping from IP to ENI  is created
	assert.Equal(t, manager.attachedENIs[0], manager.ipToENIMap[ip1])
	assert.Equal(t, manager.attachedENIs[1], manager.ipToENIMap[ip2])
}

// TestEniManager_CreateIPV4Address_FromNewENI tests if existing ENIs cannot supply new IP, IPs are allocated from a new
// ENI
func TestEniManager_CreateIPV4Address_FromNewENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID1, 0)
	manager.attachedENIs = []*eni{existingENI}

	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(2)
	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(2)
	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(2)
	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(4)
	mockInstance.EXPECT().InstanceSecurityGroup().Return(instanceSG).Times(2)

	gomock.InOrder(
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
			&ENIDescription, nil, 3).Return(networkInterface1, nil),
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
			&ENIDescription, nil, 1).Return(networkInterface2, nil),
	)

	ips, err := manager.CreateIPV4Address(4, mockEc2APIHelper, log)

	expectedNewENI1 := createENIDetails(*networkInterface1.NetworkInterfaceId, 0)
	expectedNewENI2 := createENIDetails(*networkInterface2.NetworkInterfaceId, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ip2WithMask, ip3WithMask, ip4WithMask, ip6WithMask}, ips)
	assert.Equal(t, []*eni{existingENI, expectedNewENI1, expectedNewENI2}, manager.attachedENIs)
	assert.Equal(t, map[string]*eni{ip2: expectedNewENI1, ip3: expectedNewENI1, ip4: expectedNewENI1, ip6: expectedNewENI2}, manager.ipToENIMap)
}

// TestEniManager_CreateIPV4Address_InBetweenENIFail test that if some of the IP creation fails, the ones that succeeded
// are returned
func TestEniManager_CreateIPV4Address_InBetweenENIFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID1, 0)
	manager.attachedENIs = []*eni{existingENI}

	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(2)
	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(2)
	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(2)
	mockInstance.EXPECT().InstanceSecurityGroup().Return(instanceSG).Times(2)

	gomock.InOrder(
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
			&ENIDescription, nil, 3).Return(networkInterface1, nil),
		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
			&ENIDescription, nil, 1).Return(nil, mockError),
	)

	ips, err := manager.CreateIPV4Address(4, mockEc2APIHelper, log)

	expectedNewENI1 := createENIDetails(*networkInterface1.NetworkInterfaceId, 0)

	assert.Error(t, mockError, err)
	assert.Equal(t, []string{ip2, ip3, ip4}, ips)
	assert.Equal(t, []*eni{existingENI, expectedNewENI1}, manager.attachedENIs)
	assert.Equal(t, map[string]*eni{ip2: expectedNewENI1, ip3: expectedNewENI1, ip4: expectedNewENI1}, manager.ipToENIMap)
}

// TestEniManager_DeleteIPV4Address tests ips are un assigned and network interface without any secondary IP is deleted
func TestEniManager_DeleteIPV4Address(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	eniDetails1 := createENIDetails(eniID1, 1)
	eniDetails2 := createENIDetails(eniID2, 1)

	manager.ipToENIMap = map[string]*eni{ip1: eniDetails1, ip2: eniDetails1, ip3: eniDetails2, ip4: eniDetails2}
	manager.attachedENIs = []*eni{eniDetails1, eniDetails2}

	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)
	// Unassign the IPs from interface 1 and 2
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID1, []string{ip1}).Return(nil)
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID2, []string{ip3, ip4}).Return(nil)
	// Delete the network interface 2 as it has no more secondary IP left
	mockEc2APIHelper.EXPECT().DeleteNetworkInterface(&eniID2).Return(nil)

	failedToDelete, err := manager.DeleteIPV4Address([]string{ip3, ip1, ip4}, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Empty(t, failedToDelete)
	assert.Equal(t, []*eni{eniDetails1}, manager.attachedENIs)
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

	manager.ipToENIMap = map[string]*eni{ip1: eniDetails1, ip2: eniDetails1, ip3: eniDetails2}
	manager.attachedENIs = []*eni{eniDetails1, eniDetails2}

	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)
	// Unassign the IPs from interface 1 and 2
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID1, []string{ip1}).Return(mockError)
	mockEc2APIHelper.EXPECT().UnassignPrivateIpAddresses(eniID2, []string{ip3}).Return(nil)
	// Delete the network interface 2 as it has no more secondary IP left
	mockEc2APIHelper.EXPECT().DeleteNetworkInterface(&eniID2).Return(nil)

	failedToDelete, err := manager.DeleteIPV4Address([]string{ip1, ip3}, mockEc2APIHelper, log)

	assert.NotNil(t, err)
	assert.Equal(t, []string{ip1}, failedToDelete)
	assert.Equal(t, []*eni{eniDetails1}, manager.attachedENIs)
	assert.Contains(t, manager.ipToENIMap, ip1)
	assert.NotContains(t, manager.ipToENIMap, ip3)
}

func TestEniManager_groupIPsPerENI(t *testing.T) {
	eni1 := &eni{eniID: eniID1}
	eni2 := &eni{eniID: eniID2}
	attachedENIs := []*eni{eni1, eni2}

	ipToENIMap := map[string]*eni{}

	ipToENIMap[ip1] = eni1
	ipToENIMap[ip2] = eni1
	ipToENIMap[ip3] = eni1
	ipToENIMap[ip4] = eni2
	ipToENIMap[ip5] = eni2

	eniManager := eniManager{ipToENIMap: ipToENIMap, attachedENIs: attachedENIs}

	groupedIP := eniManager.groupIPsPerENI([]string{ip1, ip4, ip3})
	assert.Equal(t, map[*eni][]string{eni1: {ip1, ip3}, eni2: {ip4}}, groupedIP)
}

//TODO: Add more test cases
