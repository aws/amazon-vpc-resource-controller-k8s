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

// connectionTrackingState contains the primary ENI connection tracking
// configuration copied to new branch ENIs.
type connectionTrackingState struct {
	tcpEstablishedTimeout *int32
	udpStreamTimeout      *int32
	udpTimeout            *int32
}

// instanceSourceState contains the instance network state loaded from EC2 or
// restored from NodeNetworkState.
type instanceSourceState struct {
	instanceType          string
	subnetID              string
	subnetCIDRBlock       string
	subnetV6CIDRBlock     string
	subnetMask            string
	subnetV6Mask          string
	primarySecurityGroups []string
	primaryENIID          string
	connectionTracking    connectionTrackingState
}

// effectiveNetworkState contains the subnet and security groups currently used
// for ENI creation after applying the active ENIConfig.
type effectiveNetworkState struct {
	subnetID          string
	subnetCIDRBlock   string
	subnetV6CIDRBlock string
	securityGroups    []string
}

// restoreState records process-local metadata for a NodeNetworkState restore.
type restoreState struct {
	fromNodeNetworkState bool
	trunkENIID           string
}

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
	// source is the instance network state loaded from EC2 or NodeNetworkState.
	source instanceSourceState
	// current is the effective network state after applying ENIConfig.
	current effectiveNetworkState
	// restore records whether source was restored and the corresponding trunk.
	restore restoreState
	// deviceIndexes is the list of indexes used by the EC2 Instance
	deviceIndexes []bool
	// newCustomNetworkingSubnetID is the SubnetID from the ENIConfig
	newCustomNetworkingSubnetID string
	// newCustomNetworkingSecurityGroups is the security groups from the ENIConfig
	newCustomNetworkingSecurityGroups []string
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
	LoadFromNodeNetworkState(state rcv1alpha1.NodeNetworkState, instanceType string, trunkENIID string)
	BuildNodeNetworkState() rcv1alpha1.NodeNetworkState
	IsRestoredFromNodeNetworkState() bool
	RestoredTrunkENIID() string
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

	// Replace all loadable state before rebuilding from the authoritative EC2
	// view. ENIConfig inputs and device-index allocation remain independent.
	i.source = instanceSourceState{}
	i.current = effectiveNetworkState{}
	i.restore = restoreState{}

	// Set instance subnet and cidr during node initialization
	i.source.subnetID = *instance.SubnetId
	instanceSubnet, err := ec2APIHelper.GetSubnet(&i.source.subnetID)
	if err != nil {
		return err
	}
	if instanceSubnet == nil || instanceSubnet.CidrBlock == nil {
		return fmt.Errorf("failed to find subnet or CIDR block for subnet %s for instance %s",
			i.source.subnetID, i.instanceID)
	}
	i.source.subnetCIDRBlock = *instanceSubnet.CidrBlock
	i.source.subnetMask = strings.Split(i.source.subnetCIDRBlock, "/")[1]
	// Cache IPv6 CIDR block if one is present
	for _, v6CidrBlock := range instanceSubnet.Ipv6CidrBlockAssociationSet {
		if v6CidrBlock.Ipv6CidrBlock != nil {
			i.source.subnetV6CIDRBlock = *v6CidrBlock.Ipv6CidrBlock
			i.source.subnetV6Mask = strings.Split(i.source.subnetV6CIDRBlock, "/")[1]
			break
		}
	}

	i.source.instanceType = string(instance.InstanceType)
	limits, ok := vpc.Limits[i.source.instanceType]
	if !ok {
		return fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s, error: %w", i.source.instanceType, utils.ErrNotFound)
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
		return fmt.Errorf("didn't find valid network card with max interface limit from limit file for instance type %s", i.source.instanceType)
	}

	// currently CNI and this controller both only support single network card
	// we want to make sure to use the smaller number between instance max supported interfaces and the default card max supported interfaces
	maxInterfaces := utils.Minimum(int64(limits.Interface), defaultNetworkCardLimit)

	i.deviceIndexes = make([]bool, int(maxInterfaces))
	for _, nwInterface := range instance.NetworkInterfaces {
		index := aws.ToInt32(nwInterface.Attachment.DeviceIndex)
		i.deviceIndexes[index] = true

		// Load the Security group of the primary network interface
		if i.source.primarySecurityGroups == nil && (nwInterface.PrivateIpAddress != nil && instance.PrivateIpAddress != nil && *nwInterface.PrivateIpAddress == *instance.PrivateIpAddress) {
			i.source.primaryENIID = *nwInterface.NetworkInterfaceId
			// TODO: Group can change, should be refreshed each time we want to use this
			for _, group := range nwInterface.Groups {
				i.source.primarySecurityGroups = append(i.source.primarySecurityGroups, *group.GroupId)
			}
		}

		// Get the connection tracking configuration from the primary ENI
		if index == 0 {
			if nwInterface.ConnectionTrackingConfiguration != nil {
				i.source.connectionTracking = connectionTrackingState{
					tcpEstablishedTimeout: nwInterface.ConnectionTrackingConfiguration.TcpEstablishedTimeout,
					udpStreamTimeout:      nwInterface.ConnectionTrackingConfiguration.UdpStreamTimeout,
					udpTimeout:            nwInterface.ConnectionTrackingConfiguration.UdpTimeout,
				}
				i.log.Info("instance has connection tracking settings",
					"instanceID", i.instanceID,
					"tcpEstablishedTimeout", i.source.connectionTracking.tcpEstablishedTimeout,
					"udpStreamTimeout", i.source.connectionTracking.udpStreamTimeout,
					"udpTimeout", i.source.connectionTracking.udpTimeout)
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

	return i.current.subnetID
}

// SubnetCidrBlock returns the subnet cidr block of the instance
func (i *ec2Instance) SubnetCidrBlock() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.current.subnetCIDRBlock
}

func (i *ec2Instance) SubnetV6CidrBlock() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.current.subnetV6CIDRBlock
}

// Name returns the name of the node
func (i *ec2Instance) Name() string {
	return i.name
}

// Type returns the instance type of the node
func (i *ec2Instance) Type() string {
	return i.source.instanceType
}

func (i *ec2Instance) PrimaryNetworkInterfaceID() string {
	return i.source.primaryENIID
}

// CurrentInstanceSecurityGroups returns the current instance security groups
// (primary network interface SG or SG specified in the ENIConfig)
func (i *ec2Instance) CurrentInstanceSecurityGroups() []string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.current.securityGroups
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

	return i.source.subnetMask
}

func (i *ec2Instance) SubnetV6Mask() string {
	i.lock.Lock()
	defer i.lock.Unlock()

	return i.source.subnetV6Mask
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
	// Custom networking uses the subnet and security groups from ENIConfig.
	if i.newCustomNetworkingSubnetID != "" {
		if i.newCustomNetworkingSecurityGroups != nil && len(i.newCustomNetworkingSecurityGroups) > 0 {
			i.current.securityGroups = i.newCustomNetworkingSecurityGroups
		} else {
			// when security groups are not specified in ENIConfig, use the primary network interface SG as per custom networking documentation
			i.current.securityGroups = i.source.primarySecurityGroups
		}
		// Only get the subnet CIDR block again if the subnet ID has changed
		if i.newCustomNetworkingSubnetID != i.current.subnetID {
			customSubnet, err := ec2APIHelper.GetSubnet(&i.newCustomNetworkingSubnetID)
			if err != nil {
				return err
			}
			if customSubnet == nil || customSubnet.CidrBlock == nil {
				return fmt.Errorf("failed to find subnet %s", i.newCustomNetworkingSubnetID)
			}
			i.current.subnetID = i.newCustomNetworkingSubnetID
			i.current.subnetCIDRBlock = *customSubnet.CidrBlock
			// NOTE: IPv6 does not support custom networking
		}
	} else {
		// Custom networking in not being used, point to the primary network interface security group and
		// subnet details
		i.current = effectiveNetworkState{
			subnetID:          i.source.subnetID,
			subnetCIDRBlock:   i.source.subnetCIDRBlock,
			subnetV6CIDRBlock: i.source.subnetV6CIDRBlock,
			securityGroups:    i.source.primarySecurityGroups,
		}
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

	return i.source.connectionTracking.tcpEstablishedTimeout,
		i.source.connectionTracking.udpStreamTimeout,
		i.source.connectionTracking.udpTimeout
}

// prefixLengthFromCIDR returns the prefix length from a CIDR, such as "16" for
// "10.0.0.0/16". It returns an empty string for empty or malformed input.
func prefixLengthFromCIDR(cidr string) string {
	if parts := strings.Split(cidr, "/"); len(parts) == 2 {
		return parts[1]
	}
	return ""
}

// LoadFromNodeNetworkState loads the persisted EC2 values needed to restore the
// instance. Effective subnet and security group values are derived separately
// from the current ENIConfig. Device indexes and the primary ENI id remain
// unset because restored nodes already have a trunk, and Windows nodes do not
// use this restoration path.
func (i *ec2Instance) LoadFromNodeNetworkState(state rcv1alpha1.NodeNetworkState, instanceType string, trunkENIID string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	source := instanceSourceState{
		instanceType:          instanceType,
		subnetID:              state.SubnetID,
		subnetCIDRBlock:       state.SubnetCIDRBlock,
		subnetV6CIDRBlock:     state.SubnetV6CIDRBlock,
		subnetMask:            prefixLengthFromCIDR(state.SubnetCIDRBlock),
		subnetV6Mask:          prefixLengthFromCIDR(state.SubnetV6CIDRBlock),
		primarySecurityGroups: state.PrimaryNetworkInterfaceSecurityGroups,
	}
	if ct := state.ConnectionTracking; ct != nil {
		source.connectionTracking = connectionTrackingState{
			tcpEstablishedTimeout: ct.TCPEstablishedTimeout,
			udpStreamTimeout:      ct.UDPStreamTimeout,
			udpTimeout:            ct.UDPTimeout,
		}
	}

	i.source = source
	i.current = effectiveNetworkState{}
	i.restore = restoreState{
		fromNodeNetworkState: true,
		trunkENIID:           trunkENIID,
	}
}

// BuildNodeNetworkState returns the EC2 values needed to restore this instance.
// It is called after authoritative EC2 initialization.
func (i *ec2Instance) BuildNodeNetworkState() rcv1alpha1.NodeNetworkState {
	i.lock.RLock()
	defer i.lock.RUnlock()

	var connectionTracking *rcv1alpha1.ConnectionTrackingConfig
	if i.source.connectionTracking.tcpEstablishedTimeout != nil ||
		i.source.connectionTracking.udpStreamTimeout != nil ||
		i.source.connectionTracking.udpTimeout != nil {
		connectionTracking = &rcv1alpha1.ConnectionTrackingConfig{
			TCPEstablishedTimeout: i.source.connectionTracking.tcpEstablishedTimeout,
			UDPStreamTimeout:      i.source.connectionTracking.udpStreamTimeout,
			UDPTimeout:            i.source.connectionTracking.udpTimeout,
		}
	}
	return rcv1alpha1.NodeNetworkState{
		InstanceID:                            i.instanceID,
		SubnetID:                              i.source.subnetID,
		SubnetCIDRBlock:                       i.source.subnetCIDRBlock,
		SubnetV6CIDRBlock:                     i.source.subnetV6CIDRBlock,
		PrimaryNetworkInterfaceSecurityGroups: i.source.primarySecurityGroups,
		ConnectionTracking:                    connectionTracking,
	}
}

// IsRestoredFromNodeNetworkState reports whether the instance details were
// restored from NodeNetworkState.
func (i *ec2Instance) IsRestoredFromNodeNetworkState() bool {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.restore.fromNodeNetworkState
}

// RestoredTrunkENIID returns the trunk ENI ID selected for restoration.
func (i *ec2Instance) RestoredTrunkENIID() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.restore.trunkENIID
}
