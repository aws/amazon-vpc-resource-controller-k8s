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
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
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
		Duration: time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    7,
		Cap:      time.Minute * 5,
	}
	waitForIPAttachment = wait.Backoff{
		Duration: time.Millisecond * 250,
		Factor:   2.0,
		Jitter:   0.1,
		Steps:    7,
		Cap:      time.Minute,
	}
	defaultControllerTag = &ec2.Tag{
		Key:   aws.String(config.NetworkInterfaceOwnerTagKey),
		Value: aws.String(config.NetworkInterfaceOwnerTagValue),
	}
	clusterNameTag *ec2.Tag
)

type ec2APIHelper struct {
	ec2Wrapper EC2Wrapper
}

func NewEC2APIHelper(ec2Wrapper EC2Wrapper, clusterName string) EC2APIHelper {
	// Set the key and value of the cluster name tag which will be used to tag all the network interfaces created by
	// the controller
	clusterNameTag = &ec2.Tag{
		Key:   aws.String(fmt.Sprintf(config.ClusterNameTagKeyFormat, clusterName)),
		Value: aws.String(config.ClusterNameTagValue),
	}
	return &ec2APIHelper{ec2Wrapper: ec2Wrapper}
}

type EC2APIHelper interface {
	AssociateBranchToTrunk(trunkInterfaceId *string, branchInterfaceId *string, vlanId int) (*ec2.AssociateTrunkInterfaceOutput, error)
	CreateNetworkInterface(description *string, subnetId *string, securityGroups []string, tags []*ec2.Tag,
		secondaryPrivateIPCount int, interfaceType *string) (*ec2.NetworkInterface, error)
	DeleteNetworkInterface(interfaceId *string) error
	GetSubnet(subnetId *string) (*ec2.Subnet, error)
	GetBranchNetworkInterface(trunkID *string) ([]*ec2.NetworkInterface, error)
	GetInstanceNetworkInterface(instanceId *string) ([]*ec2.InstanceNetworkInterface, error)
	DescribeNetworkInterfaces(nwInterfaceIds []*string) ([]*ec2.NetworkInterface, error)
	DescribeTrunkInterfaceAssociation(trunkInterfaceId *string) ([]*ec2.TrunkInterfaceAssociation, error)
	CreateAndAttachNetworkInterface(instanceId *string, subnetId *string, securityGroups []string, tags []*ec2.Tag,
		deviceIndex *int64, description *string, interfaceType *string, secondaryIPCount int) (*ec2.NetworkInterface, error)
	AttachNetworkInterfaceToInstance(instanceId *string, nwInterfaceId *string, deviceIndex *int64) (*string, error)
	SetDeleteOnTermination(attachmentId *string, eniId *string) error
	DetachNetworkInterfaceFromInstance(attachmentId *string) error
	DetachAndDeleteNetworkInterface(attachmentId *string, nwInterfaceId *string) error
	WaitForNetworkInterfaceStatusChange(networkInterfaceId *string, desiredStatus string) error
	GetInstanceDetails(instanceId *string) (*ec2.Instance, error)
	AssignIPv4AddressesAndWaitTillReady(eniID string, count int) ([]string, error)
	UnassignPrivateIpAddresses(eniID string, ips []string) error
	AssignIPv4PrefixesAndWaitTillReady(eniID string, count int) ([]string, error)
	UnassignPrivateIpPrefixes(eniID string, ips []string) error
}

// CreateNetworkInterface creates a new network interface
func (h *ec2APIHelper) CreateNetworkInterface(description *string, subnetId *string, securityGroups []string, tags []*ec2.Tag,
	secondaryPrivateIPCount int, interfaceType *string) (*ec2.NetworkInterface, error) {
	eniDescription := CreateENIDescriptionPrefix + *description

	var ec2SecurityGroups []*string
	if securityGroups != nil && len(securityGroups) != 0 {
		// Only add security groups if there are one or more security group provided, otherwise API call will fail instead
		// of creating the interface with default security groups
		ec2SecurityGroups = aws.StringSlice(securityGroups)
	}

	if tags == nil {
		tags = []*ec2.Tag{}
	}

	// Append the default controller tag to scope down the permissions on network interfaces using IAM roles and add the
	// k8s cluster name tag which will be used by the controller to clean up dangling ENIs
	tags = append(tags, defaultControllerTag, clusterNameTag)
	tagSpecifications := []*ec2.TagSpecification{
		{
			ResourceType: aws.String(ec2.ResourceTypeNetworkInterface),
			Tags:         tags,
		},
	}

	createInput := &ec2.CreateNetworkInterfaceInput{
		Description:       aws.String(eniDescription),
		Groups:            ec2SecurityGroups,
		SubnetId:          subnetId,
		TagSpecifications: tagSpecifications,
	}

	if secondaryPrivateIPCount != 0 {
		createInput.SecondaryPrivateIpAddressCount = aws.Int64(int64(secondaryPrivateIPCount))
	}

	if interfaceType != nil {
		createInput.InterfaceType = interfaceType
	}

	createOutput, err := h.ec2Wrapper.CreateNetworkInterface(createInput)
	if err != nil {
		return nil, err
	}
	if createOutput == nil ||
		createOutput.NetworkInterface == nil ||
		createOutput.NetworkInterface.NetworkInterfaceId == nil {

		return nil, fmt.Errorf("network interface details not returned in response for requet %v", *createInput)
	}

	nwInterface := createOutput.NetworkInterface
	// If the interface type is trunk then attach interface permissions
	if interfaceType != nil && *interfaceType == "trunk" {
		// Get attach permission from User's Service Linked Role. Account ID will be added by the EC2 API Wrapper
		input := &ec2.CreateNetworkInterfacePermissionInput{
			NetworkInterfaceId: nwInterface.NetworkInterfaceId,
			Permission:         aws.String(ec2.InterfacePermissionTypeInstanceAttach),
		}

		_, err = h.ec2Wrapper.CreateNetworkInterfacePermission(input)
		if err != nil {
			errDelete := h.DeleteNetworkInterface(nwInterface.NetworkInterfaceId)
			if errDelete != nil {
				return nwInterface, fmt.Errorf("failed to attach the network interface %v: failed to delete the nw interfac %v",
					err, errDelete)
			}
			return nil, fmt.Errorf("failed to get attach network interface permissions for trunk %v", err)
		}
	}
	return nwInterface, nil
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

// GetInstanceNetworkInterface returns all the network interface associated with an instance id
func (h *ec2APIHelper) GetInstanceNetworkInterface(instanceId *string) ([]*ec2.InstanceNetworkInterface, error) {
	instanceDetails, err := h.GetInstanceDetails(instanceId)
	if err != nil {
		return nil, err
	}

	if instanceDetails != nil && instanceDetails.NetworkInterfaces != nil {
		return instanceDetails.NetworkInterfaces, nil
	}

	return nil, fmt.Errorf("failed to find network interfaces for instance %s",
		*instanceDetails)
}

// DescribeNetworkInterfaces returns the network interface details of the given network interface ids
func (h *ec2APIHelper) DescribeNetworkInterfaces(nwInterfaceIds []*string) ([]*ec2.NetworkInterface, error) {
	describeNetworkInterfacesInput := &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: nwInterfaceIds,
	}
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

// TODO: Not used currently as the API is not publicly available with assumed role
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

	// Get attach permission from User's Service Linked Role. Account ID will be added by the EC2 API Wrapper
	input := &ec2.CreateNetworkInterfacePermissionInput{
		NetworkInterfaceId: branchInterfaceId,
		Permission:         aws.String(ec2.InterfacePermissionTypeInstanceAttach),
	}

	_, err := h.ec2Wrapper.CreateNetworkInterfacePermission(input)
	if err != nil {
		return nil, fmt.Errorf("failed to get attach network interface permissions for branch %v", err)
	}

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

// CreateAndAttachNetworkInterface creates and attaches the network interface to the instance. The function will
// wait till the interface is successfully attached
func (h *ec2APIHelper) CreateAndAttachNetworkInterface(instanceId *string, subnetId *string, securityGroups []string,
	tags []*ec2.Tag, deviceIndex *int64, description *string, interfaceType *string, secondaryIPCount int) (*ec2.NetworkInterface, error) {

	nwInterface, err := h.CreateNetworkInterface(description, subnetId, securityGroups, tags, secondaryIPCount, interfaceType)
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

	ErrRetryAttachmentStatusCheck := fmt.Errorf("interface not in desired status yet %s, interface id %s",
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

// AssignIPv4AddressesAndWaitTillReady assigns IPv4 Address to the interface and waits till the IP Address is attached
// to the instance
func (h *ec2APIHelper) AssignIPv4AddressesAndWaitTillReady(eniID string, count int) ([]string, error) {
	var assignedIPs []string

	input := &ec2.AssignPrivateIpAddressesInput{
		NetworkInterfaceId:             &eniID,
		SecondaryPrivateIpAddressCount: aws.Int64(int64(count)),
	}

	assignPrivateIPOutput, err := h.ec2Wrapper.AssignPrivateIPAddresses(input)
	if err != nil {
		return assignedIPs, err
	}

	if assignPrivateIPOutput != nil && assignPrivateIPOutput.AssignedPrivateIpAddresses != nil &&
		len(assignPrivateIPOutput.AssignedPrivateIpAddresses) == 0 {
		return assignedIPs, fmt.Errorf("failed ot create %v ip address to eni %s", count, eniID)
	}

	ErrIPNotAttachedYet := fmt.Errorf("private IPv4 address is not attached yet")

	err = retry.OnError(waitForIPAttachment,
		func(err error) bool {
			if err == ErrIPNotAttachedYet {
				// Retry in case IPs are not attached yet
				return true
			}
			return false
		}, func() error {
			// Describe the network interface on which the new IPs are assigned
			interfaces, err := h.DescribeNetworkInterfaces([]*string{&eniID})
			// Re initialize the slice so we don't add IPs multiple time
			assignedIPs = []string{}
			if err == nil && len(interfaces) == 1 && interfaces[0].PrivateIpAddresses != nil {
				// Get the map of IPs returned by the describe network interface call
				ipAddress := map[string]bool{}
				for _, ipAddr := range interfaces[0].PrivateIpAddresses {
					ipAddress[*ipAddr.PrivateIpAddress] = true
				}
				// Verify the describe network interface returns all the IPs that were assigned in the
				// AssignPrivateIPAddresses call
				for _, ip := range assignPrivateIPOutput.AssignedPrivateIpAddresses {
					if _, ok := ipAddress[*ip.PrivateIpAddress]; !ok {
						// Even if one IP is not assigned, set the error so that we only return only the IPs that
						// are successfully assigned on the ENI
						err = ErrIPNotAttachedYet
					} else {
						assignedIPs = append(assignedIPs, *ip.PrivateIpAddress)
					}
				}
				//
				return err
			}
			return err
		})

	if err != nil {
		// If some of the assigned IP addresses were not yet returned in the describe network interface call,
		// returns the list of IPs that were returned
		return assignedIPs, err
	}

	return assignedIPs, nil
}

func (h *ec2APIHelper) UnassignPrivateIpAddresses(eniID string, ips []string) error {
	unassignPrivateIpAddressesInput := &ec2.UnassignPrivateIpAddressesInput{
		NetworkInterfaceId: &eniID,
		PrivateIpAddresses: aws.StringSlice(ips),
	}
	_, err := h.ec2Wrapper.UnassignPrivateIPAddresses(unassignPrivateIpAddressesInput)
	return err
}

// AssignIPv4AddressesAndWaitTillReady assigns IPv4 Address to the interface and waits till the IP Address is attached
// to the instance
func (h *ec2APIHelper) AssignIPv4PrefixesAndWaitTillReady(eniID string, count int) ([]string, error) {
	var assignedIPPrefixes []string

	input := &ec2.AssignPrivateIpAddressesInput{
		NetworkInterfaceId:             &eniID,
		Ipv4PrefixCount: aws.Int64(int64(count)),
	}

	assignPrivateIPOutput, err := h.ec2Wrapper.AssignPrivateIPAddresses(input)
	if err != nil {
		return assignedIPPrefixes, err
	}

	if assignPrivateIPOutput != nil && assignPrivateIPOutput.AssignedIpv4Prefixes != nil &&
		len(assignPrivateIPOutput.AssignedIpv4Prefixes) == 0 {
		return assignedIPPrefixes, fmt.Errorf("failed ot create %v ip address to eni %s", count, eniID)
	}

	ErrIPNotAttachedYet := fmt.Errorf("private IPv4 prefix is not attached yet")

	err = retry.OnError(waitForIPAttachment,
		func(err error) bool {
			if err == ErrIPNotAttachedYet {
				// Retry in case IPs are not attached yet
				return true
			}
			return false
		}, func() error {
			// Describe the network interface on which the new IP prefixes are assigned
			interfaces, err := h.DescribeNetworkInterfaces([]*string{&eniID})
			// Re initialize the slice so we don't add IPs multiple time
			assignedIPPrefixes = []string{}
			if err == nil && len(interfaces) == 1 && interfaces[0].Ipv4Prefixes != nil {
				// Get the map of IP prefixes returned by the describe network interface call
				ipAddress := map[string]bool{}
				for _, ipAddr := range interfaces[0].Ipv4Prefixes {
					ipAddress[*ipAddr.Ipv4Prefix] = true
				}
				// Verify the describe network interface returns all the IPs that were assigned in the
				// AssignPrivateIPAddresses call
				for _, ip := range assignPrivateIPOutput.AssignedIpv4Prefixes {
					if _, ok := ipAddress[*ip.Ipv4Prefix]; !ok {
						// Even if one IP prefix is not assigned, set the error so that we only return only the IP prefixes that
						// are successfully assigned on the ENI
						err = ErrIPNotAttachedYet
					} else {
						assignedIPPrefixes = append(assignedIPPrefixes, *ip.Ipv4Prefix)
					}
				}
				//
				return err
			}
			return err
		})

	if err != nil {
		// If some of the assigned IP prefixes were not yet returned in the describe network interface call,
		// returns the list of IP prefixes that were returned
		return assignedIPPrefixes, err
	}

	return assignedIPPrefixes, nil
}

func (h *ec2APIHelper) UnassignPrivateIpPrefixes(eniID string, ipPrefixes []string) error {
	unassignPrivateIpAddressesInput := &ec2.UnassignPrivateIpAddressesInput{
		NetworkInterfaceId: &eniID,
		Ipv4Prefixes: aws.StringSlice(ipPrefixes),
	}
	_, err := h.ec2Wrapper.UnassignPrivateIPAddresses(unassignPrivateIpAddressesInput)
	return err
}

func (h *ec2APIHelper) GetBranchNetworkInterface(trunkID *string) ([]*ec2.NetworkInterface, error) {
	filters := []*ec2.Filter{{
		Name:   aws.String("tag:" + config.TrunkENIIDTag),
		Values: []*string{trunkID},
	}}

	describeNetworkInterfacesInput := &ec2.DescribeNetworkInterfacesInput{Filters: filters}
	var nwInterfaces []*ec2.NetworkInterface
	for {
		describeNetworkInterfaceOutput, err := h.ec2Wrapper.DescribeNetworkInterfaces(describeNetworkInterfacesInput)
		if err != nil {
			return nil, err
		}

		if describeNetworkInterfaceOutput == nil || describeNetworkInterfaceOutput.NetworkInterfaces == nil ||
			len(describeNetworkInterfaceOutput.NetworkInterfaces) == 0 {
			// No more interface associated with the trunk, return the result
			break
		}

		// One or more interface associated with the trunk, return the result
		for _, nwInterface := range describeNetworkInterfaceOutput.NetworkInterfaces {
			// Only attach the required details to avoid consuming extra memory
			nwInterfaces = append(nwInterfaces, &ec2.NetworkInterface{
				NetworkInterfaceId: nwInterface.NetworkInterfaceId,
				TagSet:             nwInterface.TagSet,
			})
		}

		if describeNetworkInterfaceOutput.NextToken == nil {
			break
		}

		describeNetworkInterfacesInput.NextToken = describeNetworkInterfaceOutput.NextToken
	}

	return nwInterfaces, nil
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
