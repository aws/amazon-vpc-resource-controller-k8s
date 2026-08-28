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

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/go-logr/logr"
)

// ec2Instance stores all the information that can be shared across the providers for an instance
type ec2Instance struct {
	// log is the logger for the instance
	log logr.Logger
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

	// connectionTracking* fields cache the primary ENI's connection tracking
	// configuration, applied to branch ENIs created on this instance
	tcpEstablishedTimeout *int32
	udpStreamTimeout      *int32
	udpTimeout            *int32

	// hydrated is true when the instance details were rebuilt from a CNINode
	// snapshot (LoadFromSnapshot) instead of an EC2 DescribeInstances call.
	hydrated bool
	// hydratedTrunkID is the trunk ENI id carried from that snapshot, used to
	// rebuild the trunk without EC2. Empty unless hydrated.
	hydratedTrunkID string
}

// EC2Instance exposes the immutable details of an ec2 instance and common operations on an EC2 Instance
type EC2Instance interface {
	LoadDetails(ec2APIHelper api.EC2APIHelper) error
	GetHighestUnusedDeviceIndex() (int32, error)
	FreeDeviceIndex(index int32)
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
	GetConnectionTrackingSpec() (tcpEstablishedTimeout, udpStreamTimeout, udpTimeout *int32)
	LoadFromCNINode(cniNode *rcv1alpha1.CNINode, helper api.EC2APIHelper) error
	IsHydrated() bool
	HydratedTrunkID() string
	InstanceSubnetID() string
	InstanceSubnetCidrBlock() string
	InstanceSubnetV6CidrBlock() string
	PrimaryENISecurityGroups() []string
}

// NewEC2Instance returns a new EC2 Instance type
func NewEC2Instance(nodeName string, instanceID string, os string, log logr.Logger) EC2Instance {
	return &ec2Instance{
		name:       nodeName,
		os:         os,
		instanceID: instanceID,
		log:        log,
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

	i.instanceType = string(instance.InstanceType)
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
		index := aws.ToInt32(nwInterface.Attachment.DeviceIndex)
		i.deviceIndexes[index] = true

		// Load the Security group of the primary network interface
		if i.primaryENISecurityGroups == nil && (nwInterface.PrivateIpAddress != nil && instance.PrivateIpAddress != nil && *nwInterface.PrivateIpAddress == *instance.PrivateIpAddress) {
			i.primaryENIID = *nwInterface.NetworkInterfaceId
			// TODO: Group can change, should be refreshed each time we want to use this
			for _, group := range nwInterface.Groups {
				i.primaryENISecurityGroups = append(i.primaryENISecurityGroups, *group.GroupId)
			}
		}

		// Get the connection tracking configuration from the primary ENI
		if index == 0 {
			if nwInterface.ConnectionTrackingConfiguration != nil {
				i.tcpEstablishedTimeout = nwInterface.ConnectionTrackingConfiguration.TcpEstablishedTimeout
				i.udpStreamTimeout = nwInterface.ConnectionTrackingConfiguration.UdpStreamTimeout
				i.udpTimeout = nwInterface.ConnectionTrackingConfiguration.UdpTimeout
				i.log.Info("instance has connection tracking settings",
					"instanceID", i.instanceID,
					"tcpEstablishedTimeout", i.tcpEstablishedTimeout,
					"udpStreamTimeout", i.udpStreamTimeout,
					"udpTimeout", i.udpTimeout)
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
func (i *ec2Instance) GetHighestUnusedDeviceIndex() (int32, error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	for index := len(i.deviceIndexes) - 1; index >= 0; index-- {
		if i.deviceIndexes[index] == false {
			i.deviceIndexes[index] = true
			return utils.IntToInt32(index)
		}
	}
	return 0, fmt.Errorf("no free device index found")
}

// FreeDeviceIndex frees a device index from the list of managed index
func (i *ec2Instance) FreeDeviceIndex(index int32) {
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

func (i *ec2Instance) GetConnectionTrackingSpec() (tcpEstablished, udpStream, udp *int32) {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.tcpEstablishedTimeout, i.udpStreamTimeout, i.udpTimeout
}

// LoadFromCNINode rebuilds the instance details from a persisted CNINode status
// snapshot instead of EC2 DescribeInstances/DescribeSubnets calls. The snapshot
// carries the instance's source-of-truth fields (its own subnet and primary ENI
// security groups); the effective (current*) fields are then derived through the
// same updateCurrentSubnetAndCidrBlock path LoadDetails uses, so every later
// re-derivation (UpdateResources) is idempotent. For a custom-networking node
// the derivation may make one EC2 GetSubnet call (ENIConfig carries no CIDR);
// on a derivation error the instance is left un-hydrated so the caller falls
// back to EC2 discovery. Device indexes are intentionally left unset (a
// hydrated node does not create a new trunk) and the primary ENI id is not
// carried (only consumed by the Windows secondary-IP provider, whose nodes
// have no trunk and therefore never hydrate). The caller must validate the
// snapshot (trunk interface present, instance id match, supported instance
// type) before calling this.
func (i *ec2Instance) LoadFromCNINode(cniNode *rcv1alpha1.CNINode, helper api.EC2APIHelper) error {
	i.lock.Lock()
	defer i.lock.Unlock()

	status := cniNode.Status
	trunk := status.TrunkInterface

	i.instanceType = status.InstanceType
	i.instanceSubnetID = trunk.SubnetID
	i.instanceSubnetCidrBlock = trunk.SubnetCIDR
	i.instanceSubnetV6CidrBlock = trunk.SubnetV6CIDR
	if parts := strings.Split(trunk.SubnetCIDR, "/"); len(parts) == 2 {
		i.subnetMask = parts[1]
	}
	if parts := strings.Split(trunk.SubnetV6CIDR, "/"); len(parts) == 2 {
		i.subnetV6Mask = parts[1]
	}
	i.primaryENISecurityGroups = status.SecurityGroups
	if ct := status.ConnectionTracking; ct != nil {
		i.tcpEstablishedTimeout = ct.TCPEstablishedTimeout
		i.udpStreamTimeout = ct.UDPStreamTimeout
		i.udpTimeout = ct.UDPTimeout
	}

	// Derive the effective subnet/CIDR/security groups exactly like LoadDetails
	// does as its last step.
	if err := i.updateCurrentSubnetAndCidrBlock(helper); err != nil {
		return err
	}

	i.hydratedTrunkID = trunk.ID
	i.hydrated = true
	return nil
}

// IsHydrated reports whether the instance details came from a CNINode snapshot.
func (i *ec2Instance) IsHydrated() bool {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.hydrated
}

// HydratedTrunkID returns the trunk ENI id from the CNINode snapshot, or "" if
// the instance was not hydrated.
func (i *ec2Instance) HydratedTrunkID() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.hydratedTrunkID
}

// InstanceSubnetID returns the instance's own subnet id (the source-of-truth
// value, not the effective one that may point to an ENIConfig subnet).
func (i *ec2Instance) InstanceSubnetID() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.instanceSubnetID
}

// InstanceSubnetCidrBlock returns the CIDR block of the instance's own subnet.
func (i *ec2Instance) InstanceSubnetCidrBlock() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.instanceSubnetCidrBlock
}

// InstanceSubnetV6CidrBlock returns the IPv6 CIDR block of the instance's own
// subnet, or "" if the subnet has none.
func (i *ec2Instance) InstanceSubnetV6CidrBlock() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.instanceSubnetV6CidrBlock
}

// PrimaryENISecurityGroups returns the security groups of the instance's
// primary network interface (the source-of-truth value, not the effective one
// that may point to ENIConfig security groups).
func (i *ec2Instance) PrimaryENISecurityGroups() []string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.primaryENISecurityGroups
}
