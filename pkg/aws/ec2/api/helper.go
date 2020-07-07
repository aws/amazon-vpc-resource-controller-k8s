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
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	retry "k8s.io/client-go/util/retry"
)

const (
	CreateENIDescriptionPrefix = "aws-k8s-"
)

var (
	// defaultBackOff is the default back off for retrying ec2 api calls.
	defaultBackOff = wait.Backoff{
		Duration: time.Millisecond * 100,
		Factor:   3.0,
		Jitter:   0.1,
		Steps:    7,
		Cap:      time.Second * 10,
	}
	waitForENIAttachment = wait.Backoff{
		Duration: time.Millisecond * 500,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    7,
		Cap:      time.Second * 30,
	}
)

type ec2APIHelper struct {
	ec2Wrapper EC2Wrapper
}

func NewEC2APIHelper(ec2Wrapper EC2Wrapper) EC2APIHelper {
	return &ec2APIHelper{ec2Wrapper: ec2Wrapper}
}

type EC2APIHelper interface {
	AssociateBranchToTrunk(trunkInterfaceId *string, branchInterfaceId *string, vlanId int) (*ec2.AssociateTrunkInterfaceOutput, error)
	CreateNetworkInterface(description *string, subnetId *string, securityGroups []string,
		secondaryPrivateIPCount int, interfaceType *string) (*ec2.NetworkInterface, error)
	DeleteNetworkInterface(interfaceId *string) error
	GetSubnet(subnetId *string) (*ec2.Subnet, error)
	GetNetworkInterfaceOfInstance(instanceId *string) ([]*ec2.NetworkInterface, error)
	DescribeNetworkInterfaces(nwInterfaceIds []*string) ([]*ec2.NetworkInterface, error)
	DescribeTrunkInterfaceAssociation(trunkInterfaceId *string) ([]*ec2.TrunkInterfaceAssociation, error)
	GetTrunkInterface(instanceId *string) (*ec2.NetworkInterface, error)
	CreateAndAttachNetworkInterface(instanceId *string, subnetId *string, securityGroups []string,
		deviceIndex *int64, description *string, interfaceType *string, secondaryIPCount int) (*ec2.NetworkInterface, error)
	AttachNetworkInterfaceToInstance(instanceId *string, nwInterfaceId *string, deviceIndex *int64) (*string, error)
	SetDeleteOnTermination(attachmentId *string, eniId *string) error
	DetachNetworkInterfaceFromInstance(attachmentId *string) error
	DetachAndDeleteNetworkInterface(attachmentId *string, nwInterfaceId *string) error
	WaitForNetworkInterfaceStatusChange(networkInterfaceId *string, desiredStatus string) error
	GetInstanceDetails(instanceId *string) (*ec2.Instance, error)
}

// CreateNetworkInterface creates a new network interface
func (h *ec2APIHelper) CreateNetworkInterface(description *string, subnetId *string, securityGroups []string,
	secondaryPrivateIPCount int, interfaceType *string) (*ec2.NetworkInterface, error) {
	eniDescription := CreateENIDescriptionPrefix + *description

	var ec2SecurityGroups []*string
	if securityGroups != nil && len(securityGroups) != 0 {
		// Only add security groups if there are one or more security group provided, otherwise API call will fail instead
		// of creating the interface with default security groups
		ec2SecurityGroups = aws.StringSlice(securityGroups)
	}

	createInput := &ec2.CreateNetworkInterfaceInput{
		Description: aws.String(eniDescription),
		Groups:      ec2SecurityGroups,
		SubnetId:    subnetId,
	}

	if secondaryPrivateIPCount != 0 {
		createInput.SecondaryPrivateIpAddressCount = aws.Int64(int64(secondaryPrivateIPCount))
	}

	if interfaceType != nil {
		createInput.InterfaceType = interfaceType
	}

	createOutput, err := h.ec2Wrapper.CreateNetworkInterface(createInput)
	if err != nil {
		return nil, errors.Wrap(err, "eni wrapper: unable to create network interface")
	}
	if createOutput != nil &&
		createOutput.NetworkInterface != nil &&
		createOutput.NetworkInterface.NetworkInterfaceId != nil {

		return createOutput.NetworkInterface, nil
	}

	return nil, fmt.Errorf("network interface details not returned in response for requet %v", *createInput)
}

// GetSubnet returns the subnet details of the given subnet
func (h *ec2APIHelper) GetSubnet(subnetId *string) (*ec2.Subnet, error) {
	describeSubnetInput := &ec2.DescribeSubnetsInput{
		SubnetIds: []*string{subnetId},
	}

	describeSubnetOutput, err := h.ec2Wrapper.DescribeSubnets(describeSubnetInput)
	if err != nil {
		return nil, err
	}
	if describeSubnetOutput != nil && len(describeSubnetOutput.Subnets) == 0 {
		return nil, fmt.Errorf("subnet not found %s", *subnetId)
	}

	return describeSubnetOutput.Subnets[0], nil
}

// DeleteNetworkInterface deletes a network interface with retries with exponential back offs
func (h *ec2APIHelper) DeleteNetworkInterface(interfaceId *string) error {
	deleteNetworkInterface := &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: interfaceId,
	}

	err := retry.OnError(defaultBackOff, func(err error) bool { return true }, func() error {
		_, err := h.ec2Wrapper.DeleteNetworkInterface(deleteNetworkInterface)
		return err
	})

	return err
}

// GetNetworkInterfaceOfInstance returns all the network interface associated with an instance id
func (h *ec2APIHelper) GetNetworkInterfaceOfInstance(instanceId *string) ([]*ec2.NetworkInterface, error) {
	filters := []*ec2.Filter{{
		Name:   aws.String("attachment.instance-id"),
		Values: []*string{instanceId},
	}}

	describeNetworkInterfacesInput := &ec2.DescribeNetworkInterfacesInput{Filters: filters}
	describeNetworkInterfaceOutput, err := h.ec2Wrapper.DescribeNetworkInterfaces(describeNetworkInterfacesInput)

	if err != nil {
		return nil, err
	}

	if describeNetworkInterfaceOutput != nil && describeNetworkInterfaceOutput.NetworkInterfaces != nil {
		return describeNetworkInterfaceOutput.NetworkInterfaces, nil
	}

	return nil, fmt.Errorf("failed to find network interfaces for request %v",
		*describeNetworkInterfacesInput)
}

// DescribeNetworkInterfaces returns the network interface details of the given network interface ids
func (h *ec2APIHelper) DescribeNetworkInterfaces(nwInterfaceIds []*string) ([]*ec2.NetworkInterface, error) {
	filters := []*ec2.Filter{{
		Name:   aws.String("network-interface-id"),
		Values: nwInterfaceIds,
	}}

	describeNetworkInterfacesInput := &ec2.DescribeNetworkInterfacesInput{Filters: filters}
	describeNetworkInterfaceOutput, err := h.ec2Wrapper.DescribeNetworkInterfaces(describeNetworkInterfacesInput)
	if err != nil {
		return nil, err
	}

	if describeNetworkInterfaceOutput != nil && describeNetworkInterfaceOutput.NetworkInterfaces != nil {
		return describeNetworkInterfaceOutput.NetworkInterfaces, nil
	}

	return nil, fmt.Errorf("failed to find network interfaces for request %v",
		*describeNetworkInterfacesInput)
}

// DescribeTrunkInterfaceAssociation describes all the association of the given trunk interface id
func (h *ec2APIHelper) DescribeTrunkInterfaceAssociation(trunkInterfaceId *string) ([]*ec2.TrunkInterfaceAssociation, error) {
	describeTrunkInterfaceAssociationInput := &ec2.DescribeTrunkInterfaceAssociationsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("trunk-interface-association.trunk-interface-id"),
				Values: []*string{trunkInterfaceId},
			},
		},
	}
	describeTrunkInterfaceAssociationOutput, err :=
		h.ec2Wrapper.DescribeTrunkInterfaceAssociations(describeTrunkInterfaceAssociationInput)

	if err != nil {
		return nil, err
	}
	if describeTrunkInterfaceAssociationOutput != nil &&
		describeTrunkInterfaceAssociationOutput.InterfaceAssociations != nil {
		return describeTrunkInterfaceAssociationOutput.InterfaceAssociations, nil
	}

	// The describeTrunkInterfaceAssociationOutput may be null which means that there is no branch associated with the
	// trunk
	return nil, nil
}

// AssociateBranchToTrunk associates a branch network interface to a trunk network interface
func (h *ec2APIHelper) AssociateBranchToTrunk(trunkInterfaceId *string, branchInterfaceId *string,
	vlanId int) (*ec2.AssociateTrunkInterfaceOutput, error) {

	associateTrunkInterfaceIP := &ec2.AssociateTrunkInterfaceInput{
		BranchInterfaceId: branchInterfaceId,
		TrunkInterfaceId:  trunkInterfaceId,
		VlanId:            aws.Int64(int64(vlanId)),
	}

	associateTrunkInterfaceOutput, err := h.ec2Wrapper.AssociateTrunkInterface(associateTrunkInterfaceIP)
	if err != nil {
		return associateTrunkInterfaceOutput, err
	}

	if associateTrunkInterfaceOutput != nil &&
		associateTrunkInterfaceOutput.InterfaceAssociation != nil &&
		associateTrunkInterfaceOutput.InterfaceAssociation.AssociationId != nil {
		return associateTrunkInterfaceOutput, nil
	}

	return associateTrunkInterfaceOutput, fmt.Errorf("no association id present in the output of request %v",
		*associateTrunkInterfaceIP)
}

// GetTrunkInterface returns the first trunk interface associated with the instance. If no trunk is found
// it tries to create the trunk interface
func (h *ec2APIHelper) GetTrunkInterface(instanceId *string) (*ec2.NetworkInterface, error) {
	networkInterfaces, err := h.GetNetworkInterfaceOfInstance(instanceId)
	if err != nil {
		return nil, err
	}

	for _, nwInterface := range networkInterfaces {
		if *nwInterface.InterfaceType == "trunk" {
			return nwInterface, nil
		}
	}

	// Trunk not found
	return nil, nil
}

// CreateAndAttachNetworkInterface creates and attaches the network interface to the instance. The function will
// wait till the interface is successfully attached
func (h *ec2APIHelper) CreateAndAttachNetworkInterface(instanceId *string, subnetId *string, securityGroups []string,
	deviceIndex *int64, description *string, interfaceType *string, secondaryIPCount int) (*ec2.NetworkInterface, error) {

	nwInterface, err := h.CreateNetworkInterface(description, subnetId, securityGroups, secondaryIPCount, interfaceType)
	if err != nil {
		return nil, err
	}

	var attachmentId *string

	attachmentId, err = h.AttachNetworkInterfaceToInstance(instanceId, nwInterface.NetworkInterfaceId, deviceIndex)
	if err != nil {
		errDelete := h.DeleteNetworkInterface(nwInterface.NetworkInterfaceId)
		if errDelete != nil {
			return nwInterface, fmt.Errorf("failed to attach the network interface %v: failed to delete the nw interfac %v",
				err, errDelete)
		}
		return nil, err
	}

	err = h.SetDeleteOnTermination(attachmentId, nwInterface.NetworkInterfaceId)
	if err != nil {
		errDelete := h.DetachAndDeleteNetworkInterface(attachmentId, nwInterface.NetworkInterfaceId)
		if errDelete != nil {
			return nwInterface, fmt.Errorf("failed to set deletion on termination: %v: failed to delete nw interface: %v",
				err, errDelete)
		}
		return nil, err
	}

	err = h.WaitForNetworkInterfaceStatusChange(nwInterface.NetworkInterfaceId, ec2.AttachmentStatusAttached)
	if err != nil {
		errDelete := h.DetachAndDeleteNetworkInterface(attachmentId, nwInterface.NetworkInterfaceId)
		if errDelete != nil {
			return nwInterface, fmt.Errorf("failed to verify status attached: %v: failed to delete nw interface: %v",
				err, errDelete)
		}
		return nil, err
	}

	return nwInterface, nil
}

// SetDeleteOnTermination sets the deletion on termination of the network interface to true
func (h *ec2APIHelper) SetDeleteOnTermination(attachmentId *string, eniId *string) error {
	modifyNetworkInterfaceInput := &ec2.ModifyNetworkInterfaceAttributeInput{
		Attachment: &ec2.NetworkInterfaceAttachmentChanges{
			AttachmentId:        attachmentId,
			DeleteOnTermination: aws.Bool(true),
		},
		NetworkInterfaceId: eniId,
	}

	_, err := h.ec2Wrapper.ModifyNetworkInterfaceAttribute(modifyNetworkInterfaceInput)

	return err
}

// AttachNetworkInterfaceToInstance attaches the network interface to the instance
func (h *ec2APIHelper) AttachNetworkInterfaceToInstance(instanceId *string, nwInterfaceId *string, deviceIndex *int64) (*string, error) {
	attachNetworkInterfaceInput := &ec2.AttachNetworkInterfaceInput{
		DeviceIndex:        deviceIndex,
		InstanceId:         instanceId,
		NetworkInterfaceId: nwInterfaceId,
	}

	attachNetworkInterfaceOutput, err := h.ec2Wrapper.AttachNetworkInterface(attachNetworkInterfaceInput)
	if err != nil {
		return nil, err
	}
	if attachNetworkInterfaceOutput != nil && attachNetworkInterfaceOutput.AttachmentId != nil {
		return attachNetworkInterfaceOutput.AttachmentId, nil
	}

	return nil, fmt.Errorf("failed to find attachment id in the request %v", *attachNetworkInterfaceInput)
}

// DetachNetworkInterfaceFromInstance detaches a network interface using the attachment id
func (h *ec2APIHelper) DetachNetworkInterfaceFromInstance(attachmentId *string) error {
	input := &ec2.DetachNetworkInterfaceInput{
		AttachmentId: attachmentId,
	}

	_, err := h.ec2Wrapper.DetachNetworkInterface(input)
	return err
}

// WaitForNetworkInterfaceStatusChange keeps on retrying with backoff to see if the current status of the network
// interface is equal to the desired state of the network interface
func (h *ec2APIHelper) WaitForNetworkInterfaceStatusChange(networkInterfaceId *string, desiredStatus string) error {

	ErrRetryAttachmentStatusCheck := fmt.Errorf("interface not in desired stateus yet %s, interface id %s",
		desiredStatus, *networkInterfaceId)

	err := retry.OnError(waitForENIAttachment,
		func(err error) bool {
			if err == ErrRetryAttachmentStatusCheck {
				return true
			}
			return false
		}, func() error {
			interfaces, err := h.DescribeNetworkInterfaces([]*string{networkInterfaceId})
			if err == nil && len(interfaces) == 1 {
				attachment := interfaces[0].Attachment
				if attachment != nil && attachment.Status != nil && *attachment.Status == desiredStatus {
					return nil
				} else {
					return ErrRetryAttachmentStatusCheck
				}
			}
			return err
		})

	return err
}

// GetInstanceDetails returns the details of the instance
func (h *ec2APIHelper) GetInstanceDetails(instanceId *string) (*ec2.Instance, error) {
	describeInstanceInput := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{instanceId},
	}

	describeInstanceOutput, err := h.ec2Wrapper.DescribeInstances(describeInstanceInput)
	if err != nil {
		return nil, err
	}

	if describeInstanceOutput != nil && describeInstanceOutput.Reservations != nil &&
		len(describeInstanceOutput.Reservations) != 0 && describeInstanceOutput.Reservations[0] != nil &&
		describeInstanceOutput.Reservations[0].Instances != nil && len(describeInstanceOutput.Reservations[0].Instances) != 0 {
		return describeInstanceOutput.Reservations[0].Instances[0], nil
	}

	return nil, fmt.Errorf("failed to find instance details for input %v", *describeInstanceInput)
}

// DetachAndDeleteNetworkInterface detaches the network interface first and then deletes it
func (h *ec2APIHelper) DetachAndDeleteNetworkInterface(attachmentID *string, nwInterfaceID *string) error {
	err := h.DetachNetworkInterfaceFromInstance(attachmentID)
	if err != nil {
		return err
	}
	err = h.WaitForNetworkInterfaceStatusChange(nwInterfaceID, ec2.NetworkInterfaceStatusAvailable)
	if err != nil {
		return err
	}
	err = h.DeleteNetworkInterface(nwInterfaceID)
	if err != nil {
		return err
	}
	return nil
}
