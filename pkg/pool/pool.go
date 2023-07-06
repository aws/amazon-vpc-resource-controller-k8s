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

package pool

import (
	"fmt"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/go-logr/logr"
)

var (
	ErrPoolAtMaxCapacity          = fmt.Errorf("cannot assign any more resource from warm pool")
	ErrResourceAreBeingCooledDown = fmt.Errorf("cannot assign resource now, resources are being cooled down")
	ErrResourcesAreBeingCreated   = fmt.Errorf("cannot assign resource now, resources are being created")
	ErrWarmPoolEmpty              = fmt.Errorf("warm pool is empty")
	ErrInsufficientCidrBlocks     = fmt.Errorf("InsufficientCidrBlocks: The specified subnet does not have enough free cidr blocks to satisfy the request")
	ErrResourceAlreadyAssigned    = fmt.Errorf("resource is already assigned to the requestor")
	ErrResourceDoesntExist        = fmt.Errorf("requested resource doesn't exist in used pool")
	ErrIncorrectResourceOwner     = fmt.Errorf("resource doesn't belong to the requestor")
)

const (
	NumIPv4AddrPerPrefix = 16
)

type Pool interface {
	AssignResource(requesterID string) (resourceID string, shouldReconcile bool, err error)
	FreeResource(requesterID string, resourceID string) (shouldReconcile bool, err error)
	GetAssignedResource(requesterID string) (resourceID string, ownsResource bool)
	UpdatePool(job *worker.WarmPoolJob, didSucceed bool, prefixAvailable bool) (shouldReconcile bool)
	ReSync(resources []string)
	ReconcilePool() *worker.WarmPoolJob
	ProcessCoolDownQueue() bool
	SetToDraining() *worker.WarmPoolJob
	SetToActive(warmPoolConfig *config.WarmPoolConfig) *worker.WarmPoolJob
	Introspect() IntrospectResponse
}

type pool struct {
	// log is the logger initialized with the pool details
	log logr.Logger
	// capacity is the max number of resources that can be allocated from this pool
	capacity int
	// warmPoolConfig is the configuration for the given pool
	warmPoolConfig *config.WarmPoolConfig
	// lock to concurrently make modification to the poll resources
	lock sync.RWMutex // following resources are guarded by the lock
	// usedResources is the key value pair of the owner id to the resource id
	usedResources map[string]Resource
	// warmResources is the map of group id to a list of free resources available to be allocated to the pods
	warmResources map[string][]Resource
	// coolDownQueue is the resources that sit in the queue for the cool down period
	coolDownQueue []CoolDownResource
	// pendingCreate represents the number of resources being created asynchronously
	pendingCreate int
	// pendingDelete represents the number of resources being deleted asynchronously
	pendingDelete int
	// nodeName k8s name of the node
	nodeName string
	// reSyncRequired is set if the upstream and pool are possibly out of sync due to
	// errors in creating/deleting resources
	reSyncRequired bool
	// isPDPool indicates whether the pool is for prefix IP provider or secondary IP provider
	isPDPool bool
	// prefixAvailable indicates whether subnet has any prefix available
	prefixAvailable bool
}

// Resource represents a secondary IPv4 address or a prefix-deconstructed IPv4 address, uniquely identified by GroupID and ResourceID
type Resource struct {
	// could be IPv4 address or IPv4 prefix
	GroupID string
	// IPv4 address
	ResourceID string
}

type CoolDownResource struct {
	// ResourceID is the unique ID of the resource
	Resource Resource
	// DeletionTimestamp is the time when the owner of the resource was deleted
	DeletionTimestamp time.Time
}

// IntrospectResponse is the pool state returned to the introspect API
type IntrospectResponse struct {
	UsedResources    map[string]Resource
	WarmResources    map[string][]Resource
	CoolingResources []CoolDownResource
}

type IntrospectSummaryResponse struct {
	UsedResourcesCount    int
	WarmResourcesCount    int
	CoolingResourcesCount int
}

func NewResourcePool(log logr.Logger, poolConfig *config.WarmPoolConfig, usedResources map[string]Resource,
	warmResources map[string][]Resource, nodeName string, capacity int, isPDPool bool) Pool {
	pool := &pool{
		log:            log,
		warmPoolConfig: poolConfig,
		usedResources:  usedResources,
		warmResources:  warmResources,
		capacity:       capacity,
		nodeName:       nodeName,
		isPDPool:       isPDPool,
	}
	return pool
}

// ReSync syncs state of upstream with the local pool. If local resources have additional
// resource which doesn't reflect in upstream list then these resources are removed. If the
// upstream has additional resources which are not present locally, these resources are added
// to the warm pool. During ReSync all Create/Delete operations on the Pool should be halted
// but Assign/Free on the Pool can be allowed.
func (p *pool) ReSync(upstreamResourceGroupIDs []string) {
	p.lock.Lock()
	defer p.lock.Unlock()

	// This is possible if two Re-Syn were requested at same time
	if !p.reSyncRequired {
		p.log.Info("duplicate re-sync request, will be ignored")
		return
	}
	p.reSyncRequired = false

	// Convert list of upstream resource group ids into a list of Resource
	var upstreamResource []Resource
	for _, resourceGroupID := range upstreamResourceGroupIDs {
		if resourceGroupID == "" {
			continue
		}

		if p.isPDPool {
			resourceIDs, err := utils.DeconstructIPsFromPrefix(resourceGroupID)
			if err != nil {
				p.log.Error(err, "failed to sync upstream resource", "resource group id", resourceGroupID, "err", err)
				continue
			}

			var resources []Resource
			for _, resourceID := range resourceIDs {
				resources = append(resources, Resource{resourceGroupID, resourceID})
			}
			upstreamResource = append(upstreamResource, resources...)
		} else {
			upstreamResource = append(upstreamResource, Resource{resourceGroupID, resourceGroupID})
		}
	}

	// Get the list of local resources
	var localResources []Resource
	for _, coolDownResource := range p.coolDownQueue {
		localResources = append(localResources, coolDownResource.Resource)
	}
	for _, usedResource := range p.usedResources {
		localResources = append(localResources, usedResource)
	}
	for _, warmResources := range p.warmResources {
		localResources = append(localResources, warmResources...)
	}

	// resources that are present upstream but missing in the pool
	newResources := utils.Difference(upstreamResource, localResources)

	// resources that are deleted from upstream but still present in the pool
	deletedResources := utils.Difference(localResources, upstreamResource)

	if len(newResources) == 0 && len(deletedResources) == 0 {
		p.log.Info("local and upstream state is in sync")
		return
	}

	if len(newResources) > 0 {
		p.log.Info("adding new resources to warm pool", "resource", newResources)
		for _, resource := range newResources {
			p.warmResources[resource.GroupID] = append(p.warmResources[resource.GroupID], resource)
		}
	}

	if len(deletedResources) > 0 {
		p.log.Info("attempting to remove deleted resources",
			"deleted resources", deletedResources)

		for _, resource := range deletedResources {
			// remove deleted resource from list of warm resources of the resource group
			for i := len(p.warmResources[resource.GroupID]) - 1; i >= 0; i-- {
				if p.warmResources[resource.GroupID] == nil || len(p.warmResources[resource.GroupID]) == 0 {
					continue
				}
				warmResource := p.warmResources[resource.GroupID][i]
				if warmResource.ResourceID == resource.ResourceID {
					p.log.Info("removing resource from warm pool",
						"group id", resource.GroupID, "resource id", resource.ResourceID)
					p.warmResources[resource.GroupID] = append(p.warmResources[resource.GroupID][:i], p.warmResources[resource.GroupID][i+1:]...)
					// if the removed resource is the only one in the group, then delete the resource group from warm pool
					if len(p.warmResources[resource.GroupID]) == 0 {
						delete(p.warmResources, resource.GroupID)
					}
				}
			}

			// remove deleted resource from cool down queue
			for i := len(p.coolDownQueue) - 1; i >= 0; i-- {
				coolDownResource := p.coolDownQueue[i]
				if coolDownResource.Resource.GroupID == resource.GroupID &&
					coolDownResource.Resource.ResourceID == resource.ResourceID {
					p.log.Info("removing resource from cool down queue",
						"group id", resource.GroupID, "resource id", resource.ResourceID)
					p.coolDownQueue = append(p.coolDownQueue[:i], p.coolDownQueue[i+1:]...)
				}
			}
		}
	}
}

// AssignResource assigns a resources to the requester, the caller must retry in case there is capacity and the warm pool
// is currently empty
func (p *pool) AssignResource(requesterID string) (resourceID string, shouldReconcile bool, err error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if _, isAlreadyAssigned := p.usedResources[requesterID]; isAlreadyAssigned {
		return "", false, ErrResourceAlreadyAssigned
	}

	if len(p.usedResources) == p.capacity {
		return "", false, ErrPoolAtMaxCapacity
	}

	// Caller must retry at max by 30 seconds [Max time resource will sit in the cool down queue]
	if len(p.usedResources)+len(p.coolDownQueue) == p.capacity {
		return "", false, ErrResourceAreBeingCooledDown
	}

	// Caller can retry in 600 ms [Average time to create and attach a new ENI] or less
	if len(p.usedResources)+len(p.coolDownQueue)+p.pendingCreate+p.pendingDelete == p.capacity {
		return "", false, ErrResourcesAreBeingCreated
	}

	if len(p.warmResources) == 0 {
		// If prefix is not available in subnet, caller can retry in 2 min [Action required from user to change subnet]
		if p.isPDPool && !p.prefixAvailable {
			return "", false, ErrInsufficientCidrBlocks
		}

		// Caller can retry in 600 ms [Average time to assign a new secondary IP or prefix] or less
		// Different from above check because here we want to perform reconciliation
		return "", true, ErrWarmPoolEmpty
	}

	// Allocate the resource
	resource := Resource{}
	if p.isPDPool {
		resource = p.assignResourceFromMinGroup()
	} else {
		resource = p.assignResourceFromAnyGroup()
	}
	if resource.ResourceID == "" || resource.GroupID == "" {
		return "", true, ErrWarmPoolEmpty
	}

	// Add the resource in the used resource key-value pair
	p.usedResources[requesterID] = resource
	p.log.V(1).Info("assigned resource",
		"resource id", resource.ResourceID, "requester id", requesterID)

	return resource.ResourceID, true, nil
}

func (p *pool) GetAssignedResource(requesterID string) (resourceID string, ownsResource bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	resource, ownsResource := p.usedResources[requesterID]
	resourceID = resource.ResourceID

	return
}

// FreeResource puts the resource allocated to the given requester into the cool down queue
func (p *pool) FreeResource(requesterID string, resourceID string) (shouldReconcile bool, err error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	actualResource, isAssigned := p.usedResources[requesterID]
	if !isAssigned {
		return false, ErrResourceDoesntExist
	}
	if actualResource.ResourceID != resourceID {
		return false, ErrIncorrectResourceOwner
	}

	delete(p.usedResources, requesterID)

	// Put the resource in cool down queue
	resource := CoolDownResource{
		Resource:          actualResource,
		DeletionTimestamp: time.Now(),
	}
	p.coolDownQueue = append(p.coolDownQueue, resource)

	p.log.V(1).Info("added the resource to cool down queue",
		"resource", actualResource, "owner id", requesterID)

	return true, nil
}

// UpdatePool updates the warm pool with the result of the asynchronous job executed by the provider
func (p *pool) UpdatePool(job *worker.WarmPoolJob, didSucceed bool, prefixAvailable bool) (shouldReconcile bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	log := p.log.WithValues("operation", job.Operations)

	if !didSucceed {
		// If the job fails, re-sync the state of the Pool with upstream
		p.reSyncRequired = true
		shouldReconcile = true
		log.Error(fmt.Errorf("warm pool job failed: %v", job), "operation failed")
	}

	if p.isPDPool {
		p.prefixAvailable = prefixAvailable
		if !p.prefixAvailable {
			log.Error(fmt.Errorf("warm pool job failed: %v", job), "prefix is not available in subnet")
		}
	}

	if job.Resources != nil && len(job.Resources) > 0 {
		// Add the resources to the warm pool
		for _, resourceGroup := range job.Resources {
			var resources []Resource
			resourceIDs := []string{resourceGroup}
			if p.isPDPool {
				var err error
				if resourceIDs, err = utils.DeconstructIPsFromPrefix(resourceGroup); err != nil {
					log.Error(err, "failed to deconstruct prefix into valid IPs", "prefix", resourceGroup)
				}
			}
			for _, resourceID := range resourceIDs {
				resources = append(resources, Resource{resourceGroup, resourceID})
			}
			p.warmResources[resourceGroup] = append(p.warmResources[resourceGroup], resources...)
		}
		log.Info("added resource to the warm pool", "resources", job.Resources)
	}

	if job.Operations == worker.OperationCreate {
		if p.isPDPool {
			p.pendingCreate -= job.ResourceCount * NumIPv4AddrPerPrefix
		} else {
			p.pendingCreate -= job.ResourceCount
		}
	} else if job.Operations == worker.OperationDeleted {
		if p.isPDPool {
			p.pendingDelete -= job.ResourceCount * NumIPv4AddrPerPrefix
		} else {
			p.pendingDelete -= job.ResourceCount
		}
	}

	log.V(1).Info("processed job response", "job", job, "pending create",
		p.pendingCreate, "pending delete", p.pendingDelete)

	return shouldReconcile
}

// ProcessCoolDownQueue adds the resources back to the warm pool once they have cooled down
func (p *pool) ProcessCoolDownQueue() (needFurtherProcessing bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	if len(p.coolDownQueue) == 0 {
		return false
	}

	for index, coolDownResource := range p.coolDownQueue {
		if time.Since(coolDownResource.DeletionTimestamp) >= config.CoolDownPeriod {
			p.warmResources[coolDownResource.Resource.GroupID] = append(p.warmResources[coolDownResource.Resource.GroupID], coolDownResource.Resource)
			p.log.Info("moving the deleted resource from cool down queue to warm pool",
				"resource", coolDownResource, "deletion time", coolDownResource.DeletionTimestamp)
		} else {
			// Remove the items from cool down queue that are processed and return
			p.coolDownQueue = p.coolDownQueue[index:]
			return true
		}
	}

	// All items were processed empty the cool down queue
	p.coolDownQueue = p.coolDownQueue[:0]

	return false
}

// ReconcilePool reconciles the warm pool to make it reach its desired state by submitting either create or delete
// request to the warm pool
func (p *pool) ReconcilePool() *worker.WarmPoolJob {
	p.lock.Lock()
	defer p.lock.Unlock()

	// Total created resources includes all the resources for the instance that are not yet deleted
	numWarmResources := numResourcesFromMap(p.warmResources)
	totalCreatedResources := numWarmResources + len(p.usedResources) + len(p.coolDownQueue) +
		p.pendingCreate + p.pendingDelete
	log := p.log.WithValues("resync", p.reSyncRequired, "warm", numWarmResources, "used",
		len(p.usedResources), "pending create", p.pendingCreate, "pending delete", p.pendingDelete,
		"cool down queue", len(p.coolDownQueue), "total resources", totalCreatedResources, "capacity", p.capacity)

	if p.reSyncRequired {
		// If Pending operations are present then we can't re-sync as the upstream
		// and pool could change during re-sync
		if p.pendingCreate != 0 || p.pendingDelete != 0 {
			p.log.Info("cannot re-sync as there are pending add/delete request")
			return &worker.WarmPoolJob{
				Operations: worker.OperationReconcileNotRequired,
			}
		}
		p.log.Info("submitting request re-sync the pool")
		return worker.NewWarmPoolReSyncJob(p.nodeName)
	}

	if len(p.usedResources)+p.pendingCreate+p.pendingDelete+len(p.coolDownQueue) == p.capacity {
		log.V(1).Info("cannot reconcile, at max capacity")
		return &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}
	}

	// Consider pending create as well so we don't create multiple subsequent create request
	deviation := p.warmPoolConfig.DesiredSize - (numWarmResources + p.pendingCreate)
	if p.isPDPool {
		deviation = p.getPDDeviation()
	}

	// Need to create more resources for warm pool
	if deviation > p.warmPoolConfig.MaxDeviation {
		// The maximum number of resources that can be created
		canCreateUpto := p.capacity - totalCreatedResources
		if canCreateUpto == 0 {
			return &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}
		}

		// Need to add to warm pool
		if deviation > canCreateUpto {
			log.V(1).Info("can only create limited resources", "can create", canCreateUpto,
				"requested", deviation, "desired", deviation)
			deviation = canCreateUpto
		}

		// Increment the pending to the size of deviation, once we get async response on creation success we can decrement
		// pending
		p.pendingCreate += deviation

		log.Info("created job to add resources to warm pool", "pendingCreate", p.pendingCreate,
			"requested count", deviation)
		if p.isPDPool {
			return worker.NewWarmPoolCreateJob(p.nodeName, deviation/NumIPv4AddrPerPrefix)
		}
		return worker.NewWarmPoolCreateJob(p.nodeName, deviation)

	} else if -deviation > p.warmPoolConfig.MaxDeviation {
		// Need to delete from warm pool
		deviation = -deviation

		var resourceToDelete []string
		numToDelete := deviation
		if numToDelete > 0 {
			var freeResourceGroups []string
			if p.isPDPool {
				// for prefix IP pool, each resource group is a /28 IPv4 prefix, which contains 16 IPv4 addresses
				freeResourceGroups = findFreeGroup(p.warmResources, NumIPv4AddrPerPrefix)
			} else {
				// for secondary IP pool, each resource group only contains 1 IPv4 address
				freeResourceGroups = findFreeGroup(p.warmResources, 1)
			}

			if len(freeResourceGroups) > 0 {
				// Remove resources to be deleted from the warm pool
				for _, groupID := range freeResourceGroups {
					if numToDelete <= 0 {
						break
					}
					log.Info("removing free resource group from warm pool", "group id", groupID,
						"# resources deleted", len(p.warmResources[groupID]))
					resourceToDelete = append(resourceToDelete, groupID)
					numToDelete -= len(p.warmResources[groupID])
					// Increment pending to the number of resource being deleted, once successfully deleted the count can be decremented
					p.pendingDelete += len(p.warmResources[groupID])
					delete(p.warmResources, groupID)
				}
			} else {
				log.Info("no warm resources to delete", "deviation", deviation, "numToDelete", numToDelete)
			}
		}

		if len(resourceToDelete) > 0 {
			// Submit the job to delete resources
			log.Info("created job to delete resources from warm pool", "pendingDelete", p.pendingDelete,
				"resources to delete", resourceToDelete)
			return worker.NewWarmPoolDeleteJob(p.nodeName, resourceToDelete)
		}
	}

	log.V(1).Info("no need for reconciliation")

	return &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}
}

// SetToDraining sets warm pool config to empty, which would force the pool to delete resources.
func (p *pool) SetToDraining() *worker.WarmPoolJob {
	p.lock.Lock()
	p.warmPoolConfig = &config.WarmPoolConfig{}
	p.lock.Unlock()

	return p.ReconcilePool()
}

// SetToActive sets warm pool config to valid values, which would force the pool to request resources.
func (p *pool) SetToActive(warmPoolConfig *config.WarmPoolConfig) *worker.WarmPoolJob {
	p.lock.Lock()
	p.warmPoolConfig = warmPoolConfig
	p.lock.Unlock()

	return p.ReconcilePool()
}

func (p *pool) Introspect() IntrospectResponse {
	p.lock.RLock()
	defer p.lock.RUnlock()

	usedResources := make(map[string]Resource)
	for k, v := range p.usedResources {
		usedResources[k] = v
	}

	warmResources := make(map[string][]Resource)
	for group, resources := range p.warmResources {
		var resourcesCopy []Resource
		for _, res := range resources {
			resourcesCopy = append(resourcesCopy, res)
		}
		warmResources[group] = resourcesCopy
	}

	return IntrospectResponse{
		UsedResources:    usedResources,
		WarmResources:    warmResources,
		CoolingResources: p.coolDownQueue,
	}
}

// findFreeGroup finds groups that have all possible resources free to be allocated or deleted, and returns their group ids
func findFreeGroup(resourceGroups map[string][]Resource, numResourcesPerGroup int) (freeGroupIDs []string) {
	for groupID, resources := range resourceGroups {
		if resources != nil && len(resources) == numResourcesPerGroup {
			freeGroupIDs = append(freeGroupIDs, groupID)
		}
	}
	return
}

// findMinGroup finds the resource group with the fewest resources, and returns the group id along with its count of resources
func findMinGroup(resourceGroups map[string][]Resource) (group string, minCount int) {
	for groupID, resources := range resourceGroups {
		if resources == nil || len(resources) == 0 {
			continue
		}

		// initialize return values as the first key-value pair; when lower count is found, record the group and minCount
		if group == "" || len(resources) < minCount {
			minCount = len(resources)
			group = groupID
		}
	}
	return
}

// assignResourceFromMinGroup assigns a resource from the resource group that has the lowest number of resources
func (p *pool) assignResourceFromMinGroup() Resource {
	groupID, resourceCount := findMinGroup(p.warmResources)
	if groupID == "" || resourceCount == 0 {
		return Resource{}
	}

	resource := p.warmResources[groupID][0]
	if resourceCount == 1 {
		p.warmResources[groupID] = nil
	} else {
		p.warmResources[groupID] = p.warmResources[groupID][1:]
	}

	return resource
}

// assignResourceFromAnyGroup assigns a resource from any resource group with at least one resource available
func (p *pool) assignResourceFromAnyGroup() Resource {
	resource := Resource{}
	for groupID, resources := range p.warmResources {
		if resources == nil || len(resources) <= 0 {
			continue
		}

		resource = resources[0]
		if len(resources) == 1 {
			p.warmResources[groupID] = nil
		} else {
			p.warmResources[groupID] = resources[1:]
		}
		break
	}

	return resource
}

// getPDDeviation returns the deviation in number of IPv4 addresses when PD is enabled, taking into account of all PD configuration params
func (p *pool) getPDDeviation() int {
	deviationPrefix := 0

	var isWarmIPTargetDefined, isMinIPTargetDefined, isWarmPrefixTargetDefined bool

	// if DesiredSize is 0, it means PD pool is in draining state, then set targets to 0
	if p.warmPoolConfig.DesiredSize == 0 {
		p.warmPoolConfig.WarmIPTarget = 0
		p.warmPoolConfig.MinIPTarget = 0
		p.warmPoolConfig.WarmPrefixTarget = 0
	} else {
		// PD pool is active, check if target values are valid. If so, set defined flag to true
		if p.warmPoolConfig.WarmIPTarget > 0 {
			isWarmIPTargetDefined = true
		}
		if p.warmPoolConfig.MinIPTarget > 0 {
			isMinIPTargetDefined = true
		}
		if p.warmPoolConfig.WarmPrefixTarget > 0 {
			isWarmPrefixTargetDefined = true
		}

		// set target to default values if not defined
		if !isWarmIPTargetDefined {
			if isMinIPTargetDefined || !isWarmPrefixTargetDefined {
				p.warmPoolConfig.WarmIPTarget = config.IPv4PDDefaultWarmIPTargetSize
			}
		}
		if !isMinIPTargetDefined {
			if isWarmIPTargetDefined || !isWarmPrefixTargetDefined {
				p.warmPoolConfig.MinIPTarget = config.IPv4PDDefaultMinIPTargetSize
			}
		}
	}

	numExistingWarmResources := numResourcesFromMap(p.warmResources)
	freePrefixes := findFreeGroup(p.warmResources, NumIPv4AddrPerPrefix)

	// if neither WarmIPTarget nor MinIPTarget defined but WarmPrefixTarget is defined, return deviation needed for warm prefix target
	if !isWarmIPTargetDefined && !isMinIPTargetDefined && isWarmPrefixTargetDefined {
		deviationPrefix = p.warmPoolConfig.WarmPrefixTarget - len(freePrefixes) - utils.CeilDivision(p.pendingCreate, NumIPv4AddrPerPrefix)

		if deviationPrefix != 0 {
			p.log.Info("calculating IP deviation for prefix pool to satisfy warm prefix target", "warm prefix target",
				p.warmPoolConfig.WarmPrefixTarget, "numFreePrefix", len(freePrefixes), "p.pendingCreate", p.pendingCreate,
				"p.pendingDelete", p.pendingDelete, "deviation", deviationPrefix*NumIPv4AddrPerPrefix)
		}

		return deviationPrefix * NumIPv4AddrPerPrefix
	}

	// total resources in pool include used resources, warm resources, pendingCreate (to avoid duplicate request), and coolDownQueue
	numTotalResources := len(p.usedResources) + p.pendingCreate + numExistingWarmResources + len(p.coolDownQueue)
	// number of existing prefixes
	numCurrPrefix := utils.CeilDivision(numTotalResources, 16)

	// number of total resources required to meet WarmIPTarget
	numTotalResForWarmIPTarget := len(p.usedResources) + p.warmPoolConfig.WarmIPTarget
	// number of total prefixes required to meet WarmIPTarget
	numPrefixForWarmIPTarget := utils.CeilDivision(numTotalResForWarmIPTarget, 16)
	// number of prefixes to meet MinIPTarget
	numPrefixForMinIPTarget := utils.CeilDivision(p.warmPoolConfig.MinIPTarget, 16)

	if numPrefixForWarmIPTarget >= numPrefixForMinIPTarget {
		// difference is the number of prefixes to create or delete
		deviationPrefix = numPrefixForWarmIPTarget - numCurrPrefix
	} else {
		// difference is the number of prefixes to create in order to meet MinIPTarget
		deviationPrefix = numPrefixForMinIPTarget - numCurrPrefix
	}

	// if we need to delete prefixes, should check if any prefix is free to be deleted since they can be fragmented. If not, reset deviation
	if deviationPrefix < 0 && len(freePrefixes) == 0 {
		deviationPrefix = 0
	}

	if deviationPrefix != 0 {
		p.log.V(1).Info("calculating IP deviation for prefix pool", "warmPoolConfig", p.warmPoolConfig,
			"used resources", len(p.usedResources), "existing warm resources", numExistingWarmResources, "pendingCreate", p.pendingCreate,
			"pendingDelete", p.pendingDelete, "numTotalResources", numTotalResources, "numCurrPrefix", numCurrPrefix,
			"numTotalResForWarmIPTarget", numTotalResForWarmIPTarget, "numPrefixForWarmIPTarget", numPrefixForWarmIPTarget,
			"numPrefixForMinIPTarget", numPrefixForMinIPTarget, "deviation", deviationPrefix*NumIPv4AddrPerPrefix)
	}

	return deviationPrefix * NumIPv4AddrPerPrefix
}

// numResourcesFromMap returns total number of resources from a map of list of resources indexed by group id
func numResourcesFromMap(resourceGroups map[string][]Resource) int {
	count := 0
	for _, resources := range resourceGroups {
		count += len(resources)
	}
	return count
}
