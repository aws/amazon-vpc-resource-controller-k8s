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

package ec2

import (
	"fmt"
	"strings"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
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
	instanceSubnetCidrBlock string
	// currentSubnetID is the subnet id we are using now
	currentSubnetID string
	//currentSubnetCirdBlock is the cidr block we are using now
	currentSubnetCirdBlock string
	// subnetMask is the mask of the subnet CIDR block
	subnetMask string
	// deviceIndexes is the list of indexes used by the EC2 Instance
	deviceIndexes []bool
	// instanceSecurityGroups is the security group used by the primary network interface
	instanceSecurityGroups []string
	// primaryENIID is the ID of the primary network interface of the instance
	primaryENIID string
	// latest updated custom networking subnet
	newCustomNetworkingSubnetID string
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
	SubnetCidrBlock() string
	PrimaryNetworkInterfaceID() string
	InstanceSecurityGroup() []string
	SetNewCustomNetworkingSubnetID(subnetID string)
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

	// Set instance subnet and cidr during node initialization
	i.instanceSubnetID = *instance.SubnetId
	instanceSubnet, err := ec2APIHelper.GetSubnet(&i.instanceSubnetID)
	if err != nil {
		return err
	}
	i.instanceSubnetCidrBlock = *instanceSubnet.CidrBlock

	if i.newCustomNetworkingSubnetID != "" {
		customSubnet, err := ec2APIHelper.GetSubnet(&i.newCustomNetworkingSubnetID)
		if err != nil {
			return err
		}
		i.currentSubnetID = i.newCustomNetworkingSubnetID
		i.currentSubnetCirdBlock = *customSubnet.CidrBlock
	} else {
		i.currentSubnetID = i.instanceSubnetID
		i.currentSubnetCirdBlock = i.instanceSubnetCidrBlock
	}

	i.subnetMask = strings.Split(i.instanceSubnetCidrBlock, "/")[1]
	i.instanceType = *instance.InstanceType
	limits, ok := vpc.Limits[i.instanceType]
	if !ok {
		return fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s", i.instanceType)
	}

	i.deviceIndexes = make([]bool, limits.Interface)
	for _, nwInterface := range instance.NetworkInterfaces {
		index := nwInterface.Attachment.DeviceIndex
		i.deviceIndexes[*index] = true

		// Load the Security group of the primary network interface
		if i.instanceSecurityGroups == nil && *nwInterface.PrivateIpAddress == *instance.PrivateIpAddress {
			i.primaryENIID = *nwInterface.NetworkInterfaceId
			// TODO: Group can change, should be refreshed each time we want to use this
			for _, group := range nwInterface.Groups {
				i.instanceSecurityGroups = append(i.instanceSecurityGroups, *group.GroupId)
			}
		}
	}

	return nil
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

	return i.currentSubnetCirdBlock
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

// InstanceSecurityGroup returns the instance security group of the primary network interface
func (i *ec2Instance) InstanceSecurityGroup() []string {
	return i.instanceSecurityGroups
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

// SetNewCustomNetworkingSubnetID set custom subnet in instance
func (i *ec2Instance) SetNewCustomNetworkingSubnetID(subnet string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	i.newCustomNetworkingSubnetID = subnet
}

// UpdateCurrentSubnetAndCidrBlock updates subnet and CIDR block same time
func (i *ec2Instance) UpdateCurrentSubnetAndCidrBlock(helper api.EC2APIHelper) error {
	i.lock.Lock()
	defer i.lock.Unlock()

	if i.newCustomNetworkingSubnetID == "" && i.currentSubnetID != i.instanceSubnetID {
		i.currentSubnetID = i.instanceSubnetID
		i.currentSubnetCirdBlock = i.instanceSubnetCidrBlock
	} else if i.newCustomNetworkingSubnetID != "" && i.newCustomNetworkingSubnetID != i.currentSubnetID {
		customSubnet, err := helper.GetSubnet(&i.newCustomNetworkingSubnetID)
		if err != nil {
			return err
		}
		i.currentSubnetID = i.newCustomNetworkingSubnetID
		i.currentSubnetCirdBlock = *customSubnet.CidrBlock
	}
	return nil
}
