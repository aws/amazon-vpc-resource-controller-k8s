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

package api

import (
	"fmt"
	"testing"
	"time"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/wait"
)

var (
	// instance id of EC2 worker node
	instanceId   = "i-00000000000000000"
	instanceType = "m5.xlarge"
	subnetId     = "subnet-00000000000000000"

	// network interface ids
	trunkInterfaceId   = "eni-00000000000000000"
	branchInterfaceId  = "eni-00000000000000001"
	branchInterfaceId2 = "eni-00000000000000002"
	attachmentId       = "attach-000000000000000"
	eniID              = "eni-00000000000000003"
	deviceIndex        = int64(0)

	ipAddress1 = "192.168.1.1"
	ipAddress2 = "192.168.1.2"

	// branch to trunk association id
	branchAssociationId = "association-00000000000000"
	vlanId              = 0

	// Security groups for the network interfaces
	securityGroup1 = "sg-00000000000000001"
	securityGroup2 = "sg-00000000000000002"
	securityGroups = []string{securityGroup1, securityGroup2}

	eniDescription           = "mock description of eni"
	eniDescriptionWithPrefix = "aws-k8s-" + eniDescription
	mockError                = fmt.Errorf("failed to do ec2 call")
)

var (
	associateTrunkInterfaceInput = &ec2.AssociateTrunkInterfaceInput{
		BranchInterfaceId: &branchInterfaceId,
		TrunkInterfaceId:  &trunkInterfaceId,
		VlanId:            aws.Int64(int64(vlanId)),
	}

	associateTrunkInterfaceOutput = &ec2.AssociateTrunkInterfaceOutput{
		InterfaceAssociation: &ec2.TrunkInterfaceAssociation{AssociationId: &branchAssociationId},
	}

	createNetworkInterfaceInput = &ec2.CreateNetworkInterfaceInput{
		Description: &eniDescriptionWithPrefix,
		Groups:      aws.StringSlice(securityGroups),
		SubnetId:    &subnetId,
	}

	createNetworkInterfaceOutput = &ec2.CreateNetworkInterfaceOutput{
		NetworkInterface: &ec2.NetworkInterface{NetworkInterfaceId: &branchInterfaceId}}

	deleteNetworkInterfaceInput = &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: &branchInterfaceId,
	}

	describeSubnetInput = &ec2.DescribeSubnetsInput{
		SubnetIds: []*string{&subnetId},
	}

	describeSubnetOutput = &ec2.DescribeSubnetsOutput{Subnets: []*ec2.Subnet{{SubnetId: &subnetId}}}

	describeNetworkInterfaceInputUsingInstanceId = &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("attachment.instance-id"),
			Values: []*string{&instanceId},
		}},
	}

	describeNetworkInterfaceOutputUsingInstanceId = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []*ec2.NetworkInterface{{
			NetworkInterfaceId: &branchInterfaceId,
			Status:             aws.String(ec2.AttachmentStatusDetached)}},
	}

	describeNetworkInterfaceInputUsingInterfaceId = &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("network-interface-id"),
			Values: []*string{&branchInterfaceId, &branchInterfaceId2},
		}},
	}

	describeNetworkInterfaceOutputUsingInterfaceId = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []*ec2.NetworkInterface{
			{NetworkInterfaceId: &branchInterfaceId, InterfaceType: aws.String("interface")},
			{NetworkInterfaceId: &trunkInterfaceId, InterfaceType: aws.String("trunk")},
		},
	}

	describeNetworkInterfaceInputUsingOneInterfaceId = &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("network-interface-id"),
			Values: []*string{&branchInterfaceId},
		}},
	}

	describeNetworkInterfaceOutputUsingOneInterfaceId = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []*ec2.NetworkInterface{
			{
				NetworkInterfaceId: &branchInterfaceId,
				InterfaceType:      aws.String("interface"),
				Attachment: &ec2.NetworkInterfaceAttachment{
					Status: aws.String(ec2.NetworkInterfaceStatusAvailable),
				},
			},
		},
	}

	describeTrunkInterfaceAssociationsInput = &ec2.DescribeTrunkInterfaceAssociationsInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("trunk-interface-association.trunk-interface-id"),
			Values: []*string{&trunkInterfaceId},
		}},
	}

	describeTrunkInterfaceAssociationsOutput = &ec2.DescribeTrunkInterfaceAssociationsOutput{InterfaceAssociations: []*ec2.TrunkInterfaceAssociation{
		{
			BranchInterfaceId: &branchInterfaceId,
			TrunkInterfaceId:  &trunkInterfaceId,
			VlanId:            aws.Int64(int64(vlanId)),
		},
	}}

	modifyNetworkInterfaceAttributeInput = &ec2.ModifyNetworkInterfaceAttributeInput{
		Attachment: &ec2.NetworkInterfaceAttachmentChanges{
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
		InstanceIds: []*string{&instanceId},
	}

	describeInstanceOutput = &ec2.DescribeInstancesOutput{
		Reservations: []*ec2.Reservation{{
			Instances: []*ec2.Instance{{
				InstanceId:   &instanceId,
				InstanceType: &instanceType,
				SubnetId:     &subnetId,
			}},
		}},
	}

	assignPrivateIPInput = &ec2.AssignPrivateIpAddressesInput{
		NetworkInterfaceId:             &eniID,
		SecondaryPrivateIpAddressCount: aws.Int64(int64(2)),
	}

	assignPrivateIPOutput = &ec2.AssignPrivateIpAddressesOutput{
		AssignedPrivateIpAddresses: []*ec2.AssignedPrivateIpAddress{
			{
				PrivateIpAddress: &ipAddress1,
			},
			{
				PrivateIpAddress: &ipAddress2,
			},
		},
	}

	describeNetworkInterfaceInput = &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("network-interface-id"),
			Values: []*string{&eniID},
		}},
	}

	describeNetworkInterfaceOutput = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []*ec2.NetworkInterface{
			{
				PrivateIpAddresses: []*ec2.NetworkInterfacePrivateIpAddress{
					{PrivateIpAddress: &ipAddress1},
					{PrivateIpAddress: &ipAddress2},
				},
			},
		},
	}

	maxRetryOnError = 3
)

// getMockWrapper returns the Mock EC2Wrapper along with the EC2APIHelper with mock EC2Wrapper set up
func getMockWrapper(ctrl *gomock.Controller) (EC2APIHelper, *mock_ec2.MockEC2Wrapper) {

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

	mockWrapper := mock_ec2.NewMockEC2Wrapper(ctrl)
	ec2ApiHelper := NewEC2APIHelper(mockWrapper)
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

	_, err := ec2ApiHelper.AssociateBranchToTrunk(&trunkInterfaceId, &branchInterfaceId, vlanId)

	assert.NotNil(t, err)
}

// TestEc2APIHelper_AssociateBranchToTrunk_Error tests that error is returned if ec2 call returns an error
func TestEc2APIHelper_AssociateBranchToTrunk_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	// Return empty association response
	mockWrapper.EXPECT().AssociateTrunkInterface(associateTrunkInterfaceInput).Return(nil, mockError)

	_, err := ec2ApiHelper.AssociateBranchToTrunk(&trunkInterfaceId, &branchInterfaceId, vlanId)

	assert.Error(t, mockError, err)
}

// TestEc2APIHelper_CreateNetworkInterface_NoSecondaryIP tests network interface creation when no secondary IPs are
// created
func TestEc2APIHelper_CreateNetworkInterface_NoSecondaryIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(createNetworkInterfaceOutput, nil)

	output, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, 0, nil)

	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *output.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateNetworkInterface_WithSecondaryIP tests network interface creation with secondary IP address
func TestEc2APIHelper_CreateNetworkInterface_WithSecondaryIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	ipCount := int64(5)

	createNetworkInterfaceInput.SecondaryPrivateIpAddressCount = &ipCount

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).
		Return(createNetworkInterfaceOutput, nil)

	output, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, 5, nil)

	createNetworkInterfaceInput.SecondaryPrivateIpAddressCount = nil

	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *output.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateNetworkInterface_TypeTrunk tests network interface creation with the interface type trunk
func TestEc2APIHelper_CreateNetworkInterface_TypeTrunk(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)
	interfaceTypeTrunk := "trunk"

	createNetworkInterfaceInput.InterfaceType = &interfaceTypeTrunk
	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(createNetworkInterfaceOutput, nil)

	output, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, 0, &interfaceTypeTrunk)

	createNetworkInterfaceInput.InterfaceType = nil

	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *output.NetworkInterfaceId)
}

// TestEc2APIHelper_CreateNetworkInterface_EmptyResponse tests error is returned if empty response is returned from
// ec2 api call
func TestEc2APIHelper_CreateNetworkInterface_EmptyResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(nil, nil)

	_, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, 0, nil)

	assert.NotNil(t, err)
}

// TestEc2APIHelper_CreateNetworkInterface_Error tests that error is returned if ec2 api call returns an error
func TestEc2APIHelper_CreateNetworkInterface_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(nil, mockError)

	_, err := ec2ApiHelper.CreateNetworkInterface(&eniDescription, &subnetId, securityGroups, 0, nil)

	assert.Error(t, err, mockError)
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

	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, mockError).Times(maxRetryOnError)

	err := ec2ApiHelper.DeleteNetworkInterface(&branchInterfaceId)
	assert.Error(t, mockError, err)
}

// TestEc2APIHelper_DeleteNetworkInterface_ErrorThenSuccess tests that if delete network call fails initially and
// succeeds subsequently then no error is returned
func TestEc2APIHelper_DeleteNetworkInterface_ErrorThenSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	gomock.InOrder(
		mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, mockError).Times(2),
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
	mockWrapper.EXPECT().DescribeSubnets(describeSubnetInput).Return(&ec2.DescribeSubnetsOutput{Subnets: []*ec2.Subnet{}}, nil)

	_, err := ec2ApiHelper.GetSubnet(&subnetId)
	assert.NotNil(t, err)
}

// TestEc2APIHelper_GetSubnet_Error tests that the error form ec2 api call is propagated to the caller.
func TestEc2APIHelper_GetSubnet_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)
	mockWrapper.EXPECT().DescribeSubnets(describeSubnetInput).Return(nil, mockError)

	_, err := ec2ApiHelper.GetSubnet(&subnetId)
	assert.Error(t, mockError, err)
}

// TestEc2APIHelper_GetNetworkInterfaceOfInstance tests that describe network interface returns no errors
// under valid input
func TestEc2APIHelper_GetNetworkInterfaceOfInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingInstanceId).
		Return(describeNetworkInterfaceOutputUsingInstanceId, nil)

	nwInterfaces, err := ec2ApiHelper.GetNetworkInterfaceOfInstance(&instanceId)
	assert.NoError(t, err)
	assert.Equal(t, branchInterfaceId, *nwInterfaces[0].NetworkInterfaceId)
}

// TestEc2APIHelper_GetNetworkInterfaceOfInstance_Error tests that error is propagated back to the caller if
// the Ec2 API call returns an error
func TestEc2APIHelper_GetNetworkInterfaceOfInstance_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingInstanceId).Return(nil, mockError)

	_, err := ec2ApiHelper.GetNetworkInterfaceOfInstance(&instanceId)
	assert.Error(t, mockError, err)
}

// TestEc2APIHelper_DescribeNetworkInterfaces tests describe network interface call works as expected under
// no errors under valid input
func TestEc2APIHelper_DescribeNetworkInterfaces(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingInterfaceId).
		Return(describeNetworkInterfaceOutputUsingInterfaceId, nil)

	nwInterfaces, err := ec2ApiHelper.DescribeNetworkInterfaces([]*string{&branchInterfaceId, &branchInterfaceId2})
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
		Return(nil, mockError)

	_, err := ec2ApiHelper.DescribeNetworkInterfaces([]*string{&branchInterfaceId, &branchInterfaceId2})
	assert.Error(t, mockError, err)
}

// TestEc2APIHelper_DescribeTrunkInterfaceAssociation tests that the describe trunk interface association returns
/// no errors under valid input
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
		Return(nil, mockError)

	_, err := ec2ApiHelper.DescribeTrunkInterfaceAssociation(&trunkInterfaceId)
	assert.Error(t, mockError, err)
}

// TestEc2APIHelper_GetTrunkInterface tests that trunk interface for the instance is returned under valid input
func TestEc2APIHelper_GetTrunkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingInstanceId).
		Return(describeNetworkInterfaceOutputUsingInterfaceId, nil)

	nwInterface, err := ec2ApiHelper.GetTrunkInterface(&instanceId)

	assert.NoError(t, err)
	assert.Equal(t, trunkInterfaceId, *nwInterface.NetworkInterfaceId)
}

// TestEc2APIHelper_GetTrunkInterface_TrunkNotFound tests that no error is returned when trunk interface is not found
func TestEc2APIHelper_GetTrunkInterface_TrunkNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	filters := []*ec2.Filter{{
		Name:   aws.String("attachment.instance-id"),
		Values: []*string{&instanceId},
	}}

	interfaces := []*ec2.NetworkInterface{
		{
			InterfaceType:      aws.String("interface"),
			NetworkInterfaceId: &branchInterfaceId,
		},
	}

	mockWrapper.EXPECT().DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: filters,
	}).Return(&ec2.DescribeNetworkInterfacesOutput{NetworkInterfaces: interfaces}, nil)

	_, err := ec2ApiHelper.GetTrunkInterface(&instanceId)

	assert.NoError(t, err)
}

func TestEc2APIHelper_CreateAndAttachNetworkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	oldStatus := describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status
	describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status =
		aws.String(ec2.AttachmentStatusAttached)

	mockWrapper.EXPECT().CreateNetworkInterface(createNetworkInterfaceInput).Return(createNetworkInterfaceOutput, nil)
	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).Return(attachNetworkInterfaceOutput, nil)
	mockWrapper.EXPECT().ModifyNetworkInterfaceAttribute(modifyNetworkInterfaceAttributeInput).Return(nil, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
		Return(describeNetworkInterfaceOutputUsingOneInterfaceId, nil)

	nwInterface, err := ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceId, &subnetId, securityGroups,
		&deviceIndex, &eniDescription, nil, 0)

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
	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).Return(attachNetworkInterfaceOutput, mockError)

	// Test delete is called
	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, nil)

	nwInterface, err := ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceId, &subnetId, securityGroups,
		&deviceIndex, &eniDescription, nil, 0)

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
	mockWrapper.EXPECT().ModifyNetworkInterfaceAttribute(modifyNetworkInterfaceAttributeInput).Return(nil, mockError)

	// Test detach and delete is called
	mockWrapper.EXPECT().DetachNetworkInterface(detachNetworkInterfaceInput).Return(nil, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInputUsingOneInterfaceId).
		Return(describeNetworkInterfaceOutputUsingOneInterfaceId, nil)
	mockWrapper.EXPECT().DeleteNetworkInterface(deleteNetworkInterfaceInput).Return(nil, nil)

	nwInterface, err := ec2ApiHelper.CreateAndAttachNetworkInterface(&instanceId, &subnetId, securityGroups,
		&deviceIndex, &eniDescription, nil, 0)

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
		Return(nil, mockError)

	err := ec2ApiHelper.SetDeleteOnTermination(&attachmentId, &branchInterfaceId)
	assert.Error(t, mockError, err)
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

	mockWrapper.EXPECT().AttachNetworkInterface(attachNetworkInterfaceInput).Return(nil, mockError)

	_, err := ec2ApiHelper.AttachNetworkInterfaceToInstance(&instanceId, &branchInterfaceId, &deviceIndex)
	assert.Error(t, mockError, err)
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
				NetworkInterfaces: []*ec2.NetworkInterface{{Attachment: &ec2.NetworkInterfaceAttachment{Status: &statusAttached}}}}, nil).Times(1),
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
		Return(nil, mockError)

	err := ec2ApiHelper.WaitForNetworkInterfaceStatusChange(&branchInterfaceId, statusAvailable)
	assert.Error(t, mockError, err)
}

// TestEc2APIHelper_DetachAndDeleteNetworkInterface tests the ec2 api calls are called in order with the desired input
func TestEc2APIHelper_DetachAndDeleteNetworkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	oldStatus := describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status
	describeNetworkInterfaceOutputUsingOneInterfaceId.NetworkInterfaces[0].Attachment.Status =
		aws.String(ec2.NetworkInterfaceStatusAvailable)

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

	mockWrapper.EXPECT().DetachNetworkInterface(detachNetworkInterfaceInput).Return(nil, mockError)

	err := ec2ApiHelper.DetachAndDeleteNetworkInterface(&attachmentId, &branchInterfaceId)
	assert.Error(t, mockError, err)

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

	mockWrapper.EXPECT().DescribeInstances(describeInstanceInput).Return(nil, mockError)

	_, err := ec2ApiHelper.GetInstanceDetails(&instanceId)
	assert.Error(t, mockError, err)
}

// TestEC2APIHelper_AssignIPv4AddressesAndWaitTillReady tests that once new IP addresses are assigned they are returned
// only when the IPs are attached to the instance
func TestEC2APIHelper_AssignIPv4AddressesAndWaitTillReady(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInput).Return(assignPrivateIPOutput, nil)
	mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInput).Return(describeNetworkInterfaceOutput, nil)

	createdIPs, err := ec2ApiHelper.AssignIPv4AddressesAndWaitTillReady(eniID, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ipAddress1, ipAddress2} ,createdIPs)
}

// TestEC2APIHelper_AssignIPv4AddressesAndWaitTillReady_Error tests that error is returned if the assign private IP call
// fails
func TestEC2APIHelper_AssignIPv4AddressesAndWaitTillReady_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInput).Return(nil, mockError)

	_, err := ec2ApiHelper.AssignIPv4AddressesAndWaitTillReady(eniID, 2)

	assert.Error(t, mockError, err)
}

// TestEC2APIHelper_AssignIPv4AddressesAndWaitTillReady_AttachedAfterSecondDescribe tests if the describe call is called
// till all the newly assigned ips are returned
func TestEC2APIHelper_AssignIPv4AddressesAndWaitTillReady_AttachedAfterSecondDescribe(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ec2ApiHelper, mockWrapper := getMockWrapper(ctrl)

	mockWrapper.EXPECT().AssignPrivateIPAddresses(assignPrivateIPInput).Return(assignPrivateIPOutput, nil)
	gomock.InOrder(
		// First call returns just one ip address
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInput).Return(&ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []*ec2.NetworkInterface{
				{PrivateIpAddresses: []*ec2.NetworkInterfacePrivateIpAddress{
					{PrivateIpAddress: &ipAddress1},
				}}}}, nil),
		// Second call all created IPs returned
		mockWrapper.EXPECT().DescribeNetworkInterfaces(describeNetworkInterfaceInput).Return(describeNetworkInterfaceOutput, nil),
	)

	createdIPs, err := ec2ApiHelper.AssignIPv4AddressesAndWaitTillReady(eniID, 2)

	assert.NoError(t, err)
	assert.Equal(t, []string{ipAddress1, ipAddress2} ,createdIPs)
}

