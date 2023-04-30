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

package eni

import (
	"fmt"
	"strings"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/go-logr/logr"
)

var (
	ENIDescription = "aws-k8s-eni"
)

type eniManager struct {
	// instance is the pointer to the instance details
	instance ec2.EC2Instance
	// lock to prevent multiple routines concurrently accessing the eni for same node
	lock sync.Mutex // lock guards the following resources
	// attachedENIs is the list of ENIs attached to the instance
	attachedENIs []*eni
	// resourceToENIMap is the map from IPv4 address or prefix to the ENI that it belongs to
	resourceToENIMap map[string]*eni
}

// eniDetails stores the eniID along with the number of new IPs that can be assigned form it
type eni struct {
	eniID             string
	remainingCapacity int
}

type ENIManager interface {
	InitResources(ec2APIHelper api.EC2APIHelper) ([]string, []string, error)
	CreateIPV4Resource(required int, resourceType api.ResourceType, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error)
	DeleteIPV4Resource(ipList []string, resourceType api.ResourceType, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error)
}

// NewENIManager returns a new ENI Manager
func NewENIManager(instance ec2.EC2Instance) *eniManager {
	return &eniManager{
		resourceToENIMap: map[string]*eni{},
		instance:         instance,
	}
}

// InitResources loads the list of ENIs, secondary IPs and prefixes, associated with the instance
func (e *eniManager) InitResources(ec2APIHelper api.EC2APIHelper) ([]string, []string, error) {

	nwInterfaces, err := ec2APIHelper.GetInstanceNetworkInterface(aws.String(e.instance.InstanceID()))
	if err != nil {
		return nil, nil, err
	}

	limits, found := vpc.Limits[e.instance.Type()]
	if !found {
		return nil, nil, fmt.Errorf("unsupported instance type")
	}

	ipLimit := limits.IPv4PerInterface
	var availIPs []string
	var availPrefixes []string
	for _, nwInterface := range nwInterfaces {
		if nwInterface.PrivateIpAddresses != nil {
			eni := &eni{
				remainingCapacity: ipLimit,
				eniID:             *nwInterface.NetworkInterfaceId,
			}
			// loop through assigned IPv4 addresses and store into map
			for _, ip := range nwInterface.PrivateIpAddresses {
				if *ip.Primary != true {
					availIPs = append(availIPs, *ip.PrivateIpAddress)
					e.resourceToENIMap[*ip.PrivateIpAddress] = eni
				}
				eni.remainingCapacity--
			}
			// loop through assigned IPv4 prefixes and store into map
			if nwInterface.Ipv4Prefixes != nil {
				for _, prefix := range nwInterface.Ipv4Prefixes {
					availPrefixes = append(availPrefixes, *prefix.Ipv4Prefix)
					e.resourceToENIMap[*prefix.Ipv4Prefix] = eni
					eni.remainingCapacity--
				}
			}
			e.attachedENIs = append(e.attachedENIs, eni)
		}
	}

	return e.addSubnetMaskToIPSlice(availIPs), availPrefixes, nil
}

// CreateIPV4Resource creates either IPv4 address or IPv4 prefix depending on ResourceType and returns the list of assigned resources
// along with the error if not all the required resources were assigned
func (e *eniManager) CreateIPV4Resource(required int, resourceType api.ResourceType, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	var assignedIPv4Resources []string
	log = log.WithValues("node name", e.instance.Name())

	// Loop till we reach the last available ENI and list of assigned IPv4 resources is equal to the required resources
	for index := 0; index < len(e.attachedENIs) && len(assignedIPv4Resources) < required; index++ {
		remainingCapacity := e.attachedENIs[index].remainingCapacity
		if remainingCapacity > 0 {
			canAssign := 0
			// Number of resources wanted is the number of resources required minus the number of resources assigned till now
			want := required - len(assignedIPv4Resources)
			// Cannot fulfil the entire request using this ENI, allocate whatever the ENI can assign
			if remainingCapacity < want {
				canAssign = remainingCapacity
			} else {
				canAssign = want
			}
			// Assign the IPv4 resource from this ENI
			assigned, err := ec2APIHelper.AssignIPv4ResourcesAndWaitTillReady(e.attachedENIs[index].eniID, resourceType, canAssign)
			if err != nil && len(assigned) == 0 {
				// Return the list of resources that were actually created along with the error
				return assigned, err
			} else if err != nil {
				// Just log and continue processing the assigned resources
				log.Error(err, "failed to assign all the requested resources",
					"requested", want, "got", len(assigned))
			}
			// Update the remaining capacity
			e.attachedENIs[index].remainingCapacity = remainingCapacity - canAssign
			// Append the assigned IPs on this ENI to the list of IPs created across all the ENIs
			assignedIPv4Resources = append(assignedIPv4Resources, assigned...)
			// Add the mapping from IP to ENI, so that we can easily delete the IP and increment the remaining IP count
			// on the ENI
			for _, resource := range assigned {
				e.resourceToENIMap[resource] = e.attachedENIs[index]
			}

			log.Info("assigned IPv4 resources", "resource type", resourceType, "resources", assigned,
				"eni", e.attachedENIs[index].eniID, "want", want, "can provide upto", canAssign)
		}
	}

	// Number of secondary IPs or IPv4 prefixes supported minus the primary IP
	ipLimit := vpc.Limits[e.instance.Type()].IPv4PerInterface - 1
	eniLimit := vpc.Limits[e.instance.Type()].Interface

	// If the existing ENIs could not assign the required resources, loop till the new ENIs can assign the required
	// number of IPv4 resources
	for len(assignedIPv4Resources) < required &&
		len(e.attachedENIs) < eniLimit {
		deviceIndex, err := e.instance.GetHighestUnusedDeviceIndex()
		if err != nil {
			// TODO: Refresh device index for linux nodes only
			return assignedIPv4Resources, err
		}
		want := required - len(assignedIPv4Resources)
		if want > ipLimit {
			want = ipLimit
		}

		// Create new ENI and store newly assigned resources into map
		switch resourceType {
		case api.ResourceTypeIPv4Address:
			nwInterface, err := ec2APIHelper.CreateAndAttachNetworkInterface(aws.String(e.instance.InstanceID()),
				aws.String(e.instance.SubnetID()), e.instance.InstanceSecurityGroup(), nil, aws.Int64(deviceIndex),
				&ENIDescription, nil, want, 0)
			if err != nil {
				// TODO: Check if any clean up is required here for linux nodes only?
				return assignedIPv4Resources, err
			}
			eni := &eni{
				remainingCapacity: ipLimit - want,
				eniID:             *nwInterface.NetworkInterfaceId,
			}
			e.attachedENIs = append(e.attachedENIs, eni)
			for _, assignedIP := range nwInterface.PrivateIpAddresses {
				if !*assignedIP.Primary {
					assignedIPv4Resources = append(assignedIPv4Resources, *assignedIP.PrivateIpAddress)
					// Also add the mapping from IP to ENI
					e.resourceToENIMap[*assignedIP.PrivateIpAddress] = eni
				}
			}

		case api.ResourceTypeIPv4Prefix:
			nwInterface, err := ec2APIHelper.CreateAndAttachNetworkInterface(aws.String(e.instance.InstanceID()),
				aws.String(e.instance.SubnetID()), e.instance.InstanceSecurityGroup(), nil, aws.Int64(deviceIndex),
				&ENIDescription, nil, 0, want)
			if err != nil {
				// TODO: Check if any clean up is required here for linux nodes only?
				return assignedIPv4Resources, err
			}
			eni := &eni{
				remainingCapacity: ipLimit - want,
				eniID:             *nwInterface.NetworkInterfaceId,
			}
			e.attachedENIs = append(e.attachedENIs, eni)
			for _, assignedPrefix := range nwInterface.Ipv4Prefixes {
				assignedIPv4Resources = append(assignedIPv4Resources, *assignedPrefix.Ipv4Prefix)
				// Also add the mapping from Prefix to ENI
				e.resourceToENIMap[*assignedPrefix.Ipv4Prefix] = eni
			}
		}
	}

	var err error
	// This can happen if the subnet doesn't have remaining IPs
	if len(assignedIPv4Resources) < required {
		err = fmt.Errorf("not able to create the desired number of %s, required %d, created %d",
			resourceType, required, len(assignedIPv4Resources))
	}

	// add subnet mask to assigned IP
	if resourceType == api.ResourceTypeIPv4Address {
		assignedIPv4Resources = e.addSubnetMaskToIPSlice(assignedIPv4Resources)
	}

	return assignedIPv4Resources, err
}

// DeleteIPV4Resource deletes the list of IPv4 resources depending on resource type and returns the list of resources
// that failed to delete along with the error
func (e *eniManager) DeleteIPV4Resource(resourceList []string, resourceType api.ResourceType, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	var failedToUnAssign []string
	var errors []error

	log = log.WithValues("node name", e.instance.Name())

	if resourceList == nil || len(resourceList) == 0 {
		return resourceList, fmt.Errorf("failed to unassign since resourceList is empty")
	}

	// IP address needs to have /19 suffix, whereas prefix already has /28 suffix
	if resourceType == api.ResourceTypeIPv4Address {
		resourceList = e.stripSubnetMaskFromIPSlice(resourceList)
	}
	groupedResources := e.groupResourcesPerENI(resourceList)

	for eni, resources := range groupedResources {
		err := ec2APIHelper.UnassignIPv4Resources(eni.eniID, resourceType, resources)
		if err != nil {
			errors = append(errors, err)
			log.Info("failed to deleted IPv4 resources", "eni", eni.eniID, "resource type", resourceType,
				"resources", resources)
			failedToUnAssign = append(failedToUnAssign, resources...)
			continue
		}
		eni.remainingCapacity += len(resources)
		for _, resource := range resources {
			delete(e.resourceToENIMap, resource)
		}
		log.Info("deleted IPv4 resources", "eni", eni.eniID, "resource type", resourceType, "resources", resources)
	}

	ipLimit := vpc.Limits[e.instance.Type()].IPv4PerInterface - 1
	primaryENIID := e.instance.PrimaryNetworkInterfaceID()

	// Clean up ENIs that just have the primary network interface attached to them
	i := 0
	for _, eni := range e.attachedENIs {
		// ENI doesn't have any secondary IP or prefix attached to it and is not the primary network interface
		if eni.remainingCapacity == ipLimit && primaryENIID != eni.eniID {
			err := ec2APIHelper.DeleteNetworkInterface(&eni.eniID)
			if err != nil {
				errors = append(errors, err)
				e.attachedENIs[i] = eni
				i++
				continue
			}
			log.Info("deleted ENI successfully as it has no secondary IP or prefix attached",
				"id", eni.eniID)
		} else {
			e.attachedENIs[i] = eni
			i++
		}
	}
	e.attachedENIs = e.attachedENIs[:i]

	if errors != nil && len(errors) > 0 {
		return failedToUnAssign, fmt.Errorf("failed to unassign one or more %s: %v", resourceType, errors)
	}

	return nil, nil
}

// groupResourcesPerENI groups the resources to delete per ENI
func (e *eniManager) groupResourcesPerENI(deleteList []string) map[*eni][]string {
	toDelete := map[*eni][]string{}
	for _, resource := range deleteList {
		eni := e.resourceToENIMap[resource]
		ls := toDelete[eni]
		ls = append(ls, resource)
		toDelete[eni] = ls
	}

	return toDelete
}

func (e *eniManager) addSubnetMaskToIPSlice(ipAddresses []string) []string {
	for i := 0; i < len(ipAddresses); i++ {
		ipAddresses[i] = ipAddresses[i] + "/" + e.instance.SubnetMask()
	}
	return ipAddresses
}

func (e *eniManager) stripSubnetMaskFromIPSlice(ipAddresses []string) []string {
	for i := 0; i < len(ipAddresses); i++ {
		ipAddresses[i] = strings.Split(ipAddresses[i], "/")[0]
	}
	return ipAddresses
}
