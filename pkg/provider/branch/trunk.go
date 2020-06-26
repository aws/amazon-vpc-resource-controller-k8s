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

package branch

import (
	"fmt"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"

	"github.com/go-logr/logr"
	"github.com/google/go-cmp/cmp"
)

const (
	// MaxAllocatableVlanIds is the maximum number of Vlan Ids that can be allocated per trunk.
	MaxAllocatableVlanIds = 121
)

var (
	InterfaceTypeTrunk   = "trunk"
	TrunkEniDescription  = "trunk-eni"
	BranchEniDescription = "branch-eni"
)

type TrunkENI interface {
	InitTrunk(instance ec2.EC2Instance) error
	CreateAndAssociateBranchToTrunk(securityGroups []*string) (*BranchENI, error)
	DeleteBranchNetworkInterface(branchENI *BranchENI) error
}

// trunkENI is the first trunk network interface of an instance
type trunkENI struct {
	// Log is the logger with the instance details
	log logr.Logger
	// lock is used to perform concurrent operation on the shared variables like the list of used vlan ids
	lock sync.RWMutex
	// ec2ApiHelper is the wrapper interface that provides EC2 API helper functions
	ec2ApiHelper api.EC2APIHelper
	// trunkENIId is the interface id of the trunk network interface
	trunkENIId *string
	// instanceId is the id of the instance that owns the trunk interface
	instanceId *string
	// subnetId is the id of the subnet of the trunk network interface
	subnetId *string
	// subnetCidrBlock is the cidr block of the subnet of trunk interface
	subnetCidrBlock *string
	// usedVlanIds is the list of boolean value representing the used vlan ids
	usedVlanIds []bool
	// branchENIs is the list of BranchENIs associated with the trunk
	branchENIs map[string]*BranchENI
}

// BranchENI is a json convertible structure that stores the Branch ENI details that can be
// used by the CNI plugin or the component consuming the resource
type BranchENI struct {
	// BranchENId is the network interface id of the branch interface
	BranchENId *string `json:"eniId"`
	// MacAdd is the MAC address of the network interface
	MacAdd *string `json:"ifAddress"`
	// BranchIp is the primary IP of the branch Network interface
	BranchIp *string `json:"privateIp"`
	// VlanId is the VlanId of the branch network interface
	VlanId *int64 `json:"vlanId"`
	// SubnetCIDR is the CIDR block of the subnet
	SubnetCIDR *string `json:"subnetCidr"`
}

// NewTrunkENI returns a new Trunk ENI interface.
func NewTrunkENI(logger logr.Logger, instanceId string, instanceSubnetId string,
	instanceSubnetCidrBlock string, helper api.EC2APIHelper) TrunkENI {

	availVlans := make([]bool, MaxAllocatableVlanIds)
	// VlanID 0 cannot be assigned.
	availVlans[0] = true

	return &trunkENI{
		log:             logger,
		instanceId:      &instanceId,
		subnetId:        &instanceSubnetId,
		subnetCidrBlock: &instanceSubnetCidrBlock,
		usedVlanIds:     availVlans,
		ec2ApiHelper:    helper,
		branchENIs:      make(map[string]*BranchENI),
	}
}

// InitTrunk initializes the trunk network interface and all it's associated branch network interfaces by making calls
// to EC2 API
func (t *trunkENI) InitTrunk(instance ec2.EC2Instance) error {
	log := t.log.WithValues("request", "initialize", "instance ID", instance.InstanceID())

	// Get trunk network interface
	trunkInterface, err := t.ec2ApiHelper.GetTrunkInterface(t.instanceId)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("get_trunk").Inc()
		return fmt.Errorf("failed to find trunk interface: %v", err)
	}

	if trunkInterface == nil {
		// Trunk interface doesn't exists, try to create a new trunk interface
		log.Info("creating trunk as it doesn't exist")

		freeIndex, err := instance.GetHighestUnusedDeviceIndex()
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("find_free_index").Inc()
			log.Error(err, "failed to find free device index")
			return err
		}

		trunk, err := t.ec2ApiHelper.CreateAndAttachNetworkInterface(t.instanceId, t.subnetId, nil, &freeIndex,
			&TrunkEniDescription, &InterfaceTypeTrunk, 0)
		if err != nil {
			trunkENIOperationsErrCount.WithLabelValues("create_trunk_eni").Inc()
			log.Error(err, "failed to create trunk interface")
			return err
		}

		t.trunkENIId = trunk.NetworkInterfaceId
		log.Info("created a new trunk interface", "trunk id", t.trunkENIId)
		return nil
	}

	// Trunk interface already exists, fetch the branch interface associated with the trunk
	log.Info("fetched trunk details", "trunk id", t.trunkENIId)

	t.trunkENIId = trunkInterface.NetworkInterfaceId

	// Get the branch associated with the trunk and store the result in the cache
	associations, err := t.ec2ApiHelper.DescribeTrunkInterfaceAssociation(trunkInterface.NetworkInterfaceId)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("describe_trunk_assoc").Inc()
		return fmt.Errorf("failed to describe associations for trunk %s: %v", *trunkInterface.NetworkInterfaceId, err)
	}

	// Return if no branches are associated with the trunk
	if associations == nil || len(associations) == 0 {
		log.Info("successfully initialized trunk with no branch eni currently associated",
			"trunk id", t.trunkENIId)
		return nil
	}

	// For each association build the map of branch ENIs with the interface id and the vlan id
	var branchInterfaceIds []*string
	var branchENIs = map[string]*BranchENI{}
	for _, association := range associations {
		branchENI := &BranchENI{
			BranchENId: association.BranchInterfaceId,
			VlanId:     association.VlanId,
			SubnetCIDR: t.subnetCidrBlock,
		}
		err := t.markVlanAssigned(*branchENI.VlanId)
		if err != nil {
			// This should never happen
			trunkENIOperationsErrCount.WithLabelValues("mark_vlan_assigned").Inc()
			log.Error(err, "vlan id is already assigned, skipping the branch interface",
				"interface", *branchENI.BranchENId)
			continue
		}

		branchENIs[*association.BranchInterfaceId] = branchENI
		branchInterfaceIds = append(branchInterfaceIds, association.BranchInterfaceId)
	}

	// Describe the list of associated branch network interfaces to get the MAC and IPv4 address
	branchInterfaces, err := t.ec2ApiHelper.DescribeNetworkInterfaces(branchInterfaceIds)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("describe_branch_enis").Inc()
		return fmt.Errorf("failed to find branch interfaces on trunk %s: %v",
			*trunkInterface.NetworkInterfaceId, err)
	}

	// For each network interface get the mac and private IPv4 address and store the entry in cache
	for _, branchInterface := range branchInterfaces {
		branchENI, ok := branchENIs[*branchInterface.NetworkInterfaceId]
		if !ok {
			// If the NW Interface was deleted manually between Describe Association and Describe network interface call
			log.Info("skipping NW interface as it doesn't exist anymore", "id",
				*branchInterface.NetworkInterfaceId)
			continue
		}
		branchENI.MacAdd = branchInterface.MacAddress
		branchENI.BranchIp = branchInterface.PrivateIpAddress

		t.addBranchToCache(branchENI)
		t.log.Info("added the branch interface to the cache", "branch", *branchENI)
	}

	log.Info("successfully initialized trunk with all associated branch interfaces")

	return nil
}

// CreateAndAssociateBranchToTrunk creates a new branch network interface and associates the branch to the trunk
// network interface. It returns a Json convertible structure which has all the required details of the branch ENI
func (t *trunkENI) CreateAndAssociateBranchToTrunk(securityGroups []*string) (*BranchENI, error) {
	log := t.log.WithValues("request", "create")

	// Create Trunk Interface and store branch details in object
	nwInterface, err := t.ec2ApiHelper.CreateNetworkInterface(&BranchEniDescription, t.subnetId, securityGroups,
		0, nil)
	if err != nil {
		return nil, err
	}

	log = log.WithValues("interface id", *nwInterface.NetworkInterfaceId)

	log.Info("created branch interface")

	// Create Branch Interface object and store it the internal cache
	branchENI := &BranchENI{BranchENId: nwInterface.NetworkInterfaceId, MacAdd: nwInterface.MacAddress,
		BranchIp: nwInterface.PrivateIpAddress, SubnetCIDR: t.subnetCidrBlock}
	t.addBranchToCache(branchENI)

	log.Info("added branch interface to cache")

	// Assign a vlan id from the list of available vlan ids
	vlanId, err := t.assignVlanId()
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("assign_vlan_id").Inc()
		errDelete := t.DeleteBranchNetworkInterface(branchENI)
		if errDelete != nil {
			trunkENIOperationsErrCount.WithLabelValues("delete_branch").Inc()
			return nil, fmt.Errorf("failed to assing vlan id %v, failed to delete %v", err, errDelete)
		}
		return nil, err
	}
	branchENI.VlanId = &vlanId

	log.Info("assigned vlan id to branch network interface", "vlan id", vlanId)

	// Associate branch to trunk interface
	_, err = t.ec2ApiHelper.AssociateBranchToTrunk(t.trunkENIId, nwInterface.NetworkInterfaceId, &vlanId)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("associate_branch").Inc()
		errDelete := t.DeleteBranchNetworkInterface(branchENI)
		if errDelete != nil {
			trunkENIOperationsErrCount.WithLabelValues("delete_branch").Inc()
			return nil, fmt.Errorf("failed to associate trunk to branch %v, failed to delete %v", err, errDelete)
		}
		return nil, err
	}

	log.Info("associated branch to trunk")

	return branchENI, err
}

// DeleteBranchNetworkInterface deletes the branch network interface and returns an error in case of failure to delete
func (t *trunkENI) DeleteBranchNetworkInterface(branchENI *BranchENI) error {
	log := t.log.WithValues("request", "delete", "branch id", *branchENI.BranchENId)

	// Ensure that the branch ENI was created by the controller and it was not tampered with.
	cacheEntry, isPresent := t.getBranchFromCache(branchENI.BranchENId)
	if !isPresent {
		trunkENIOperationsErrCount.WithLabelValues("get_branch_from_cache").Inc()
		return fmt.Errorf("branch eni was not created by the controller %s not deleting", *branchENI.BranchENId)
	}
	if !cmp.Equal(branchENI, cacheEntry) {
		trunkENIOperationsErrCount.WithLabelValues("compare_branch").Inc()
		return fmt.Errorf("cahced entry %v don't match the expected entry %v not deleting", *branchENI, *cacheEntry)
	}

	// Delete Branch network interface first
	err := t.ec2ApiHelper.DeleteNetworkInterface(branchENI.BranchENId)
	if err != nil {
		trunkENIOperationsErrCount.WithLabelValues("delete_branch").Inc()
		return err
	}

	log.Info("deleted branch interface")

	// Free vlan id used by the branch ENI
	if branchENI.VlanId != nil {
		t.freeVlanId(*branchENI.VlanId)
		log.Info("freed vlan id", "vlan id", *branchENI.VlanId)
	}

	// Remove the entry from the cache
	t.removeBranchFromCache(branchENI.BranchENId)
	log.Info("removed branch from the cache")

	return nil
}

// removeBranchFromCache removes the branch eni from the cache
func (t *trunkENI) removeBranchFromCache(branchId *string) {
	t.lock.Lock()
	defer t.lock.Unlock()

	delete(t.branchENIs, *branchId)
}

// addBranchToCache adds the given branch to the cache if not already present
func (t *trunkENI) addBranchToCache(branchENI *BranchENI) {
	t.lock.Lock()
	defer t.lock.Unlock()

	if _, ok := t.branchENIs[*branchENI.BranchENId]; ok {
		t.log.Info("branch eni already exist not adding again", "request", branchENI)
		return
	}

	t.branchENIs[*branchENI.BranchENId] = branchENI
}

// getBranchFromCache returns the branch from the cache
func (t *trunkENI) getBranchFromCache(branchId *string) (branch *BranchENI, isPresent bool) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	branch, isPresent = t.branchENIs[*branchId]
	return
}

// assignVlanId assigns a free vlan id from the list of available vlan ids. In the future this can be changed to LL
func (t *trunkENI) assignVlanId() (int64, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	for index, used := range t.usedVlanIds {
		if !used {
			t.usedVlanIds[index] = true
			return int64(index), nil
		}
	}
	return 0, fmt.Errorf("failed to find free vlan id in the available %d ids", len(t.usedVlanIds))
}

// markVlanAssigned marks a vlan Id as assigned if not used, if it's already assigned returns an error
func (t *trunkENI) markVlanAssigned(vlanId int64) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	if !t.usedVlanIds[vlanId] {
		t.usedVlanIds[vlanId] = true
		return nil
	}

	return fmt.Errorf("vlan id is already assigned")
}

// freeVlanId frees a vlan ID currently used by a network interface
func (t *trunkENI) freeVlanId(vlanId int64) {
	t.lock.Lock()
	defer t.lock.Unlock()

	isUsed := t.usedVlanIds[vlanId]
	if !isUsed {
		trunkENIOperationsErrCount.WithLabelValues("free_unused_vlan_id").Inc()
		t.log.Error(fmt.Errorf("failed to free a unused vlan id"), "", "vlan id", vlanId)
		return
	}
	t.usedVlanIds[vlanId] = false
}
