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
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/go-logr/logr"
)

type connectionTrackingState struct {
	tcpEstablishedTimeout *int32
	udpStreamTimeout      *int32
	udpTimeout            *int32
}

type instanceSourceState struct {
	instanceType       string
	subnetID           string
	subnetCIDR         string
	subnetV6CIDR       string
	subnetMask         string
	subnetV6Mask       string
	primaryENIID       string
	primarySGs         []string
	connectionTracking connectionTrackingState
}

type effectiveNetworkState struct {
	subnetID       string
	subnetCIDR     string
	subnetV6CIDR   string
	securityGroups []string
}

type customNetworkingSpec struct {
	subnetID       string
	securityGroups []string
}

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
	// source is the stable instance state loaded from EC2 or a CNINode checkpoint.
	source instanceSourceState
	// current is the effective network state after applying the current ENIConfig.
	current effectiveNetworkState
	// customNetworking is the desired override read from the current ENIConfig.
	customNetworking customNetworkingSpec
	// restore records whether stable state came from CNINode and the restored trunk identity.
	restore restoreState
	// deviceIndexes is the list of indexes used by the EC2 Instance
	deviceIndexes []bool
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
	LoadFromNodeNetworkState(state rcv1alpha1.NodeNetworkState, trunkENIID string) error
	RestoredTrunkENIID() string
	BuildNodeNetworkState() rcv1alpha1.NodeNetworkState
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
	instance, err := ec2APIHelper.GetInstanceDetails(&i.instanceID)
	if err != nil {
		return err
	}
	if instance == nil || instance.SubnetId == nil {
		return fmt.Errorf("failed to find instance %s details from EC2 API", i.instanceID)
	}

	source := instanceSourceState{
		instanceType: string(instance.InstanceType),
		subnetID:     *instance.SubnetId,
	}
	instanceSubnet, err := ec2APIHelper.GetSubnet(&source.subnetID)
	if err != nil {
		return err
	}
	if instanceSubnet == nil || instanceSubnet.CidrBlock == nil {
		return fmt.Errorf("failed to find subnet or CIDR block for subnet %s for instance %s",
			source.subnetID, i.instanceID)
	}
	source.subnetCIDR = *instanceSubnet.CidrBlock
	source.subnetMask = strings.Split(source.subnetCIDR, "/")[1]
	// Cache IPv6 CIDR block if one is present
	for _, v6CidrBlock := range instanceSubnet.Ipv6CidrBlockAssociationSet {
		if v6CidrBlock.Ipv6CidrBlock != nil {
			source.subnetV6CIDR = *v6CidrBlock.Ipv6CidrBlock
			source.subnetV6Mask = strings.Split(source.subnetV6CIDR, "/")[1]
			break
		}
	}

	limits, ok := vpc.Limits[source.instanceType]
	if !ok {
		return fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s, error: %w", source.instanceType, utils.ErrNotFound)
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
		return fmt.Errorf("didn't find valid network card with max interface limit from limit file for instance type %s", source.instanceType)
	}

	// currently CNI and this controller both only support single network card
	// we want to make sure to use the smaller number between instance max supported interfaces and the default card max supported interfaces
	maxInterfaces := utils.Minimum(int64(limits.Interface), defaultNetworkCardLimit)

	deviceIndexes := make([]bool, int(maxInterfaces))
	for _, nwInterface := range instance.NetworkInterfaces {
		index := aws.ToInt32(nwInterface.Attachment.DeviceIndex)
		deviceIndexes[index] = true

		// Load the Security group of the primary network interface
		if source.primarySGs == nil && (nwInterface.PrivateIpAddress != nil && instance.PrivateIpAddress != nil && *nwInterface.PrivateIpAddress == *instance.PrivateIpAddress) {
			source.primaryENIID = *nwInterface.NetworkInterfaceId
			// TODO: Group can change, should be refreshed each time we want to use this
			for _, group := range nwInterface.Groups {
				source.primarySGs = append(source.primarySGs, *group.GroupId)
			}
		}

		// Get the connection tracking configuration from the primary ENI
		if index == 0 {
			if nwInterface.ConnectionTrackingConfiguration != nil {
				source.connectionTracking = connectionTrackingState{
					tcpEstablishedTimeout: copyInt32(nwInterface.ConnectionTrackingConfiguration.TcpEstablishedTimeout),
					udpStreamTimeout:      copyInt32(nwInterface.ConnectionTrackingConfiguration.UdpStreamTimeout),
					udpTimeout:            copyInt32(nwInterface.ConnectionTrackingConfiguration.UdpTimeout),
				}
				i.log.Info("instance has connection tracking settings",
					"instanceID", i.instanceID,
					"tcpEstablishedTimeout", source.connectionTracking.tcpEstablishedTimeout,
					"udpStreamTimeout", source.connectionTracking.udpStreamTimeout,
					"udpTimeout", source.connectionTracking.udpTimeout)
			}
		}
	}

	i.lock.Lock()
	defer i.lock.Unlock()

	i.source = source
	i.deviceIndexes = deviceIndexes
	i.restore = restoreState{}
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

	return i.current.subnetCIDR
}

func (i *ec2Instance) SubnetV6CidrBlock() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.current.subnetV6CIDR
}

// Name returns the name of the node
func (i *ec2Instance) Name() string {
	return i.name
}

// Type returns the instance type of the node
func (i *ec2Instance) Type() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.source.instanceType
}

func (i *ec2Instance) PrimaryNetworkInterfaceID() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.source.primaryENIID
}

// CurrentInstanceSecurityGroups returns the current instance security groups
// (primary network interface SG or SG specified in the ENIConfig)
func (i *ec2Instance) CurrentInstanceSecurityGroups() []string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return slices.Clone(i.current.securityGroups)
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
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.source.subnetMask
}

func (i *ec2Instance) SubnetV6Mask() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.source.subnetV6Mask
}

// SetNewCustomNetworkingSpec updates the subnet ID and subnet CIDR block for the instance
func (i *ec2Instance) SetNewCustomNetworkingSpec(subnet string, securityGroups []string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	i.customNetworking = customNetworkingSpec{
		subnetID:       subnet,
		securityGroups: slices.Clone(securityGroups),
	}
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
	if i.customNetworking.subnetID != "" {
		if len(i.customNetworking.securityGroups) > 0 {
			i.current.securityGroups = slices.Clone(i.customNetworking.securityGroups)
		} else {
			// when security groups are not specified in ENIConfig, use the primary network interface SG as per custom networking documentation
			i.current.securityGroups = slices.Clone(i.source.primarySGs)
		}
		// Only get the subnet CIDR block again if the subnet ID has changed
		if i.customNetworking.subnetID != i.current.subnetID {
			customSubnet, err := ec2APIHelper.GetSubnet(&i.customNetworking.subnetID)
			if err != nil {
				return err
			}
			if customSubnet == nil || customSubnet.CidrBlock == nil {
				return fmt.Errorf("failed to find subnet %s", i.customNetworking.subnetID)
			}
			i.current.subnetID = i.customNetworking.subnetID
			i.current.subnetCIDR = *customSubnet.CidrBlock
			// NOTE: IPv6 does not support custom networking
		}
	} else {
		// Custom networking in not being used, point to the primary network interface security group and
		// subnet details
		i.current = effectiveNetworkState{
			subnetID:       i.source.subnetID,
			subnetCIDR:     i.source.subnetCIDR,
			subnetV6CIDR:   i.source.subnetV6CIDR,
			securityGroups: slices.Clone(i.source.primarySGs),
		}
	}

	return nil
}

func (i *ec2Instance) GetCustomNetworkingSpec() (subnetID string, securityGroup []string) {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return i.customNetworking.subnetID, slices.Clone(i.customNetworking.securityGroups)
}

func (i *ec2Instance) GetConnectionTrackingSpec() (tcpEstablished, udpStream, udp *int32) {
	i.lock.RLock()
	defer i.lock.RUnlock()

	return copyInt32(i.source.connectionTracking.tcpEstablishedTimeout),
		copyInt32(i.source.connectionTracking.udpStreamTimeout),
		copyInt32(i.source.connectionTracking.udpTimeout)
}

func subnetMaskFromCIDR(cidr string, wantIPv6 bool) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", err
	}
	if prefix.Addr().Is6() != wantIPv6 {
		family := "IPv4"
		if wantIPv6 {
			family = "IPv6"
		}
		return "", fmt.Errorf("expected an %s CIDR", family)
	}
	return strconv.Itoa(prefix.Bits()), nil
}

func copyInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// LoadFromNodeNetworkState restores the stable instance and trunk state persisted in CNINode.
// Current ENIConfig state is applied separately by UpdateCurrentSubnetAndCidrBlock.
func (i *ec2Instance) LoadFromNodeNetworkState(state rcv1alpha1.NodeNetworkState, trunkENIID string) error {
	if state.InstanceID != i.instanceID {
		return fmt.Errorf("checkpoint instance ID %q does not match node instance ID %q", state.InstanceID, i.instanceID)
	}
	if state.InstanceType == "" || state.SubnetID == "" || state.SubnetCIDRBlock == "" ||
		state.PrimaryNetworkInterfaceID == "" || len(state.PrimaryNetworkInterfaceSecurityGroups) == 0 ||
		trunkENIID == "" {
		return fmt.Errorf("checkpoint is missing required stable instance or trunk state")
	}
	if _, ok := vpc.Limits[state.InstanceType]; !ok {
		return fmt.Errorf("unsupported checkpoint instance type %s", state.InstanceType)
	}

	subnetMask, err := subnetMaskFromCIDR(state.SubnetCIDRBlock, false)
	if err != nil {
		return fmt.Errorf("invalid IPv4 CIDR block %q in checkpoint: %w", state.SubnetCIDRBlock, err)
	}
	subnetV6Mask := ""
	if state.SubnetV6CIDRBlock != "" {
		subnetV6Mask, err = subnetMaskFromCIDR(state.SubnetV6CIDRBlock, true)
		if err != nil {
			return fmt.Errorf("invalid IPv6 CIDR block %q in checkpoint: %w", state.SubnetV6CIDRBlock, err)
		}
	}

	i.lock.Lock()
	defer i.lock.Unlock()

	i.source = instanceSourceState{
		instanceType:       state.InstanceType,
		subnetID:           state.SubnetID,
		subnetCIDR:         state.SubnetCIDRBlock,
		subnetV6CIDR:       state.SubnetV6CIDRBlock,
		subnetMask:         subnetMask,
		subnetV6Mask:       subnetV6Mask,
		primaryENIID:       state.PrimaryNetworkInterfaceID,
		primarySGs:         slices.Clone(state.PrimaryNetworkInterfaceSecurityGroups),
		connectionTracking: connectionTrackingFrom(state.ConnectionTracking),
	}
	i.current = effectiveNetworkState{}
	i.restore = restoreState{
		fromNodeNetworkState: true,
		trunkENIID:           trunkENIID,
	}

	return nil
}

func connectionTrackingFrom(config *rcv1alpha1.ConnectionTrackingConfig) connectionTrackingState {
	if config == nil {
		return connectionTrackingState{}
	}
	return connectionTrackingState{
		tcpEstablishedTimeout: copyInt32(config.TCPEstablishedTimeout),
		udpStreamTimeout:      copyInt32(config.UDPStreamTimeout),
		udpTimeout:            copyInt32(config.UDPTimeout),
	}
}

// RestoredTrunkENIID returns the checkpointed trunk ID, or empty for EC2-initialized instances.
func (i *ec2Instance) RestoredTrunkENIID() string {
	i.lock.RLock()
	defer i.lock.RUnlock()

	if !i.restore.fromNodeNetworkState {
		return ""
	}
	return i.restore.trunkENIID
}

// BuildNodeNetworkState returns the stable instance state persisted in CNINode.
func (i *ec2Instance) BuildNodeNetworkState() rcv1alpha1.NodeNetworkState {
	i.lock.RLock()
	defer i.lock.RUnlock()

	var connectionTracking *rcv1alpha1.ConnectionTrackingConfig
	if i.source.connectionTracking.tcpEstablishedTimeout != nil ||
		i.source.connectionTracking.udpStreamTimeout != nil ||
		i.source.connectionTracking.udpTimeout != nil {
		connectionTracking = &rcv1alpha1.ConnectionTrackingConfig{
			TCPEstablishedTimeout: copyInt32(i.source.connectionTracking.tcpEstablishedTimeout),
			UDPStreamTimeout:      copyInt32(i.source.connectionTracking.udpStreamTimeout),
			UDPTimeout:            copyInt32(i.source.connectionTracking.udpTimeout),
		}
	}

	return rcv1alpha1.NodeNetworkState{
		InstanceID:                            i.instanceID,
		InstanceType:                          i.source.instanceType,
		SubnetID:                              i.source.subnetID,
		SubnetCIDRBlock:                       i.source.subnetCIDR,
		SubnetV6CIDRBlock:                     i.source.subnetV6CIDR,
		PrimaryNetworkInterfaceID:             i.source.primaryENIID,
		PrimaryNetworkInterfaceSecurityGroups: slices.Clone(i.source.primarySGs),
		ConnectionTracking:                    connectionTracking,
	}
}
