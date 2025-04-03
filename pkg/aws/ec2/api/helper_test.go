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

package api

import (
	"fmt"
	"testing"
	"time"

	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	clusterName = "cluster-name"
	// instance id of EC2 worker node
	instanceId   = "i-00000000000000000"
	instanceType = ec2types.InstanceTypeM5Xlarge
	subnetId     = "subnet-00000000000000000"

	// network interface ids
	trunkInterfaceId   = "eni-00000000000000000"
	branchInterfaceId  = "eni-00000000000000001"
	branchInterfaceId2 = "eni-00000000000000002"
	attachmentId       = "attach-000000000000000"
	eniID              = "eni-00000000000000003"
	deviceIndex        = int32(0)

	ipAddress1 = "192.168.1.1"
	ipAddress2 = "192.168.1.2"

	ipPrefix1 = "192.168.1.0/28"
	ipPrefix2 = "192.168.2.0/28"

	// branch to trunk association id
	branchAssociationId = "association-00000000000000"
	vlanId              = 0

	// Security groups for the network interfaces
	securityGroup1 = "sg-00000000000000001"
	securityGroup2 = "sg-00000000000000002"
	securityGroups = []string{securityGroup1, securityGroup2}

	tags = []ec2types.Tag{
		{
			Key:   aws.String("mock-key"),
			Value: aws.String("mock-val"),
		},
	}

	eniDescription           = "mock description of eni"
	eniDescriptionWithPrefix = "aws-k8s-" + eniDescription
	errMock                  = fmt.Errorf("failed to do ec2 call")
)

var (
	associateTrunkInterfaceInput = &ec2.AssociateTrunkInterfaceInput{
		BranchInterfaceId: &branchInterfaceId,
		TrunkInterfaceId:  &trunkInterfaceId,
		VlanId:            aws.Int32(int32(vlanId)),
	}

	associateTrunkInterfaceOutput = &ec2.AssociateTrunkInterfaceOutput{
		InterfaceAssociation: &ec2types.TrunkInterfaceAssociation{AssociationId: &branchAssociationId},
	}

	defaultClusterNameTag = ec2types.Tag{
		Key:   aws.String(fmt.Sprintf(config.ClusterNameTagKeyFormat, clusterName)),
		Value: aws.String(config.ClusterNameTagValue),
	}

	createNetworkInterfaceInput = &ec2.CreateNetworkInterfaceInput{
		Description: &eniDescriptionWithPrefix,
		Groups:      securityGroups,
		SubnetId:    &subnetId,
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeNetworkInterface,
				Tags:         append(tags, defaultControllerTag, defaultClusterNameTag),
			},
		},
	}

	createNetworkInterfaceOutput = &ec2.CreateNetworkInterfaceOutput{
		NetworkInterface: &ec2types.NetworkInterface{NetworkInterfaceId: &branchInterfaceId},
	}

	deleteNetworkInterfaceInput = &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: &branchInterfaceId,
	}

	describeSubnetInput = &ec2.DescribeSubnetsInput{
		SubnetIds: []string{subnetId},
	}

	describeSubnetOutput = &ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{{SubnetId: &subnetId}}}

	describeNetworkInterfaceInputUsingInstanceId = &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceId},
	}

	describeNetworkInterfaceOutputUsingInstanceId = &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{
				Instances: []ec2types.Instance{
					{
						NetworkInterfaces: []ec2types.InstanceNetworkInterface{
							{
								NetworkInterfaceId: &branchInterfaceId,
								Status:             ec2types.NetworkInterfaceStatusDetaching,
							},
						},
					},
				},
			},
		},
	}

	describeNetworkInterfaceInputUsingInterfaceId = &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{branchInterfaceId, branchInterfaceId2},
	}

	describeNetworkInterfaceOutputUsingInterfaceId = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []ec2types.NetworkInterface{
			{NetworkInterfaceId: &branchInterfaceId, InterfaceType: ec2types.NetworkInterfaceTypeInterface},
			{NetworkInterfaceId: &trunkInterfaceId, InterfaceType: ec2types.NetworkInterfaceTypeTrunk},
		},
	}

	describeNetworkInterfaceInputUsingOneInterfaceId = &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{branchInterfaceId},
	}

	describeNetworkInterfaceOutputUsingOneInterfaceId = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []ec2types.NetworkInterface{
			{
				NetworkInterfaceId: &branchInterfaceId,
				InterfaceType:      ec2types.NetworkInterfaceTypeInterface,
				Attachment: &ec2types.NetworkInterfaceAttachment{
					Status: ec2types.AttachmentStatusDetached,
				},
			},
		},
	}

	branchTag1 = []ec2types.Tag{{
		Key:   aws.String("tag-key-1"),
		Value: aws.String("tag-val-1"),
	}}
	branchTag2 = []ec2types.Tag{{
		Key:   aws.String("tag-key-2"),
		Value: aws.String("tag-val-2"),
	}}

	networkInterface1 = ec2types.NetworkInterface{
		NetworkInterfaceId: &branchInterfaceId,
		TagSet:             branchTag1,
	}
	networkInterface2 = ec2types.NetworkInterface{
		NetworkInterfaceId: &branchInterfaceId2,
		TagSet:             branchTag2,
	}

	tokenID = "token"

	describeTrunkInterfaceInput = &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			{
				Name:   aws.String("tag:" + config.TrunkENIIDTag),
				Values: []string{trunkInterfaceId},
			},
			{
				Name:   aws.String("subnet-id"),
				Values: []string{subnetId},
			},
		},
	}

	describeTrunkInterfaceOutput = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []ec2types.NetworkInterface{networkInterface1, networkInterface2},
	}

	describeTrunkInterfaceAssociationsInput = &ec2.DescribeTrunkInterfaceAssociationsInput{
		Filters: []ec2types.Filter{{
			Name:   aws.String("trunk-interface-association.trunk-interface-id"),
			Values: []string{trunkInterfaceId},
		}},
	}

	describeTrunkInterfaceAssociationsOutput = &ec2.DescribeTrunkInterfaceAssociationsOutput{InterfaceAssociations: []ec2types.TrunkInterfaceAssociation{
		{
			BranchInterfaceId: &branchInterfaceId,
			TrunkInterfaceId:  &trunkInterfaceId,
			VlanId:            aws.Int32(int32(vlanId)),
		},
	}}

	modifyNetworkInterfaceAttributeInput = &ec2.ModifyNetworkInterfaceAttributeInput{
		Attachment: &ec2types.NetworkInterfaceAttachmentChanges{
			AttachmentId:        &attachmentId,
			DeleteOnTermination: aws.Bool(true),
		},
		NetworkInterfaceId: &branchInterfaceId,
	}

	attachNetworkInterfaceInput = &ec2.AttachNetworkInterfaceInput{
		InstanceId:         &instanceId,
		NetworkInterfaceId: &branchInterfaceId,
		DeviceIndex:        &deviceIndex,
	}

	attachNetworkInterfaceOutput = &ec2.AttachNetworkInterfaceOutput{AttachmentId: &attachmentId}

	detachNetworkInterfaceInput = &ec2.DetachNetworkInterfaceInput{
		AttachmentId: &attachmentId,
	}

	describeInstanceInput = &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceId},
	}

	describeInstanceOutput = &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{{
			Instances: []ec2types.Instance{{
				InstanceId:   &instanceId,
				InstanceType: instanceType,
				SubnetId:     &subnetId,
			}},
		}},
	}

	assignPrivateIPInput = &ec2.AssignPrivateIpAddressesInput{
		NetworkInterfaceId:             &eniID,
		SecondaryPrivateIpAddressCount: aws.Int32(int32(2)),
	}

	assignPrivateIPOutput = &ec2.AssignPrivateIpAddressesOutput{
		AssignedPrivateIpAddresses: []ec2types.AssignedPrivateIpAddress{
			{
				PrivateIpAddress: &ipAddress1,
			},
			{
				PrivateIpAddress: &ipAddress2,
			},
		},
	}

	assignPrivateIPInputPrefix = &ec2.AssignPrivateIpAddressesInput{
		NetworkInterfaceId: &eniID,
		Ipv4PrefixCount:    aws.Int32(int32(2)),
	}

	assignPrivateIPOutputPrefix = &ec2.AssignPrivateIpAddressesOutput{
		AssignedIpv4Prefixes: []ec2types.Ipv4PrefixSpecification{
			{
				Ipv4Prefix: &ipPrefix1,
			},
			{
				Ipv4Prefix: &ipPrefix2,
			},
		},
	}

	describeNetworkInterfaceInput = &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	}

	describeNetworkInterfaceOutput = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []ec2types.NetworkInterface{
			{
				PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
					{PrivateIpAddress: &ipAddress1},
					{PrivateIpAddress: &ipAddress2},
				},
			},
		},
	}

	describeNetworkInterfaceInputPrefix = &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	}

	describeNetworkInterfaceOutputPrefix = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []ec2types.NetworkInterface{
			{
				Ipv4Prefixes: []ec2types.Ipv4PrefixSpecification{
					{Ipv4Prefix: &ipPrefix1},
					{Ipv4Prefix: &ipPrefix2},
				},
			},
		},
	}

	createNetworkInterfacePermissionInputBranch = &ec2.CreateNetworkInterfacePermissionInput{
		NetworkInterfaceId: &branchInterfaceId,
		Permission:         ec2types.InterfacePermissionTypeInstanceAttach,
	}

	createNetworkInterfacePermissionInputTrunk = &ec2.CreateNetworkInterfacePermissionInput{
		NetworkInterfaceId: &trunkInterfaceId,
		Permission:         ec2types.InterfacePermissionTypeInstanceAttach,
	}

	maxRetryOnError = 3
)

// getMockWrapper returns the Mock EC2Wrapper along with the EC2APIHelper with mock EC2Wrapper set up
func getMockWrapper(ctrl *gomock.Controller) (EC2APIHelper, *mock_api.MockEC2Wrapper) {
	defaultBackOff = wait.Backoff{
		Duration: time.Millisecond,
		Factor:   1.0,
		Jitter:   0.1,
		Steps:    maxRetryOnError,
		Cap:      time.Second * 10,
	}
	waitForENIAttachment = wait.Backoff{
		Duration: time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    maxRetryOnError,
		Cap:      time.Second * 30,
	}
	waitForIPAttachment = wait.Backoff{
		Duration: time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    maxRetryOnError,
		Cap:      time.Second * 2,
	}

	mockWrapper := mock_api.NewMockEC2Wrapper(ctrl)
	ec2ApiHelper := NewEC2APIHelper(mockWrapper, clusterName)

	return ec2ApiHelper, mockWrapper
}

// TestEc2APIHelper_AssociateBranchToTrunk tests the associate branch to trunk API call
func TestEc2APIHelper_AssociateBranchToTrunk(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	// Return response with association id
	mockWrapper.EXPECT().AssociateTrunkInterface(associateTrunkInterfaceInput).
		Return(associateTrunkInterfaceOutput, nil)
	mockWrapper.EXPECT().CreateNetworkInterfacePermission(createNetworkInterfacePermissionInputBranch).
		Return(nil, nil)

	_, err := ec2ApiHelper.AssociateBranchToTrunk(&trunkInterfaceId, &branchInterfaceId, vlanId)

	assert.NoError(t, err)
}

// TestEc2APIHelper_AssociateBranchToTrunk_AssociationIdMissing tests that the Associate call returns error
// if the association id is missing from the ec2 output
func TestEc2APIHelper_AssociateBranchToTrunk_AssociationIdMissing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	// Return empty association response
	mockWrapper.EXPECT().AssociateTrunkInterface(associateTrunkInterfaceInput).
		Return(&ec2.AssociateTrunkInterfaceOutput{}, nil)
	mockWrapper.EXPECT().CreateNetworkInterfacePermission(createNetworkInterfacePermissionInputBranch).
		Return(nil, nil)

	_, err := ec2ApiHelper.AssociateBranchToTrunk(&trunkInterfaceId, &branchInterfaceId, vlanId)

	assert.NotNil(t, err)
}

// TestEc2APIHelper_AssociateBranchToTrunk_Error tests that error is returned if ec2 call returns an error
func TestEc2APIHelper_AssociateBranchToTrunk_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	// Return empty association response
	mockWrapper.EXPECT().AssociateTrunkInterface(associateTrunkInterfaceInput).Return(nil, errMock)
	mockWrapper.EXPECT().CreateNetworkInterfacePermission(createNetworkInterfacePermissionInputBranch).
		Return(nil, nil)

	_, err := ec2ApiHelper.AssociateBranchToTrunk(&trunkInterfaceId, &branchInterfaceId, vlanId)

	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_CreateNetworkInterface_NoSecondaryIP tests network interface creation when no secondary IPs are
// created
func TestEc2APIHelper_CreateNetworkInterface_NoSecondaryIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(createNetworkInterfaceOutput, nil)
	count := &config.IPResourceCount{SecondaryIPv4Count: 0, IPv4PrefixCount: 0}
	output, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, tags, count, nil)

	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *output.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateNetworkInterface_WithSecondaryIP tests network interface creation with secondary IP address
func TestEc2APIHelper_CreateNetworkInterface_WithSecondaryIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	ipCount := int32(5)

	createNetworkInterfaceInput.SecondaryPrivateIpAddressCount = &ipCount

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).
		Return(createNetworkInterfaceOutput, nil)

	count := &config.IPResourceCount{SecondaryIPv4Count: 5, IPv4PrefixCount: 0}
	output, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, tags, count, nil)

	createNetworkInterfaceInput.SecondaryPrivateIpAddressCount = nil

	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *output.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateNetworkInterface_WithPrefixCount tests network interface creation with prefix count
func TestEc2APIHelper_CreateNetworkInterface_WithPrefixCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	prefixCount := int32(5)

	createNetworkInterfaceInput.Ipv4PrefixCount = &prefixCount

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).
		Return(createNetworkInterfaceOutput, nil)

	count := &config.IPResourceCount{SecondaryIPv4Count: 0, IPv4PrefixCount: 5}
	output, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, tags, count, nil)

	createNetworkInterfaceInput.Ipv4PrefixCount = nil

	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *output.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateNetworkInterface_WithSecondaryIPAndPrefixCount_Error tests network interface creation with secondary IP count and prefix count fails
func TestEc2APIHelper_CreateNetworkInterface_WithSecondaryIPAndPrefixCount_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, _ := getMockWrapper(ctrl)

	ipCount := int32(5)

	createNetworkInterfaceInput.SecondaryPrivateIpAddressCount = &ipCount
	createNetworkInterfaceInput.Ipv4PrefixCount = &ipCount

	count := &config.IPResourceCount{SecondaryIPv4Count: 5, IPv4PrefixCount: 5}
	_, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, tags, count, nil)

	createNetworkInterfaceInput.SecondaryPrivateIpAddressCount = nil
	createNetworkInterfaceInput.Ipv4PrefixCount = nil

	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_CreateNetworkInterface_TypeTrunk tests network interface creation with the interface type trunk
func TestEc2APIHelper_CreateNetworkInterface_TypeTrunk(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	interfaceTypeTrunk := ec2types.NetworkInterfaceCreationTypeTrunk
	interfaceTypeTrunkString := string(interfaceTypeTrunk)

	createNetworkInterfaceInput.InterfaceType = interfaceTypeTrunk
	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(
		&ec2.CreateNetworkInterfaceOutput{
			NetworkInterface: &ec2types.NetworkInterface{NetworkInterfaceId: &trunkInterfaceId},
		}, nil)
	mockWrapper.EXPECT().CreateNetworkInterfacePermission(createNetworkInterfacePermissionInputTrunk).
		Return(nil, nil)

	output, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, tags, nil, &interfaceTypeTrunkString)

	createNetworkInterfaceInput.InterfaceType = ""

	assert.NoError(t, err)
	assert.Equal(t, trunkInterfaceId, *output.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateNetworkInterface_EmptyResponse tests error is returned if empty response is returned from
// ec2 api call
func TestEc2APIHelper_CreateNetworkInterface_EmptyResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(nil, nil)

	_, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, tags, nil, nil)

	assert.NotNil(t, err)
}

// TestEc2APIHelper_CreateNetworkInterface_Error tests that error is returned if ec2 api call returns an error
func TestEc2APIHelper_CreateNetworkInterface_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(nil, errMock)

	_, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, tags, nil, nil)

	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_DeleteNetworkInterface tests delete network interface returns correct response in case of valid
// request
func TestEc2APIHelper_DeleteNetworkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, nil)

	err := ec2ApiHelper.DeleteNetworkInterface(&branchInterfaceId)
	assert.NoError(t, err)
}

// TestEc2APIHelper_DeleteNetworkInterface_Error tests that delete is tried multiple times in case of error form
// ec2 api call
func TestEc2APIHelper_DeleteNetworkInterface_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, errMock).Times(maxRetryOnError)

	err := ec2ApiHelper.DeleteNetworkInterface(&branchInterfaceId)
	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_DeleteNetworkInterface_ErrorThenSuccess tests that if delete network call fails initially and
// succeeds subsequently then no error is returned
func TestEc2APIHelper_DeleteNetworkInterface_ErrorThenSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	gomock.InOrder(
		mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, errMock).Times(2),
		mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, nil).Times(1),
	)

	err := ec2ApiHelper.DeleteNetworkInterface(&branchInterfaceId)
	assert.NoError(t, err)
}

// TestEc2APIHelper_GetSubnet tests that get subnet call returns the expected response with no error
func TestEc2APIHelper_GetSubnet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)
	mockWrapper.EXPECT().DescribeSubnets(describeSubnetInput).Return(describeSubnetOutput, nil)

	subnet, err := ec2ApiHelper.GetSubnet(&subnetId)
	assert.NoError(t, err)
	assert.Equal(t, subnetId, *subnet.SubnetId)
}

// TestEc2APIHelper_GetSubnet_NoSubnetReturned tests that in case the ec2 api call response returns empty response
// then an error is returned
func TestEc2APIHelper_GetSubnet_NoSubnetReturned(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)
	mockWrapper.EXPECT().DescribeSubnets(describeSubnetInput).Return(&ec2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{}}, nil)

	_, err := ec2ApiHelper.GetSubnet(&subnetId)
	assert.NotNil(t, err)
}

// TestEc2APIHelper_GetSubnet_Error tests that the error form ec2 api call is propagated to the caller.
func TestEc2APIHelper_GetSubnet_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)
	mockWrapper.EXPECT().DescribeSubnets(describeSubnetInput).Return(nil, errMock)

	_, err := ec2ApiHelper.GetSubnet(&subnetId)
	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_GetNetworkInterfaceOfInstance tests that describe network interface returns no errors
// under valid input
func TestEc2APIHelper_GetNetworkInterfaceOfInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeInstances(describeNetworkInterfaceInputUsingInstanceId).
		Return(describeNetworkInterfaceOutputUsingInstanceId, nil)

	nwInterfaces, err := ec2ApiHelper.GetInstanceNetworkInterface(&instanceId)
	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *nwInterfaces[0].NetworkInterfaceId)
}

// TestEc2APIHelper_GetNetworkInterfaceOfInstance_Error tests that error is propagated back to the caller if
// the Ec2 API call returns an error
func TestEc2APIHelper_GetNetworkInterfaceOfInstance_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeInstances(describeNetworkInterfaceInputUsingInstanceId).Return(nil, errMock)

	_, err := ec2ApiHelper.GetInstanceNetworkInterface(&instanceId)
	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_DescribeNetworkInterfaces tests describe network interface call works as expected under
// no errors under valid input
func TestEc2APIHelper_DescribeNetworkInterfaces(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingInterfaceId).
		Return(describeNetworkInterfaceOutputUsingInterfaceId, nil)

	nwInterfaces, err := ec2ApiHelper.DescribeNetworkInterfaces([]string{branchInterfaceId, branchInterfaceId2})
	assert.NoError(t, err)
	assert.Equal(t, 2, len(nwInterfaces))
}

// TestEc2APIHelper_DescribeNetworkInterfaces_Error tests the describe network interface call returns an error if
// the ec2 api call returns an error
func TestEc2APIHelper_DescribeNetworkInterfaces_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingInterfaceId).
		Return(nil, errMock)

	_, err := ec2ApiHelper.DescribeNetworkInterfaces([]string{branchInterfaceId, branchInterfaceId2})
	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_DescribeTrunkInterfaceAssociation tests that the describe trunk interface association returns
// no errors under valid input
func TestEc2APIHelper_DescribeTrunkInterfaceAssociation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeTrunkInterfaceAssociations(describeTrunkInterfaceAssociationsInput).
		Return(describeTrunkInterfaceAssociationsOutput, nil)

	output, err := ec2ApiHelper.DescribeTrunkInterfaceAssociation(&trunkInterfaceId)
	assert.NoError(t, err)
	assert.Equal(t, describeTrunkInterfaceAssociationsOutput.InterfaceAssociations, output)
}

// TestEc2APIHelper_DescribeTrunkInterfaceAssociation_EmptyResponse tests that describe trunk association nil response
// in case output is nil as the ec2 returns empty response when trunk has no branches
func TestEc2APIHelper_DescribeTrunkInterfaceAssociation_EmptyResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeTrunkInterfaceAssociations(describeTrunkInterfaceAssociationsInput).
		Return(nil, nil)

	_, err := ec2ApiHelper.DescribeTrunkInterfaceAssociation(&trunkInterfaceId)
	assert.NoError(t, err)
}

// TestEc2APIHelper_DescribeTrunkInterfaceAssociation_Error tests that error is propagated back in case ec2 api call
// returns an error
func TestEc2APIHelper_DescribeTrunkInterfaceAssociation_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeTrunkInterfaceAssociations(describeTrunkInterfaceAssociationsInput).
		Return(nil, errMock)

	_, err := ec2ApiHelper.DescribeTrunkInterfaceAssociation(&trunkInterfaceId)
	assert.Error(t, errMock, err)
}

func TestEc2APIHelper_CreateAndAttachNetworkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	oldStatus := describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status
	describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status = ec2types.AttachmentStatusAttached

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(createNetworkInterfaceOutput, nil)
	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).Return(attachNetworkInterfaceOutput, nil)
	mockWrapper.EXPECT().ModifyNetworkInterfaceAttribute(modifyNetworkInterfaceAttributeInput).Return(nil, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
		Return(describeNetworkInterfaceOutputUsingOneInterfaceId, nil)

	nwInterface, err := ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceId, &subnetId, securityGroups, tags,
		&deviceIndex, &eniDescription, nil, nil)

	// Clean up
	describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status = oldStatus

	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *nwInterface.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateAndAttachNetworkInterface_DeleteOnAttachFailed tests that delete is invoked if the attach
// call fails
func TestEc2APIHelper_CreateAndAttachNetworkInterface_DeleteOnAttachFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(createNetworkInterfaceOutput, nil)
	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).Return(attachNetworkInterfaceOutput, errMock)

	// Test delete is called
	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, nil)

	nwInterface, err := ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceId, &subnetId, securityGroups, tags,
		&deviceIndex, &eniDescription, nil, nil)

	assert.NotNil(t, err)
	assert.Nil(t, nwInterface)
}

// TestEc2APIHelper_CreateAndAttachNetworkInterface_DeleteOnSetTerminationFail tests that delete is invoked if the set
// deletion on termination fails
func TestEc2APIHelper_CreateAndAttachNetworkInterface_DeleteOnSetTerminationFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(createNetworkInterfaceOutput, nil)
	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).Return(attachNetworkInterfaceOutput, nil)
	mockWrapper.EXPECT().ModifyNetworkInterfaceAttribute(modifyNetworkInterfaceAttributeInput).Return(nil, errMock)

	// Test detach and delete is called
	mockWrapper.EXPECT().DetachNetworkInterface(detachNetworkInterfaceInput).Return(nil, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
		Return(describeNetworkInterfaceOutputUsingOneInterfaceId, nil)
	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, nil)

	nwInterface, err := ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceId, &subnetId, securityGroups, tags,
		&deviceIndex, &eniDescription, nil, nil)

	assert.NotNil(t, err)
	assert.Nil(t, nwInterface)
}

// TestEc2APIHelper_SetDeleteOnTermination tests that ec2 api call is made with the valid input
func TestEc2APIHelper_SetDeleteOnTermination(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().ModifyNetworkInterfaceAttribute(modifyNetworkInterfaceAttributeInput).
		Return(nil, nil)

	err := ec2ApiHelper.SetDeleteOnTermination(&attachmentId, &branchInterfaceId)
	assert.NoError(t, err)
}

// TestEc2APIHelper_SetDeleteOnTermination tests when ec2 api call return errors it is propagated to the caller
func TestEc2APIHelper_SetDeleteOnTermination_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().ModifyNetworkInterfaceAttribute(modifyNetworkInterfaceAttributeInput).
		Return(nil, errMock)

	err := ec2ApiHelper.SetDeleteOnTermination(&attachmentId, &branchInterfaceId)
	assert.Error(t, errMock, err)
}

// TestEC2APIHelper_AttachNetworkInterfaceToInstance no error is returned when valid inputs are passed
func TestEC2APIHelper_AttachNetworkInterfaceToInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).
		Return(attachNetworkInterfaceOutput, nil)

	id, err := ec2ApiHelper.AttachNetworkInterfaceToInstance(&instanceId, &branchInterfaceId, &deviceIndex)
	assert.NoError(t, err)
	assert.Equal(t, attachmentId, *id)
}

// TestEC2APIHelper_AttachNetworkInterfaceToInstance_NoAttachmentId tests error is returned when no attachment id is
// present
func TestEC2APIHelper_AttachNetworkInterfaceToInstance_NoAttachmentId(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).
		Return(&ec2.AttachNetworkInterfaceOutput{AttachmentId: nil}, nil)

	_, err := ec2ApiHelper.AttachNetworkInterfaceToInstance(&instanceId, &branchInterfaceId, &deviceIndex)
	assert.NotNil(t, err)
}

// TestEC2APIHelper_AttachNetworkInterfaceToInstance_Error tests if ec2 api call returns an error it's propagated to caller
func TestEC2APIHelper_AttachNetworkInterfaceToInstance_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).Return(nil, errMock)

	_, err := ec2ApiHelper.AttachNetworkInterfaceToInstance(&instanceId, &branchInterfaceId, &deviceIndex)
	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_DetachNetworkInterfaceFromInstance tests ec2 api call returns no error on valid input
func TestEc2APIHelper_DetachNetworkInterfaceFromInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DetachNetworkInterface(detachNetworkInterfaceInput).Return(nil, nil)

	err := ec2ApiHelper.DetachNetworkInterfaceFromInstance(&attachmentId)
	assert.NoError(t, err)
}

// TestEC2APIHelper_WaitForNetworkInterfaceStatusChange tests that the ec2 api call is retried till the status changes
// to the desired status
func TestEC2APIHelper_WaitForNetworkInterfaceStatusChange(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	statusAttached := "attached"

	gomock.InOrder(
		// Initially in detached state, must retry
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
			Return(describeNetworkInterfaceOutputUsingOneInterfaceId, nil).Times(2),
		// Status changed to attached state, must return
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
			Return(&ec2.DescribeNetworkInterfacesOutput{
				NetworkInterfaces: []ec2types.NetworkInterface{{Attachment: &ec2types.NetworkInterfaceAttachment{Status: ec2types.AttachmentStatusAttached}}},
			}, nil).Times(1),
	)

	err := ec2ApiHelper.WaitForNetworkInterfaceStatusChange(&branchInterfaceId, statusAttached)
	assert.NoError(t, err)
}

// TestEC2ADIHelper_WaitForNetworkInterfaceStatusChange_NonRetryableError tests call immediately returns in case of
// a non retryable error
func TestEC2ADIHelper_WaitForNetworkInterfaceStatusChange_NonRetryableError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	statusAvailable := "available"

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
		Return(nil, errMock)

	err := ec2ApiHelper.WaitForNetworkInterfaceStatusChange(&branchInterfaceId, statusAvailable)
	assert.Error(t, errMock, err)
}

// TestEc2APIHelper_DetachAndDeleteNetworkInterface tests the ec2 api calls are called in order with the desired input
func TestEc2APIHelper_DetachAndDeleteNetworkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	oldStatus := describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status
	describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status = ec2types.AttachmentStatusDetached

	mockWrapper.EXPECT().DetachNetworkInterface(detachNetworkInterfaceInput).Return(nil, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
		Return(describeNetworkInterfaceOutputUsingOneInterfaceId, nil)
	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, nil)

	err := ec2ApiHelper.DetachAndDeleteNetworkInterface(&attachmentId, &branchInterfaceId)
	assert.NoError(t, err)

	// clean up
	describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status = oldStatus
}

// TestEc2APIHelper_DetachAndDeleteNetworkInterface_Error tests the error is returned if any of the ec2 api call fails
func TestEc2APIHelper_DetachAndDeleteNetworkInterface_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DetachNetworkInterface(detachNetworkInterfaceInput).Return(nil, errMock)

	err := ec2ApiHelper.DetachAndDeleteNetworkInterface(&attachmentId, &branchInterfaceId)
	assert.Error(t, errMock, err)
}

// TestEC2APIHelper_GetInstanceDetails tests no error is returned on valid input
func TestEC2APIHelper_GetInstanceDetails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeInstances(describeInstanceInput).Return(describeInstanceOutput, nil)

	instance, err := ec2ApiHelper.GetInstanceDetails(&instanceId)

	assert.NoError(t, err)
	assert.Equal(t, instanceId, *instance.InstanceId)
	assert.Equal(t, subnetId, *instance.SubnetId)
}

// TestEC2APIHelper_GetInstanceDetails_InstanceNotFound tests if the response doesn't have the instance details then
// error is returned
func TestEC2APIHelper_GetInstanceDetails_InstanceNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeInstances(describeInstanceInput).
		Return(&ec2.DescribeInstancesOutput{Reservations: nil}, nil)

	_, err := ec2ApiHelper.GetInstanceDetails(&instanceId)
	assert.NotNil(t, err)
}

// TestEC2APIHelper_GetInstanceDetails_Error tests if error is returned from ec2 api call it is propagated to caller
func TestEC2APIHelper_GetInstanceDetails_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeInstances(describeInstanceInput).Return(nil, errMock)

	_, err := ec2ApiHelper.GetInstanceDetails(&instanceId)
	assert.Error(t, errMock, err)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address tests that once new IP addresses are assigned they are returned
// only when the IPs are attached to the instance
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInput).Return(assignPrivateIPOutput, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInput).Return(describeNetworkInterfaceOutput, nil)

	createdIPs, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Address, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ipAddress1, ipAddress2}, createdIPs)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix tests that once new IP prefixes are assigned they are returned
// only when the prefixes are attached to the instance
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInputPrefix).Return(assignPrivateIPOutputPrefix, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputPrefix).Return(describeNetworkInterfaceOutputPrefix, nil)

	createdPrefixes, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Prefix, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ipPrefix1, ipPrefix2}, createdPrefixes)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address_Error tests that error is returned if the assign private IP call
// fails
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInput).Return(nil, errMock)

	_, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Address, 2)

	assert.Error(t, errMock, err)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix_Error tests that error is returned if the assign private IP call
// fails
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInputPrefix).Return(nil, errMock)

	_, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Prefix, 2)

	assert.Error(t, errMock, err)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address_AttachedAfterSecondDescribe tests if the describe call is called
// till all the newly assigned ips are returned
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address_AttachedAfterSecondDescribe(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInput).Return(assignPrivateIPOutput, nil)
	gomock.InOrder(
		// First call returns just one ip address
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInput).Return(&ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
					{PrivateIpAddress: &ipAddress1},
				}},
			},
		}, nil),
		// Second call all created IPs returned
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInput).Return(describeNetworkInterfaceOutput, nil),
	)

	createdIPs, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Address, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ipAddress1, ipAddress2}, createdIPs)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix_AttachedAfterSecondDescribe tests if the describe call is called
// till all the newly assigned prefixes are returned
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix_AttachedAfterSecondDescribe(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInputPrefix).Return(assignPrivateIPOutputPrefix, nil)
	gomock.InOrder(
		// First call returns just one ip prefix
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputPrefix).Return(&ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{Ipv4Prefixes: []ec2types.Ipv4PrefixSpecification{
					{Ipv4Prefix: &ipPrefix1},
				}},
			},
		}, nil),
		// Second call all created prefixes returned
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputPrefix).Return(describeNetworkInterfaceOutputPrefix, nil),
	)

	createdPrefixes, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Prefix, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ipPrefix1, ipPrefix2}, createdPrefixes)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address_DescribeReturnsPartialResult returns the partially assigned IPs
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Address_DescribeReturnsPartialResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInput).Return(assignPrivateIPOutput, nil)
	gomock.InOrder(
		// First call returns just one ip address
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInput).Return(&ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{PrivateIpAddresses: []ec2types.NetworkInterfacePrivateIpAddress{
					{PrivateIpAddress: &ipAddress1},
				}},
			},
		}, nil).Times(maxRetryOnError),
	)

	createdIPs, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Address, 2)

	assert.NotNil(t, err)
	// Assert that even though 2 IPs were assigned, only 1 is returned because the describe call doesn't contain the second IP
	assert.Equal(t, []string{ipAddress1}, createdIPs)
}

// TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix_DescribeReturnsPartialResult returns the partially assigned IPs
func TestEC2APIHelper_AssignIPv4ResourcesAndWaitTillReady_TypeIPv4Prefix_DescribeReturnsPartialResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInputPrefix).Return(assignPrivateIPOutputPrefix, nil)
	gomock.InOrder(
		// First call returns just one ip address
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputPrefix).Return(&ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{Ipv4Prefixes: []ec2types.Ipv4PrefixSpecification{
					{Ipv4Prefix: &ipPrefix1},
				}},
			},
		}, nil).Times(maxRetryOnError),
	)

	createdPrefixes, err := ec2ApiHelper.AssignIPv4ResourcesAndWaitTillReady(eniID, config.ResourceTypeIPv4Prefix, 2)

	assert.NotNil(t, err)
	// Assert that even though 2 prefixes were assigned, only 1 is returned because the describe call doesn't contain the second prefix
	assert.Equal(t, []string{ipPrefix1}, createdPrefixes)
}

// TestEc2APIHelper_GetBranchNetworkInterface_PaginatedResults returns the branch interface when paginated results is returned
func TestEc2APIHelper_GetBranchNetworkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeTrunkInterfaceInput).Return(describeTrunkInterfaceOutput, nil)

	branchInterfaces, err := ec2ApiHelper.GetBranchNetworkInterface(&trunkInterfaceId, &subnetId)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []*ec2types.NetworkInterface{&networkInterface1, &networkInterface2}, branchInterfaces)
}
