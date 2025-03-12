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

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	instanceID   = "i-000000000000001"
	instanceName = "ip-192-0-2-1.us-west-2.compute.internal"

	eniID1 = "eni-000000000000001"
	eniID2 = "eni-000000000000002"
	eniID3 = "eni-000000000000003"

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
	prefix1     = "192.168.1.0/28"
	prefix2     = "192.168.2.0/28"
	prefix3     = "192.168.3.0/28"

	mockError = fmt.Errorf("mock-error")

	instanceType = "t3.small"

	nwInterfaces = []ec2types.InstanceNetworkInterface{
		{
			NetworkInterfaceId: &eniID1,
			PrivateIpAddresses: []ec2types.InstancePrivateIpAddress{
				{PrivateIpAddress: &ip1, Primary: aws.Bool(false)},
				{PrivateIpAddress: &ip2, Primary: aws.Bool(false)},
			},
			Ipv4Prefixes: []ec2types.InstanceIpv4Prefix{
				{Ipv4Prefix: &prefix1},
			},
		},
		{
			NetworkInterfaceId: &eniID2,
			PrivateIpAddresses: []ec2types.InstancePrivateIpAddress{
				{PrivateIpAddress: &ip3, Primary: aws.Bool(false)},
			},
		},
	}

	networkInterface1 = ec2types.NetworkInterface{
		NetworkInterfaceId: &eniID2,
		PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: &ip1, Primary: aws.Bool(true)},
			{PrivateIpAddress: &ip2, Primary: aws.Bool(false)},
			{PrivateIpAddress: &ip3, Primary: aws.Bool(false)},
			{PrivateIpAddress: &ip4, Primary: aws.Bool(false)},
		},
	}
	networkInterface2 = ec2types.NetworkInterface{
		NetworkInterfaceId: &eniID2,
		PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: &ip5, Primary: aws.Bool(true)},
			{PrivateIpAddress: &ip6, Primary: aws.Bool(false)},
		},
	}
	networkInterface3 = ec2types.NetworkInterface{
		NetworkInterfaceId: &eniID3,
		PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: &ip1, Primary: aws.Bool(true)},
		},
		Ipv4Prefixes: []ec2types.Ipv4PrefixSpecification{{Ipv4Prefix: &prefix1}},
	}
	networkInterface4 = ec2types.NetworkInterface{
		NetworkInterfaceId: &eniID3,
		PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
			{PrivateIpAddress: &ip1, Primary: aws.Bool(true)},
		},
		Ipv4Prefixes: []ec2types.Ipv4PrefixSpecification{{Ipv4Prefix: &prefix1}, {Ipv4Prefix: &prefix2}, {Ipv4Prefix: &prefix3}},
	}
	log = zap.New(zap.UseDevMode(true)).WithName("eni manager")

	ipCountFor3     = &config.IPResourceCount{SecondaryIPv4Count: 3, IPv4PrefixCount: 0}
	ipCountFor1     = &config.IPResourceCount{SecondaryIPv4Count: 1, IPv4PrefixCount: 0}
	prefixCountFor3 = &config.IPResourceCount{SecondaryIPv4Count: 0, IPv4PrefixCount: 3}
	prefixCountFor1 = &config.IPResourceCount{SecondaryIPv4Count: 0, IPv4PrefixCount: 1}
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
		resourceToENIMap: map[string]*eni{},
		instance:         mockInstance,
	}, mockInstance, mockEc2APIHelper
}

// TestEni_InitResources tests Init resource re builds the in memory state from syncing with EC2 API and returns the
// list of IPs and Prefixes present
func TestEni_InitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().InstanceID().Return(instanceID)
	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(3)

	mockEc2APIHelper.EXPECT().GetInstanceNetworkInterface(&instanceID).Return(nwInterfaces, nil)

	// Capacity is 4 and already present 3, so remaining capacity = 4-3=1
	expectedENIDetails1 := createENIDetails(eniID1, 1)
	// Capacity is 4 and already present 1, so remaining capacity = 4-1=3
	expectedENIDetails2 := createENIDetails(eniID2, 3)

	ipV4Resource, err := manager.InitResources(mockEc2APIHelper)

	assert.NoError(t, err)
	// Assert all the IPs are returned
	assert.Equal(t, []string{ip1WithMask, ip2WithMask, ip3WithMask}, ipV4Resource.PrivateIPv4Addresses)
	// Assert all the prefixes are returned
	assert.Equal(t, []string{prefix1}, ipV4Resource.IPv4Prefixes)
	// Assert ENIs are added to the list of available ENIs
	assert.Equal(t, []*eni{expectedENIDetails1, expectedENIDetails2}, manager.attachedENIs)
	// Assert the ENI to IP mapping is as expected
	assert.True(t, reflect.DeepEqual(map[string]*eni{
		ip1: expectedENIDetails1, ip2: expectedENIDetails1,
		ip3: expectedENIDetails2, prefix1: expectedENIDetails1,
	}, manager.resourceToENIMap))
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

// TestEni_InitResources_Unsupported_Type_Error tests that error is returned if the instance type is not supported
func TestEni_InitResources_Unsupported_Type_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)
	dummyType := "dummy.large"
	mockInstance.EXPECT().InstanceID().Return(instanceID)
	mockInstance.EXPECT().Type().Return(dummyType)
	mockEc2APIHelper.EXPECT().GetInstanceNetworkInterface(&instanceID).Return(nwInterfaces, nil)

	_, err := manager.InitResources(mockEc2APIHelper)

	assert.Error(t, mockError, err)
	assert.ErrorIs(t, err, utils.ErrNotFound)
}

// TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromSingleENI tests IPs are created using a single ENI when it has the desired
// capacity
func TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromSingleENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	manager.attachedENIs = []*eni{createENIDetails(eniID1, 2)}

	mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID1, config.ResourceTypeIPv4Address, 2).Return([]string{ip1, ip2}, nil)
	mockInstance.EXPECT().Name().Return(instanceName)
	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(2)

	ips, err := manager.CreateIPV4Resource(2, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Equal(t, []string{ip1WithMask, ip2WithMask}, ips)
	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
}

// TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromSingleENI tests prefixes are created using a single ENI when it has the desired
// capacity
func TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromSingleENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	manager.attachedENIs = []*eni{createENIDetails(eniID1, 2)}

	mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID1, config.ResourceTypeIPv4Prefix, 2).Return([]string{prefix1, prefix2}, nil)
	mockInstance.EXPECT().Name().Return(instanceName)

	prefixes, err := manager.CreateIPV4Resource(2, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Equal(t, []string{prefix1, prefix2}, prefixes)
	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
}

// TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromSingleENIFail test that if some ip creation fails, the ones that succeeded
// are returned
func TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromSingleENIFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID2, 1)
	manager.attachedENIs = []*eni{existingENI}

	mockInstance.EXPECT().Name().Return(instanceName)
	mockInstance.EXPECT().SubnetMask().Return(subnetMask)

	// only assign 1 since remaining capacity is 1
	mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID2, config.ResourceTypeIPv4Address, 1).Return([]string{ip6}, nil)

	ips, err := manager.CreateIPV4Resource(2, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)

	assert.Error(t, mockError, err)
	assert.Equal(t, []string{ip6WithMask}, ips)
	assert.Equal(t, []*eni{existingENI}, manager.attachedENIs)
	assert.Equal(t, map[string]*eni{ip6: existingENI}, manager.resourceToENIMap)
	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
}

// TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromSingleENIFail test that if some prefix creation fails, the ones that
// succeeded are returned
func TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromSingleENIFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID3, 1)
	manager.attachedENIs = []*eni{existingENI}

	mockInstance.EXPECT().Name().Return(instanceName)

	// only assign 1 since remaining capacity is 1
	mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID3, config.ResourceTypeIPv4Prefix, 1).Return([]string{prefix1}, nil)

	prefixes, err := manager.CreateIPV4Resource(2, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)

	assert.Error(t, mockError, err)
	assert.Equal(t, []string{prefix1}, prefixes)
	assert.Equal(t, []*eni{existingENI}, manager.attachedENIs)
	assert.Equal(t, map[string]*eni{prefix1: existingENI}, manager.resourceToENIMap)
	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
}

// TestEniManager_CreateIPV4Resource_TypeIPV4Address_AssignEmpty test that if resource assign returns empty and no err, return error
// so that caller can retry
func TestEniManager_CreateIPV4Resource_TypeIPV4Address_AssignEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID2, 1)
	manager.attachedENIs = []*eni{existingENI}

	mockInstance.EXPECT().Name().Return(instanceName)

	// only assign 1 since remaining capacity is 1, note it returns nil assigned and nil error
	mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID2, config.ResourceTypeIPv4Address, 1).Return(nil, nil)

	// should return error about failing to assign required resources
	ips, err := manager.CreateIPV4Resource(2, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)

	assert.Error(t, mockError, err)
	assert.Nil(t, ips)
	assert.Equal(t, []*eni{existingENI}, manager.attachedENIs)
	assert.Equal(t, map[string]*eni{}, manager.resourceToENIMap)
	assert.Equal(t, 1, manager.attachedENIs[0].remainingCapacity)
}

// TODO: Uncomment once multi-ENI is supported for Windows
//// TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromMultipleENI tests IPs are assigned from multiple different ENIs
//func TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromMultipleENI(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)
//
//	manager.attachedENIs = []*eni{createENIDetails(eniID1, 1),
//		createENIDetails(eniID2, 3)}
//
//	gomock.InOrder(
//		mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID1, config.ResourceTypeIPv4Address, 1).Return([]string{ip1}, nil),
//		mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID2, config.ResourceTypeIPv4Address, 1).Return([]string{ip2}, nil),
//	)
//
//	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
//	mockInstance.EXPECT().Name().Return(instanceName)
//	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(2)
//
//	ips, err := manager.CreateIPV4Resource(2, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)
//
//	assert.NoError(t, err)
//	// Assert returned Ips are as expected
//	assert.Equal(t, []string{ip1WithMask, ip2WithMask}, ips)
//	// Assert the remaining capacity for the eni manager is reduced
//	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
//	assert.Equal(t, 2, manager.attachedENIs[1].remainingCapacity)
//	// Assert the mapping from IP to ENI  is created
//	assert.Equal(t, manager.attachedENIs[0], manager.resourceToENIMap[ip1])
//	assert.Equal(t, manager.attachedENIs[1], manager.resourceToENIMap[ip2])
//}
//
//// TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromMultipleENI tests prefixes are assigned from multiple different ENIs
//func TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromMultipleENI(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)
//
//	manager.attachedENIs = []*eni{createENIDetails(eniID1, 1),
//		createENIDetails(eniID2, 3)}
//
//	gomock.InOrder(
//		mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID1, config.ResourceTypeIPv4Prefix, 1).Return([]string{prefix1}, nil),
//		mockEc2APIHelper.EXPECT().AssignIPv4ResourcesAndWaitTillReady(eniID2, config.ResourceTypeIPv4Prefix, 1).Return([]string{prefix2}, nil),
//	)
//
//	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
//	mockInstance.EXPECT().Name().Return(instanceName)
//
//	prefixes, err := manager.CreateIPV4Resource(2, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)
//
//	assert.NoError(t, err)
//	// Assert returned prefixes are as expected
//	assert.Equal(t, []string{prefix1, prefix2}, prefixes)
//	// Assert the remaining capacity for the eni manager is reduced
//	assert.Equal(t, 0, manager.attachedENIs[0].remainingCapacity)
//	assert.Equal(t, 2, manager.attachedENIs[1].remainingCapacity)
//	// Assert the mapping from resource to ENI  is created
//	assert.Equal(t, manager.attachedENIs[0], manager.resourceToENIMap[prefix1])
//	assert.Equal(t, manager.attachedENIs[1], manager.resourceToENIMap[prefix2])
//}
//
//// TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromNewENI tests if existing ENIs cannot supply new IP, IPs are allocated from a new
//// ENI
//func TestEniManager_CreateIPV4Resource_TypeIPV4Address_FromNewENI(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)
//
//	existingENI := createENIDetails(eniID1, 0)
//	manager.attachedENIs = []*eni{existingENI}
//
//	mockInstance.EXPECT().Name().Return(instanceName)
//	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
//	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(2)
//	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(2)
//	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(2)
//	mockInstance.EXPECT().SubnetMask().Return(subnetMask).Times(4)
//	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(instanceSG).Times(2)
//
//	gomock.InOrder(
//		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
//			&ENIDescription, nil, ipCountFor3).Return(networkInterface1, nil),
//		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
//			&ENIDescription, nil, ipCountFor1).Return(networkInterface2, nil),
//	)
//
//	ips, err := manager.CreateIPV4Resource(4, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)
//
//	expectedNewENI1 := createENIDetails(*networkInterface1.NetworkInterfaceId, 0)
//	expectedNewENI2 := createENIDetails(*networkInterface2.NetworkInterfaceId, 2)
//
//	assert.NoError(t, err)
//	assert.Equal(t, []string{ip2WithMask, ip3WithMask, ip4WithMask, ip6WithMask}, ips)
//	assert.Equal(t, []*eni{existingENI, expectedNewENI1, expectedNewENI2}, manager.attachedENIs)
//	assert.Equal(t, map[string]*eni{ip2: expectedNewENI1, ip3: expectedNewENI1, ip4: expectedNewENI1, ip6: expectedNewENI2}, manager.resourceToENIMap)
//}
//
//// TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromNewENI tests if existing ENIs cannot supply new prefix, prefixes are allocated from a new
//// ENI
//func TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_FromNewENI(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)
//
//	existingENI := createENIDetails(eniID1, 0)
//	manager.attachedENIs = []*eni{existingENI}
//
//	mockInstance.EXPECT().Name().Return(instanceName)
//	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
//	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(1)
//	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(1)
//	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(1)
//	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(instanceSG).Times(1)
//
//	mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
//		&ENIDescription, nil, prefixCountFor1).Return(networkInterface3, nil)
//
//	prefixes, err := manager.CreateIPV4Resource(1, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)
//
//	expectedNewENI3 := createENIDetails(*networkInterface3.NetworkInterfaceId, 2)
//
//	assert.NoError(t, err)
//	assert.Equal(t, []string{prefix1}, prefixes)
//	assert.Equal(t, []*eni{existingENI, expectedNewENI3}, manager.attachedENIs)
//	assert.Equal(t, map[string]*eni{prefix1: expectedNewENI3}, manager.resourceToENIMap)
//}

// TODO: remove once multi-ENI is supported for Windows
// TestEniManager_CreateIPV4Resource_TypeIPV4Address_NoNewENI tests if existing ENIs cannot supply new IP address, no new ENI is created
func TestEniManager_CreateIPV4Resource_TypeIPV4Address_NoNewENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID1, 0)
	manager.attachedENIs = []*eni{existingENI}

	mockInstance.EXPECT().Name().Return(instanceName)

	ips, err := manager.CreateIPV4Resource(1, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)

	assert.Error(t, err)
	assert.Nil(t, ips)
}

// TODO: remove once multi-ENI is supported for Windows
// TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_NoNewENI tests if existing ENIs cannot supply new prefix, no new ENI is created
func TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_NoNewENI(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	existingENI := createENIDetails(eniID1, 0)
	manager.attachedENIs = []*eni{existingENI}

	mockInstance.EXPECT().Name().Return(instanceName)

	prefixes, err := manager.CreateIPV4Resource(1, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)

	assert.Error(t, err)
	assert.Nil(t, prefixes)
}

// TODO: Uncomment once multi-ENI is supported for Windows
//// TestEniManager_CreateIPV4Resource_TypeIPV4Address_InBetweenENIFail test that if some of the IP creation fails, the ones that succeeded
//// are returned
//func TestEniManager_CreateIPV4Resource_TypeIPV4Address_InBetweenENIFail(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)
//
//	existingENI := createENIDetails(eniID1, 0)
//	manager.attachedENIs = []*eni{existingENI}
//
//	mockInstance.EXPECT().Name().Return(instanceName)
//	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
//	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(2)
//	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(2)
//	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(2)
//	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(instanceSG).Times(2)
//
//	gomock.InOrder(
//		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
//			&ENIDescription, nil, ipCountFor3).Return(networkInterface1, nil),
//		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
//			&ENIDescription, nil, ipCountFor1).Return(nil, mockError),
//	)
//
//	ips, err := manager.CreateIPV4Resource(4, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)
//
//	expectedNewENI1 := createENIDetails(*networkInterface1.NetworkInterfaceId, 0)
//
//	assert.Error(t, mockError, err)
//	assert.Equal(t, []string{ip2, ip3, ip4}, ips)
//	assert.Equal(t, []*eni{existingENI, expectedNewENI1}, manager.attachedENIs)
//	assert.Equal(t, map[string]*eni{ip2: expectedNewENI1, ip3: expectedNewENI1, ip4: expectedNewENI1}, manager.resourceToENIMap)
//}
//
//// TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_InBetweenENIFail test that if some prefix creation fails, the ones that succeeded
//// are returned
//func TestEniManager_CreateIPV4Resource_TypeIPV4Prefix_InBetweenENIFail(t *testing.T) {
//	ctrl := gomock.NewController(t)
//	defer ctrl.Finish()
//
//	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)
//
//	existingENI := createENIDetails(eniID1, 0)
//	manager.attachedENIs = []*eni{existingENI}
//
//	mockInstance.EXPECT().Name().Return(instanceName)
//	mockInstance.EXPECT().Type().Return(instanceType).Times(2)
//	mockInstance.EXPECT().GetHighestUnusedDeviceIndex().Return(int64(3), nil).Times(2)
//	mockInstance.EXPECT().InstanceID().Return(instanceID).Times(2)
//	mockInstance.EXPECT().SubnetID().Return(subnetID).Times(2)
//	mockInstance.EXPECT().CurrentInstanceSecurityGroups().Return(instanceSG).Times(2)
//
//	gomock.InOrder(
//		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
//			&ENIDescription, nil, prefixCountFor3).Return(networkInterface4, nil),
//		mockEc2APIHelper.EXPECT().CreateAndAttachNetworkInterface(&instanceID, &subnetID, instanceSG, nil, aws.Int64(3),
//			&ENIDescription, nil, prefixCountFor1).Return(nil, mockError),
//	)
//
//	prefixes, err := manager.CreateIPV4Resource(4, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)
//
//	expectedNewENI3 := createENIDetails(*networkInterface4.NetworkInterfaceId, 0)
//
//	assert.Error(t, mockError, err)
//	assert.Equal(t, []string{prefix1, prefix2, prefix3}, prefixes)
//	assert.Equal(t, []*eni{existingENI, expectedNewENI3}, manager.attachedENIs)
//	assert.Equal(t, map[string]*eni{prefix1: expectedNewENI3, prefix2: expectedNewENI3, prefix3: expectedNewENI3}, manager.resourceToENIMap)
//}

// TestEniManager_DeleteIPV4Resource_TypeIPV4Address tests ips are un assigned and network interface without any secondary IP is deleted
func TestEniManager_DeleteIPV4Resource_TypeIPV4Address(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	eniDetails1 := createENIDetails(eniID1, 1)
	eniDetails2 := createENIDetails(eniID2, 1)

	manager.resourceToENIMap = map[string]*eni{ip1: eniDetails1, ip2: eniDetails1, ip3: eniDetails2, ip4: eniDetails2}
	manager.attachedENIs = []*eni{eniDetails1, eniDetails2}

	mockInstance.EXPECT().Name().Return(instanceName)
	mockInstance.EXPECT().Type().Return(instanceType).Times(1)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)
	// Unassign the IPs from interface 1 and 2
	mockEc2APIHelper.EXPECT().UnassignIPv4Resources(eniID1, config.ResourceTypeIPv4Address, []string{ip1}).Return(nil)
	mockEc2APIHelper.EXPECT().UnassignIPv4Resources(eniID2, config.ResourceTypeIPv4Address, []string{ip3, ip4}).Return(nil)
	// Delete the network interface 2 as it has no more secondary IP left
	mockEc2APIHelper.EXPECT().DeleteNetworkInterface(&eniID2).Return(nil)

	failedToDelete, err := manager.DeleteIPV4Resource([]string{ip3, ip1, ip4}, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Empty(t, failedToDelete)
	assert.Equal(t, []*eni{eniDetails1}, manager.attachedENIs)
	assert.NotContains(t, manager.resourceToENIMap, ip1)
	assert.NotContains(t, manager.resourceToENIMap, ip3)
}

// TestEniManager_DeleteIPV4Resource_TypeIPV4Prefix tests prefixes are un assigned
func TestEniManager_DeleteIPV4Resource_TypeIPV4Prefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	eniDetails1 := createENIDetails(eniID1, 1)

	manager.resourceToENIMap = map[string]*eni{ip1: eniDetails1, ip2: eniDetails1, prefix1: eniDetails1}
	manager.attachedENIs = []*eni{eniDetails1}

	mockInstance.EXPECT().Name().Return(instanceName)
	mockInstance.EXPECT().Type().Return(instanceType).Times(1)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)
	// Unassign the prefix from interface 1
	mockEc2APIHelper.EXPECT().UnassignIPv4Resources(eniID1, config.ResourceTypeIPv4Prefix, []string{prefix1}).Return(nil)

	failedToDelete, err := manager.DeleteIPV4Resource([]string{prefix1}, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)

	assert.NoError(t, err)
	assert.Empty(t, failedToDelete)
	assert.Equal(t, []*eni{eniDetails1}, manager.attachedENIs)
	assert.NotContains(t, manager.resourceToENIMap, prefix1)
}

// TestEniManager_DeleteIPV4Resource_TypeIPV4Address_SomeFail tests ips are un assigned and network interface without any secondary IP is deleted
func TestEniManager_DeleteIPV4Resource_TypeIPV4Address_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	eniDetails1 := createENIDetails(eniID1, 1)
	eniDetails2 := createENIDetails(eniID2, 2)

	manager.resourceToENIMap = map[string]*eni{ip1: eniDetails1, ip2: eniDetails1, ip3: eniDetails2}
	manager.attachedENIs = []*eni{eniDetails1, eniDetails2}

	mockInstance.EXPECT().Name().Return(instanceName)
	mockInstance.EXPECT().Type().Return(instanceType).Times(1)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)
	// Unassign the IPs from interface 1 and 2
	mockEc2APIHelper.EXPECT().UnassignIPv4Resources(eniID1, config.ResourceTypeIPv4Address, []string{ip1}).Return(mockError)
	mockEc2APIHelper.EXPECT().UnassignIPv4Resources(eniID2, config.ResourceTypeIPv4Address, []string{ip3}).Return(nil)
	// Delete the network interface 2 as it has no more secondary IP left
	mockEc2APIHelper.EXPECT().DeleteNetworkInterface(&eniID2).Return(nil)

	failedToDelete, err := manager.DeleteIPV4Resource([]string{ip1, ip3}, config.ResourceTypeIPv4Address, mockEc2APIHelper, log)

	assert.NotNil(t, err)
	assert.Equal(t, []string{ip1}, failedToDelete)
	assert.Equal(t, []*eni{eniDetails1}, manager.attachedENIs)
	assert.Contains(t, manager.resourceToENIMap, ip1)
	assert.NotContains(t, manager.resourceToENIMap, ip3)
}

// TestEniManager_DeleteIPV4Resource_TypeIPV4Prefix_SomeFail tests prefixes are un assigned with errors
func TestEniManager_DeleteIPV4Resource_TypeIPV4Prefix_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockInstance, mockEc2APIHelper := getMockManager(ctrl)

	eniDetails1 := createENIDetails(eniID1, 1)

	manager.resourceToENIMap = map[string]*eni{prefix1: eniDetails1}
	manager.attachedENIs = []*eni{eniDetails1}

	mockInstance.EXPECT().Name().Return(instanceName)
	mockInstance.EXPECT().Type().Return(instanceType).Times(1)
	mockInstance.EXPECT().PrimaryNetworkInterfaceID().Return(eniID1)

	mockEc2APIHelper.EXPECT().UnassignIPv4Resources(eniID1, config.ResourceTypeIPv4Prefix, []string{prefix1}).Return(mockError)

	failedToDelete, err := manager.DeleteIPV4Resource([]string{prefix1}, config.ResourceTypeIPv4Prefix, mockEc2APIHelper, log)

	assert.NotNil(t, err)
	assert.Equal(t, []string{prefix1}, failedToDelete)
	assert.Equal(t, []*eni{eniDetails1}, manager.attachedENIs)
	assert.Contains(t, manager.resourceToENIMap, prefix1)
}

func TestEniManager_groupResourcesPerENI(t *testing.T) {
	eni1 := &eni{eniID: eniID1}
	eni2 := &eni{eniID: eniID2}
	attachedENIs := []*eni{eni1, eni2}

	resourcesToENIMap := map[string]*eni{}

	resourcesToENIMap[ip1] = eni1
	resourcesToENIMap[ip2] = eni1
	resourcesToENIMap[ip3] = eni1
	resourcesToENIMap[ip4] = eni2
	resourcesToENIMap[ip5] = eni2
	resourcesToENIMap[prefix1] = eni1

	eniManager := eniManager{resourceToENIMap: resourcesToENIMap, attachedENIs: attachedENIs}

	groupedResources := eniManager.groupResourcesPerENI([]string{ip1, ip4, ip3, prefix1})
	assert.Equal(t, map[*eni][]string{eni1: {ip1, ip3, prefix1}, eni2: {ip4}}, groupedResources)
}

// TODO: Add more test cases
