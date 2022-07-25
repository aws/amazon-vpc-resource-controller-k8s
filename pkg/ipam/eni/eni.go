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
	ENIDescription = "aws-k8s-eni"
)

type eniManager struct {
	// instance is the pointer to the instance details
	instance ec2.EC2Instance
	// lock to prevent multiple routines concurrently accessing the eni for same node
	lock sync.Mutex // lock guards the following resources
	// attachedENIs is the list of ENIs attached to the instance
	attachedENIs []*eni
	// ipToENIMap is the map from ip to the ENI that it belongs to
	ipPrefixToENIMap map[string]*eni
}

// eniDetails stores the eniID along with the number of new IPs that can be assigned form it
type eni struct {
	eniID             string
	remainingCapacity int
}

type ENIManager interface {
	InitResources(ec2APIHelper api.EC2APIHelper) ([]string, error)
	CreateIPV4Prefix(required int, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error)
	DeleteIPV4Prefix(ipPrefix []string, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error)
}

// NewENIManager returns a new ENI Manager
func NewENIManager(instance ec2.EC2Instance) *eniManager {
	return &eniManager{
		ipPrefixToENIMap: map[string]*eni{},
		instance:         instance,
	}
}

// InitResources loads the list of ENIs and IPs associated with the instance
func (e *eniManager) InitResources(ec2APIHelper api.EC2APIHelper) ([]string, error) {

	nwInterfaces, err := ec2APIHelper.GetInstanceNetworkInterface(aws.String(e.instance.InstanceID()))
	if err != nil {
		return nil, err
	}

	limits, found := vpc.Limits[e.instance.Type()]
	if !found {
		return nil, fmt.Errorf("unsupported instance type")
	}

	ipLimit := limits.IPv4PerInterface
	var availPrefixes []string
	for _, nwInterface := range nwInterfaces {
		if nwInterface.PrivateIpAddresses != nil {
			eni := &eni{
				remainingCapacity: ipLimit,
				eniID:             *nwInterface.NetworkInterfaceId,
			}
			for _, ip := range nwInterface.Ipv4Prefixes {
				availPrefixes = append(availPrefixes, *ip.Ipv4Prefix)
				e.ipPrefixToENIMap[*ip.Ipv4Prefix] = eni
				eni.remainingCapacity--
			}
			e.attachedENIs = append(e.attachedENIs, eni)
		}
	}

	return availPrefixes, nil
}

// CreateIPV4Address creates IPv4 address and returns the list of assigned IPs along with the error if not all the required
// IPs were assigned
func (e *eniManager) CreateIPV4Prefix(required int, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	var assignedIPv4Prefixes []string
	log = log.WithValues("node name", e.instance.Name())

	// Loop till we reach the last available ENI and list of assigned IPv4 addresses is less than the required IPv4 addresses
	for index := 0; index < len(e.attachedENIs) && len(assignedIPv4Prefixes) < required; index++ {
		remainingCapacity := e.attachedENIs[index].remainingCapacity
		if remainingCapacity > 0 {
			canAssign := 0
			// Number of IPs wanted is the number of IPs required minus the number of IPs assigned till now.
			want := required - len(assignedIPv4Prefixes)
			// Cannot fulfil the entire request using this ENI, allocate whatever the ENI can assign
			if remainingCapacity < want {
				canAssign = remainingCapacity
			} else {
				canAssign = want
			}
			// Assign the IPv4 Addresses from this ENI
			assignedPrefixes, err := ec2APIHelper.AssignIPv4PrefixesAndWaitTillReady(e.attachedENIs[index].eniID, canAssign)
			if err != nil && len(assignedPrefixes) == 0 {
				// Return the list of IPs that were actually created along with the error
				log.Error(err, "failed to assign all the requested prefixes",
					"requested", want, "got", len(assignedPrefixes))
				return assignedPrefixes, err
			} else if err != nil {
				// Just log and continue processing the assigned IPs
				log.Error(err, "failed to assign all the requested prefixes",
					"requested", want, "got", len(assignedPrefixes))
			}
			// Update the remaining capacity
			e.attachedENIs[index].remainingCapacity = remainingCapacity - canAssign
			// Append the assigned IPs on this ENI to the list of IPs created across all the ENIs
			assignedIPv4Prefixes = append(assignedIPv4Prefixes, assignedPrefixes...)
			// Add the mapping from IP to ENI, so that we can easily delete the IP and increment the remaining IP count
			// on the ENI
			for _, ip := range assignedPrefixes {
				e.ipPrefixToENIMap[ip] = e.attachedENIs[index]
			}

			log.Info("assigned IPv4 prefixes", "prefixes", assignedPrefixes, "eni",
				e.attachedENIs[index].eniID, "want", want, "can provide up to", canAssign)
		}
	}

	// // List of secondary IPs supported minus the primary IP
	// ipLimit := vpc.Limits[e.instance.Type()].IPv4PerInterface - 1
	// eniLimit := vpc.Limits[e.instance.Type()].Interface

	// // If the existing ENIs could not assign the required IPs, loop till the new ENIs can assign the required
	// // number of IPv4 Addresses
	// for len(assignedIPv4Prefixes) < required &&
	// 	len(e.attachedENIs) < eniLimit {

	// 	deviceIndex, err := e.instance.GetHighestUnusedDeviceIndex()
	// 	if err != nil {
	// 		// TODO: Refresh device index for linux nodes only
	// 		return assignedIPv4Prefixes, err
	// 	}
	// 	want := required - len(assignedIPv4Prefixes)
	// 	if want > ipLimit {
	// 		want = ipLimit
	// 	}
	// 	nwInterface, err := ec2APIHelper.CreateAndAttachNetworkInterface(aws.String(e.instance.InstanceID()),
	// 		aws.String(e.instance.SubnetID()), e.instance.InstanceSecurityGroup(), nil, aws.Int64(deviceIndex),
	// 		&ENIDescription, nil, want)
	// 	if err != nil {
	// 		// TODO: Check if any clean up is required here for linux nodes only?
	// 		return assignedIPv4Prefixes, err
	// 	}
	// 	eni := &eni{
	// 		remainingCapacity: ipLimit - want,
	// 		eniID:             *nwInterface.NetworkInterfaceId,
	// 	}
	// 	e.attachedENIs = append(e.attachedENIs, eni)
	// 	for _, assignedPrefix := range nwInterface.Ipv4Prefixes {
	// 		assignedIPv4Prefixes = append(assignedIPv4Prefixes, *assignedPrefix.Ipv4Prefix)
	// 		// Also add the mapping from IP to ENI
	// 		e.ipPrefixToENIMap[*assignedPrefix.Ipv4Prefix] = eni
	// 	}
	// }

	var err error
	// This can happen if the subnet doesn't have remaining IPs
	if len(assignedIPv4Prefixes) < required {
		err = fmt.Errorf("not able to create the desired number of IPv4 addresses, required %d, created %d",
			required, len(assignedIPv4Prefixes))
	}

	return assignedIPv4Prefixes, err
}

// DeleteIPV4Address deletes the list of IPv4 addresses and returns the list of IPs that failed to delete along with the
// error
func (e *eniManager) DeleteIPV4Prefix(ipList []string, ec2APIHelper api.EC2APIHelper, log logr.Logger) ([]string, error) {
	e.lock.Lock()
	defer e.lock.Unlock()

	var failedToUnAssign []string
	var errors []error

	log = log.WithValues("node name", e.instance.Name())

	groupedPrefixes := e.groupPrefixPerENI(ipList)
	for eni, prefixes := range groupedPrefixes {
		err := ec2APIHelper.UnassignPrivateIpPrefixes(eni.eniID, prefixes)
		if err != nil {
			errors = append(errors, err)
			log.Info("failed to deleted secondary IPv4 address", "eni", eni.eniID,
				"IPv4 addresses", prefixes)
			failedToUnAssign = append(failedToUnAssign, prefixes...)
			continue
		}
		eni.remainingCapacity += len(prefixes)
		for _, ip := range prefixes {
			delete(e.ipPrefixToENIMap, ip)
		}
		log.Info("deleted secondary IPv4 prefixes", "eni", eni.eniID, "IPv4 addresses", prefixes)
	}

	ipLimit := vpc.Limits[e.instance.Type()].IPv4PerInterface - 1
	primaryENIID := e.instance.PrimaryNetworkInterfaceID()

	// Clean up ENIs that just have the primary network interface attached to them
	i := 0
	for _, eni := range e.attachedENIs {
		// ENI doesn't have any secondary IP attached to it and is not the primary network interface
		if eni.remainingCapacity == ipLimit && primaryENIID != eni.eniID {
			err := ec2APIHelper.DeleteNetworkInterface(&eni.eniID)
			if err != nil {
				errors = append(errors, err)
				e.attachedENIs[i] = eni
				i++
				continue
			}
			log.Info("deleted ENI successfully as it has no secondary IP attached",
				"id", eni.eniID)
		} else {
			e.attachedENIs[i] = eni
			i++
		}
	}
	e.attachedENIs = e.attachedENIs[:i]

	if errors != nil && len(errors) > 0 {
		return failedToUnAssign, fmt.Errorf("failed to unassign one or more ip addresses %v", errors)
	}

	return nil, nil
}

// groupIPsPerENI groups the IPs to delete per ENI
func (e *eniManager) groupPrefixPerENI(deleteList []string) map[*eni][]string {
	toDelete := map[*eni][]string{}
	for _, ip := range deleteList {
		eni := e.ipPrefixToENIMap[ip]
		ls := toDelete[eni]
		ls = append(ls, ip)
		toDelete[eni] = ls
	}

	return toDelete
}
