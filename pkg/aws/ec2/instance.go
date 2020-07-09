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
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
)

// ec2Instance stores all the information that can be shared across the providers for an instance
type ec2Instance struct {
	// lock is to prevent concurrent writes to the fields of the ec2Instance
	lock sync.Mutex
	// name is the k8s name of the node
	name string
	// os is the operating system of the worker node
	os string
	// instanceId of the worker node
	instanceID string
	// instanceType is the EC2 instance type
	instanceType string
	// subnetId is the instance's subnet id
	subnetID string
	// subnetCidrBlock is the cidr block of the instance's subnet
	subnetCidrBlock string
	// deviceIndexes is the list of indexes used by the EC2 Instance
	deviceIndexes []bool
	// instanceSecurityGroups is the security group used by the primary network interface
	instanceSecurityGroups []string
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
	SetSubnet(subnetID string)
	SubnetCidrBlock() string
	InstanceSecurityGroup() []string
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
	// Custom networking is not set, must get the instance subnet id
	if i.subnetID == "" {
		i.subnetID = *instance.SubnetId
	}
	i.instanceType = *instance.InstanceType

	subnet, err := ec2APIHelper.GetSubnet(&i.subnetID)
	if err != nil {
		return err
	}
	i.subnetCidrBlock = *subnet.CidrBlock

	maxENIs, ok := vpc.InstanceENIsAvailable[i.instanceType]
	if !ok {
		return fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s", i.instanceType)
	}

	i.deviceIndexes = make([]bool, maxENIs)
	for _, nwInterface := range instance.NetworkInterfaces {
		index := nwInterface.Attachment.DeviceIndex
		i.deviceIndexes[*index] = true

		// Load the Security group of the primary network interface
		if i.instanceSecurityGroups == nil && *nwInterface.PrivateIpAddress == *instance.PrivateIpAddress {
			// TODO: Group can change, should be refreshed each time we want to use this
			for _, group := range nwInterface.Groups {
				i.instanceSecurityGroups = append(i.instanceSecurityGroups, *group.GroupId)
			}
		}
	}

	return nil
}

// SetSubnet sets the subnet ID in case the node is using cni custom networking
func (i *ec2Instance) SetSubnet(subnetID string) {
	i.subnetID = subnetID
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
	return i.subnetID
}

// SubnetCidrBlock returns the subnet cidr block of the instance
func (i *ec2Instance) SubnetCidrBlock() string {
	return i.subnetCidrBlock
}

// Name returns the name of the node
func (i *ec2Instance) Name() string {
	return i.name
}

// Type returns the instance type of the node
func (i *ec2Instance) Type() string {
	return i.instanceType
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
