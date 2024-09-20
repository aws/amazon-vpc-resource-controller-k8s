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
	"strings"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
)

// ec2Instance stores all the information that can be shared across the providers for an instance
type ec2Instance struct {
	// lock is to prevent concurrent writes to the fields of the ec2Instance
	lock sync.RWMutex
	// name is the k8s name of the node
	name string
	// os is the operating system of the worker node
	os string
	// instanceId of the worker node
	instanceID string
	// instanceType is the EC2 instance type
	instanceType string
	// subnetId is the instance's subnet id
	instanceSubnetID string
	// instanceSubnetCidrBlock is the cidr block of the instance's subnet
	instanceSubnetCidrBlock   string
	instanceSubnetV6CidrBlock string
	// currentSubnetID can either point to the Subnet ID of the instance or subnet ID from the ENIConfig
	currentSubnetID string
	// currentSubnetCIDRBlock can either point to the Subnet CIDR block for instance subnet or subnet from ENIConfig
	currentSubnetCIDRBlock   string
	currentSubnetV6CIDRBlock string
	// currentInstanceSecurityGroups can either point to the primary network interface security groups or the security groups in ENIConfig
	currentInstanceSecurityGroups []string
	// subnetMask is the mask of the subnet CIDR block
	subnetMask   string
	subnetV6Mask string
	// deviceIndexes is the list of indexes used by the EC2 Instance
	deviceIndexes []bool
	// primaryENIGroups is the security group used by the primary network interface
	primaryENISecurityGroups []string
	// primaryENIID is the ID of the primary network interface of the instance
	primaryENIID string
	// newCustomNetworkingSubnetID is the SubnetID from the ENIConfig
	newCustomNetworkingSubnetID string
	// newCustomNetworkingSecurityGroups is the security groups from the ENIConfig
	newCustomNetworkingSecurityGroups []string
}

// EC2Instance exposes the immutable details of an ec2 instance and common operations on an EC2 Instance
type EC2Instance interface {
	LoadDetails(ec2APIHelper api.EC2APIHelper) error
	GetHighestUnusedDeviceIndex() (int64, error)
	FreeDeviceIndex(index int64)
	Name() string
	Os() string
	Type() string
	InstanceID() string
	SubnetID() string
	SubnetMask() string
	SubnetV6Mask() string
	SubnetCidrBlock() string
	SubnetV6CidrBlock() string
	PrimaryNetworkInterfaceID() string
	CurrentInstanceSecurityGroups() []string
	SetNewCustomNetworkingSpec(subnetID string, securityGroup []string)
	GetCustomNetworkingSpec() (subnetID string, securityGroup []string)
	UpdateCurrentSubnetAndCidrBlock(helper api.EC2APIHelper) error
}

// NewEC2Instance returns a new EC2 Instance type
func NewEC2Instance(nodeName string, instanceID string, os string) EC2Instance {
	return &ec2Instance{
		name:       nodeName,
		os:         os,
		instanceID: instanceID,
	}
}

// LoadDetails loads the instance details by making an EC2 API call
func (i *ec2Instance) LoadDetails(ec2APIHelper api.EC2APIHelper) error {
	i.lock.Lock()
	defer i.lock.Unlock()

	instance, err := ec2APIHelper.GetInstanceDetails(&i.instanceID)
	if err != nil {
		return err
	}
	if instance == nil || instance.SubnetId == nil {
		return fmt.Errorf("failed to find instance %s details from EC2 API", i.instanceID)
	}

	// Set instance subnet and cidr during node initialization
	i.instanceSubnetID = *instance.SubnetId
	instanceSubnet, err := ec2APIHelper.GetSubnet(&i.instanceSubnetID)
	if err != nil {
		return err
	}
	if instanceSubnet == nil || instanceSubnet.CidrBlock == nil {
		return fmt.Errorf("failed to find subnet or CIDR block for subnet %s for instance %s",
			i.instanceSubnetID, i.instanceID)
	}
	i.instanceSubnetCidrBlock = *instanceSubnet.CidrBlock
	i.subnetMask = strings.Split(i.instanceSubnetCidrBlock, "/")[1]
	// Cache IPv6 CIDR block if one is present
	for _, v6CidrBlock := range instanceSubnet.Ipv6CidrBlockAssociationSet {
		if v6CidrBlock.Ipv6CidrBlock != nil {
			i.instanceSubnetV6CidrBlock = *v6CidrBlock.Ipv6CidrBlock
			i.subnetV6Mask = strings.Split(i.instanceSubnetV6CidrBlock, "/")[1]
			break
		}
	}

	i.instanceType = *instance.InstanceType
	limits, ok := vpc.Limits[i.instanceType]
	if !ok {
		return fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s, error: %w", i.instanceType, utils.ErrNotFound)
	}

	defaultCardIdx := limits.DefaultNetworkCardIndex
	var defaultNetworkCardLimit int64
	for _, card := range limits.NetworkCards {
		if card.NetworkCardIndex == int64(defaultCardIdx) {
			defaultNetworkCardLimit = card.MaximumNetworkInterfaces
			break
		}
	}
	if defaultNetworkCardLimit == 0 {
		return fmt.Errorf("didn't find valid network card with max interface limit from limit file for instance type %s", i.instanceType)
	}

	// currently CNI and this controller both only support single network card
	// we want to make sure to use the smaller number between instance max supported interfaces and the default card max supported interfaces
	maxInterfaces := utils.Minimum(int64(limits.Interface), defaultNetworkCardLimit)

	i.deviceIndexes = make([]bool, int(maxInterfaces))
	for _, nwInterface := range instance.NetworkInterfaces {
		index := nwInterface.Attachment.DeviceIndex
		i.deviceIndexes[*index] = true

		// Load the Security group of the primary network interface
		if i.primaryENISecurityGroups == nil && (nwInterface.PrivateIpAddress != nil && instance.PrivateIpAddress != nil && *nwInterface.PrivateIpAddress == *instance.PrivateIpAddress) {
			i.primaryENIID = *nwInterface.NetworkInterfaceId
			// TODO: Group can change, should be refreshed each time we want to use this
			for _, group := range nwInterface.Groups {
				i.primaryENISecurityGroups = append(i.primaryENISecurityGroups, *group.GroupId)
			}
		}
	}

	return i.updateCurrentSubnetAndCidrBlock(ec2APIHelper)
}

// Os returns the os of the instance
func (i *ec2Instance) Os() string {
	return i.os
}

// InstanceId returns the instance id of the instance
func (i *ec2Instance) InstanceID() string {
	return i.instanceID
}

// SubnetId returns the subnet id of the instance
func (i *ec2Instance) SubnetID() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.currentSubnetID
}

// SubnetCidrBlock returns the subnet cidr block of the instance
func (i *ec2Instance) SubnetCidrBlock() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.currentSubnetCIDRBlock
}

func (i *ec2Instance) SubnetV6CidrBlock() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.currentSubnetV6CIDRBlock
}

// Name returns the name of the node
func (i *ec2Instance) Name() string {
	return i.name
}

// Type returns the instance type of the node
func (i *ec2Instance) Type() string {
	return i.instanceType
}

func (i *ec2Instance) PrimaryNetworkInterfaceID() string {
	return i.primaryENIID
}

// CurrentInstanceSecurityGroups returns the current instance security groups
// (primary network interface SG or SG specified in the ENIConfig)
func (i *ec2Instance) CurrentInstanceSecurityGroups() []string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.currentInstanceSecurityGroups
}

// GetHighestUnusedDeviceIndex assigns a free device index from the end of the list since IPAMD assigns indexes from
// the beginning of the list
func (i *ec2Instance) GetHighestUnusedDeviceIndex() (int64, error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	for index := len(i.deviceIndexes) - 1; index >= 0; index-- {
		if i.deviceIndexes[index] == false {
			i.deviceIndexes[index] = true
			return int64(index), nil
		}
	}
	return 0, fmt.Errorf("no free device index found")
}

// FreeDeviceIndex frees a device index from the list of managed index
func (i *ec2Instance) FreeDeviceIndex(index int64) {
	i.lock.Lock()
	defer i.lock.Unlock()

	i.deviceIndexes[index] = false
}

func (i *ec2Instance) SubnetMask() string {
	i.lock.Lock()
	defer i.lock.Unlock()

	return i.subnetMask
}

func (i *ec2Instance) SubnetV6Mask() string {
	i.lock.Lock()
	defer i.lock.Unlock()

	return i.subnetV6Mask
}

// SetNewCustomNetworkingSpec updates the subnet ID and subnet CIDR block for the instance
func (i *ec2Instance) SetNewCustomNetworkingSpec(subnet string, securityGroups []string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	i.newCustomNetworkingSubnetID = subnet
	i.newCustomNetworkingSecurityGroups = securityGroups
}

// UpdateCurrentSubnetAndCidrBlock updates the subnet details under a write lock
func (i *ec2Instance) UpdateCurrentSubnetAndCidrBlock(ec2APIHelper api.EC2APIHelper) error {
	i.lock.Lock()
	defer i.lock.Unlock()

	return i.updateCurrentSubnetAndCidrBlock(ec2APIHelper)
}

// updateCurrentSubnetAndCidrBlock updates subnet details and security group if the node is
// using custom networking
func (i *ec2Instance) updateCurrentSubnetAndCidrBlock(ec2APIHelper api.EC2APIHelper) error {
	// Custom networking is being used on node, point the current subnet ID, CIDR block and
	// instance security group to the one's present in the Custom networking spec
	if i.newCustomNetworkingSubnetID != "" {
		if i.newCustomNetworkingSecurityGroups != nil && len(i.newCustomNetworkingSecurityGroups) > 0 {
			i.currentInstanceSecurityGroups = i.newCustomNetworkingSecurityGroups
		} else {
			// when security groups are not specified in ENIConfig, use the primary network interface SG as per custom networking documentation
			i.currentInstanceSecurityGroups = i.primaryENISecurityGroups
		}
		// Only get the subnet CIDR block again if the subnet ID has changed
		if i.newCustomNetworkingSubnetID != i.currentSubnetID {
			customSubnet, err := ec2APIHelper.GetSubnet(&i.newCustomNetworkingSubnetID)
			if err != nil {
				return err
			}
			if customSubnet == nil || customSubnet.CidrBlock == nil {
				return fmt.Errorf("failed to find subnet %s", i.newCustomNetworkingSubnetID)
			}
			i.currentSubnetID = i.newCustomNetworkingSubnetID
			i.currentSubnetCIDRBlock = *customSubnet.CidrBlock
			// NOTE: IPv6 does not support custom networking
		}
	} else {
		// Custom networking in not being used, point to the primary network interface security group and
		// subnet details
		i.currentSubnetID = i.instanceSubnetID
		i.currentSubnetCIDRBlock = i.instanceSubnetCidrBlock
		i.currentSubnetV6CIDRBlock = i.instanceSubnetV6CidrBlock
		i.currentInstanceSecurityGroups = i.primaryENISecurityGroups
	}

	return nil
}

func (i *ec2Instance) GetCustomNetworkingSpec() (subnetID string, securityGroup []string) {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.newCustomNetworkingSubnetID, i.newCustomNetworkingSecurityGroups
}
