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

package eni

import (
	"fmt"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/go-logr/logr"
)

var (
	ENIDescription = "aws-k0s-eni"
)

type eniManager struct {
	// lock to prevent multiple routines concurrently accessing the eni for same node
	lock       sync.Mutex
	// availENIs is the list of ENIs attached to the instance
	availENIs  []*eniDetails
	// ipToENIMap is the map from ip to the ENI that it belongs to
	ipToENIMap map[string]*eniDetails
	// instance is the pointer to the instance details
	instance   ec2.EC2Instance
}

// eniDetails stores the eniID along with the number of new IPs that can be assigned form it
type eniDetails struct {
	eniID             string
	remainingCapacity int
}

type ENIManager interface {
	InitResources(ec2APIHelper api.EC2APIHelper) ([]string, error)
	CreateIPV4Address(required int, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error)
	DeleteIPV4Address(ipList []string, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error)
}

// NewENIManager returns a new ENI Manager
func NewENIManager(instance ec2.EC2Instance) *eniManager {
	return &eniManager{
		ipToENIMap: map[string]*eniDetails{},
		instance:   instance,
	}
}

// InitResources loads the list of ENIs and IPs associated with the instance
func (e *eniManager) InitResources(ec2APIHelper api.EC2APIHelper) ([]string, error) {

	nwInterfaces, err := ec2APIHelper.GetNetworkInterfaceOfInstance(aws.String(e.instance.InstanceID()))
	if err != nil {
		return nil, err
	}

	ipLimit := vpc.InstanceIPsAvailable[e.instance.Type()]
	var availIPs []string
	for _, nwInterface := range nwInterfaces {
		if nwInterface.PrivateIpAddresses != nil {
			eni := &eniDetails{
				remainingCapacity: ipLimit,
				eniID:             *nwInterface.NetworkInterfaceId,
			}
			for _, ip := range nwInterface.PrivateIpAddresses {
				eni.remainingCapacity--
				availIPs = append(availIPs, *ip.PrivateIpAddress)
				e.ipToENIMap[*ip.PrivateIpAddress] = eni
			}
			e.availENIs = append(e.availENIs, eni)
		}
	}

	return availIPs, nil
}

// CreateIPV4Address creates IPv4 address and returns the list of assigned IPs along with the error if not all the required
// IPs were assigned
func (e *eniManager) CreateIPV4Address(required int, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	var assignedIPv4Address []string

	// Loop till we reach the last available ENI and list of assigned IPv4 addresses is less than the required IPv4 addresses
	for index := 0; index < len(e.availENIs) && len(assignedIPv4Address) < required; index++ {
		remainingCapacity := e.availENIs[index].remainingCapacity
		if remainingCapacity > 0 {
			canAssign := 0
			// Number of IPs wanted is the number of IPs required minus the number of IPs assigned till now.
			want := required - len(assignedIPv4Address)
			// Cannot fulfil the entire request using this ENI, allocate whatever the ENI can assign
			if remainingCapacity < want {
				canAssign = remainingCapacity
			} else {
				canAssign = want
			}
			// Assign the IPv4 Addresses from this ENI
			assignedIPs, err := ec2APIHelper.AssignIPv4AddressesAndWaitTillReady(e.availENIs[index].eniID, canAssign)
			if err != nil {
				// Return the list of IPs that were actually created along with the error
				return assignedIPs, err
			}
			// Update the remaining capacity
			e.availENIs[index].remainingCapacity = remainingCapacity - canAssign
			// Append the assigned IPs on this ENI to the list of IPs created across all the ENIs
			assignedIPv4Address = append(assignedIPv4Address, assignedIPs...)
			// Add the mapping from IP to ENI, so that we can easily delete the IP and increment the remaining IP count
			// on the ENI
			e.addIPtoENIMapping(e.availENIs[index], assignedIPs)

			log.Info("assigned IPv4 addresses", "ip", assignedIPs,
				"eni", e.availENIs[index].eniID, "want", want, "can provide up", canAssign)
		}
	}

	// List of secondary IPs supported minus the primary IP
	ipLimit := vpc.InstanceIPsAvailable[e.instance.Type()] - 1
	eniLimit := vpc.InstanceENIsAvailable[e.instance.Type()]

	// If the existing ENIs could not assign the required IPs, loop till the new ENIs can assign the required
	// number of IPv4 Addresses
	for len(assignedIPv4Address) < required &&
		len(e.availENIs) < eniLimit {

		deviceIndex, err := e.instance.GetHighestUnusedDeviceIndex()
		if err != nil {
			// TODO: Refresh device index
			return assignedIPv4Address, err
		}
		want := required - len(assignedIPv4Address)
		if want > ipLimit {
			want = ipLimit
		}
		nwInterface, err := ec2APIHelper.CreateAndAttachNetworkInterface(aws.String(e.instance.InstanceID()),
			aws.String(e.instance.SubnetID()), e.instance.InstanceSecurityGroup(), aws.Int64(deviceIndex),
			&ENIDescription, nil, want)
		if err != nil {
			// TODO: Check if any clean up is required here?
			return assignedIPv4Address, err
		}
		eni := &eniDetails{
			remainingCapacity: ipLimit - want,
			eniID:             *nwInterface.NetworkInterfaceId,
		}
		e.availENIs = append(e.availENIs, eni)
		for _, assignedIP := range nwInterface.PrivateIpAddresses {
			if !*assignedIP.Primary {
				assignedIPv4Address = append(assignedIPv4Address, *assignedIP.PrivateIpAddress)
				// Also add the mapping from IP to ENI
				e.ipToENIMap[*assignedIP.PrivateIpAddress] = eni
			}
		}
	}

	var err error
	// This should never happen
	if len(assignedIPv4Address) < required {
		err = fmt.Errorf("not able to create the desired number of IPv4 addresses, required %d, created %d",
			required, len(assignedIPv4Address))
	}

	return assignedIPv4Address, err
}

// DeleteIPV4Address deletes the list of IPv4 addresses and returns the list of IPs that failed to delete along with the
// error
func (e *eniManager) DeleteIPV4Address(ipList []string, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	var failedToUnAssign []string
	var errors []error
	for _, ip := range ipList {
		eni, ok := e.ipToENIMap[ip]
		if !ok {
			log.Error(fmt.Errorf("failed to find the eni for ip %s", ip), "skipping the IP address deletion")
			continue
		}
		// TODO: Club unassign requests for same ENI into one request for efficiency
		err := ec2APIHelper.UnassignPrivateIpAddresses(eni.eniID, ip)
		if err != nil {
			errors = append(errors, err)
			log.Info("failed to deleted secondary IPv4 address", "eni", eni.eniID, "ip", ip)
			failedToUnAssign = append(failedToUnAssign, ip)
			continue
		}
		log.Info("deleted secondary IPv4 address", "eni", eni.eniID, "ip", ip)
		// Delete the mapping and increment the capacity of the ENI
		delete(e.ipToENIMap, ip)
		eni.remainingCapacity++
	}

	ipLimit := vpc.InstanceIPsAvailable[e.instance.Type()] - 1
	primaryENIID := e.instance.PrimaryNetworkInterfaceID()

	// Clean up ENIs that just have the primary network interface attached to them
	i := 0
	for _, eni := range e.availENIs {
		// ENI doesn't have any secondary IP attached to it and is not the primary network interface
		if eni.remainingCapacity == ipLimit && primaryENIID != eni.eniID {
			err := ec2APIHelper.DeleteNetworkInterface(&eni.eniID)
			if err != nil {
				errors = append(errors, err)
				e.availENIs[i] = eni
				i++
				continue
			}
			log.Info("deleted ENI successfully as it has no secondary IP attached",
				"id", eni.eniID)
		} else {
			e.availENIs[i] = eni
			i++
		}
	}
	e.availENIs = e.availENIs[:i]

	if errors != nil && len(errors) > 0 {
		return failedToUnAssign, fmt.Errorf("failed to unassign one or more ip addresses %v", errors)
	}

	return nil, nil
}

// addIPtoENIMapping adds the list of ip address to point the ENI from which they are allocated
func (e *eniManager) addIPtoENIMapping(eni *eniDetails, ipAddress []string) {
	for _, ip := range ipAddress {
		e.ipToENIMap[ip] = eni
	}
}
