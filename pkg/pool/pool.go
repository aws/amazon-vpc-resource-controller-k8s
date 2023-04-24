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
	ErrResourceAlreadyAssigned    = fmt.Errorf("resource is already assigned to the requestor")
	ErrResourceDoesntExist        = fmt.Errorf("requested resource doesn't exist in used pool")
	ErrIncorrectResourceOwner     = fmt.Errorf("resource doesn't belong to the requestor")
)

type Pool interface {
	AssignResource(requesterID string) (resourceID string, shouldReconcile bool, err error)
	FreeResource(requesterID string, resourceID string) (shouldReconcile bool, err error)
	GetAssignedResource(requesterID string) (resourceID string, ownsResource bool)
	UpdatePool(job *worker.WarmPoolJob, didSucceed bool) (shouldReconcile bool)
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
	usedResources map[string]string
	// warmResources is the list of free resources available to be allocated to the pods
	warmResources []string
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
}

type CoolDownResource struct {
	// ResourceID is the unique ID of the resource
	ResourceID string
	// DeletionTimestamp is the time when the owner of the resource was deleted
	DeletionTimestamp time.Time
}

// IntrospectResponse is the pool state returned to the introspect API
type IntrospectResponse struct {
	UsedResources    map[string]string
	WarmResources    []string
	CoolingResources []CoolDownResource
}

func NewResourcePool(log logr.Logger, poolConfig *config.WarmPoolConfig, usedResources map[string]string,
	warmResources []string, nodeName string, capacity int) Pool {
	pool := &pool{
		log:            log,
		warmPoolConfig: poolConfig,
		usedResources:  usedResources,
		warmResources:  warmResources,
		capacity:       capacity,
		nodeName:       nodeName,
	}
	return pool
}

// ReSync syncs state of upstream with the local pool. If local resources have additional
// resource which doesn't reflect in upstream list then these resources are removed. If the
// upstream has additional resources which are not present locally, these resources are added
// to the warm pool. During ReSync all Create/Delete operations on the Pool should be halted
// but Assign/Free on the Pool can be allowed.
func (p *pool) ReSync(upstreamResource []string) {
	p.lock.Lock()
	defer p.lock.Unlock()

	// This is possible if two Re-Syn were requested at same time
	if !p.reSyncRequired {
		p.log.Info("duplicate re-sync request, will be ignored")
		return
	}
	p.reSyncRequired = false

	// Get the list of local resources
	var localResources []string
	for _, resource := range p.coolDownQueue {
		localResources = append(localResources, resource.ResourceID)
	}
	_, usedResources := utils.GetKeyValSlice(p.usedResources)
	localResources = append(localResources, usedResources...)
	localResources = append(localResources, p.warmResources...)

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
		p.warmResources = append(p.warmResources, newResources...)
	}

	if len(deletedResources) > 0 {
		p.log.Info("attempting to remove deleted resources",
			"deleted resources", deletedResources)

		for _, deletedResource := range deletedResources {
			for i := len(p.warmResources) - 1; i >= 0; i-- {
				if p.warmResources[i] == deletedResource {
					p.log.Info("removing resource from warm pool",
						"resource id", deletedResource)
					p.warmResources = append(p.warmResources[:i], p.warmResources[i+1:]...)
				}
			}
			for i := len(p.coolDownQueue) - 1; i >= 0; i-- {
				if p.coolDownQueue[i].ResourceID == deletedResource {
					p.log.Info("removing resource from cool down queue",
						"resource id", deletedResource)
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

	// Caller can retry in 600 ms [Average time to create and attach a new ENI] or less
	// Different from above check because here we want to perform reconciliation
	if len(p.warmResources) == 0 {
		return "", true, ErrWarmPoolEmpty
	}

	// Allocate the resource
	resourceID = p.warmResources[0]
	p.warmResources = p.warmResources[1:]

	// Add the resource in the used resource key-value pair
	p.usedResources[requesterID] = resourceID

	p.log.V(1).Info("assigned resource",
		"resource id", resourceID, "requester id", requesterID)

	return resourceID, true, nil
}

func (p *pool) GetAssignedResource(requesterID string) (resourceID string, ownsResource bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	resourceID, ownsResource = p.usedResources[requesterID]
	return
}

// FreeResource puts the resource allocated to the given requester into the cool down queue
func (p *pool) FreeResource(requesterID string, resourceID string) (shouldReconcile bool, err error) {
	p.lock.Lock()
	defer p.lock.Unlock()

	actualResourceID, isAssigned := p.usedResources[requesterID]
	if !isAssigned {
		return false, ErrResourceDoesntExist
	}
	if actualResourceID != resourceID {
		return false, ErrIncorrectResourceOwner
	}

	delete(p.usedResources, requesterID)

	// Put the resource in cool down queue
	resource := CoolDownResource{
		ResourceID:        actualResourceID,
		DeletionTimestamp: time.Now(),
	}
	p.coolDownQueue = append(p.coolDownQueue, resource)

	p.log.V(1).Info("added the resource to cool down queue",
		"id", resourceID, "owner id", requesterID)

	return true, nil
}

// UpdatePool updates the warm pool with the result of the asynchronous job executed by the provider
func (p *pool) UpdatePool(job *worker.WarmPoolJob, didSucceed bool) (shouldReconcile bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	log := p.log.WithValues("operation", job.Operations)

	if !didSucceed {
		// If the job fails, re-sync the state of the Pool with upstream
		p.reSyncRequired = true
		shouldReconcile = true
		log.Error(fmt.Errorf("warm pool job failed: %v", job), "operation failed")
	}

	if job.Resources != nil && len(job.Resources) > 0 {
		// Add the resources to the warm pool
		for _, resource := range job.Resources {
			p.warmResources = append(p.warmResources, resource)
		}
		log.Info("added resource to the warm pool", "resources", job.Resources)
	}

	if job.Operations == worker.OperationCreate {
		p.pendingCreate -= job.ResourceCount
	} else if job.Operations == worker.OperationDeleted {
		p.pendingDelete -= job.ResourceCount
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

	for index, resource := range p.coolDownQueue {
		if time.Since(resource.DeletionTimestamp) >= config.CoolDownPeriod {
			// Add back to the cool down queue
			p.warmResources = append(p.warmResources, resource.ResourceID)
			p.log.Info("moving the resource from delete to cool down queue",
				"resource id", resource.ResourceID, "deletion time", resource.DeletionTimestamp)
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

// reconcilePoolIfRequired reconciles the Warm pool to make it reach it's desired state by submitting either create or delete
// request to the warm pool
func (p *pool) ReconcilePool() *worker.WarmPoolJob {
	p.lock.Lock()
	defer p.lock.Unlock()

	// Total created resources includes all the resources for the instance that are not yet deleted
	totalCreatedResources := len(p.warmResources) + len(p.usedResources) + len(p.coolDownQueue) +
		p.pendingCreate + p.pendingDelete

	log := p.log.WithValues("resync", p.reSyncRequired, "warm", len(p.warmResources), "used",
		len(p.usedResources), "pending create", p.pendingCreate, "pending delete", &p.pendingDelete,
		"cool down queue", len(p.coolDownQueue), "total resources", totalCreatedResources,
		"max capacity", p.capacity, "desired size", p.warmPoolConfig.DesiredSize)

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
	deviation := p.warmPoolConfig.DesiredSize - (len(p.warmResources) + p.pendingCreate)

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

		log.Info("created job to add resources to warm pool", "requested count", deviation)

		return worker.NewWarmPoolCreateJob(p.nodeName, deviation)

	} else if -deviation > p.warmPoolConfig.MaxDeviation {
		// Need to delete from warm pool
		deviation = -deviation
		var resourceToDelete []string
		for i := len(p.warmResources) - 1; i >= len(p.warmResources)-deviation; i-- {
			resourceToDelete = append(resourceToDelete, p.warmResources[i])
		}

		// Remove resources to be deleted form the warm pool
		p.warmResources = p.warmResources[:len(p.warmResources)-deviation]
		// Increment pending to the number of resource being deleted, once successfully deleted the count can be decremented
		p.pendingDelete += deviation
		// Submit the job to delete resources

		log.Info("created job to delete resources from warm pool", "resources to delete", resourceToDelete)

		return worker.NewWarmPoolDeleteJob(p.nodeName, resourceToDelete)
	}

	log.V(1).Info("no need for reconciliation")

	return &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}
}

func (p *pool) SetToDraining() *worker.WarmPoolJob {
	// Set the desired size and max deviation to 0.
	// This would force the pool to delete resources from the pool.
	// Any resource being cooled down will be released.
	p.warmPoolConfig.DesiredSize = 0
	p.warmPoolConfig.MaxDeviation = 0

	return p.ReconcilePool()
}

func (p *pool) SetToActive(warmPoolConfig *config.WarmPoolConfig) *worker.WarmPoolJob {
	p.warmPoolConfig = warmPoolConfig
	return p.ReconcilePool()
}

func (p *pool) Introspect() IntrospectResponse {
	p.lock.RLock()
	defer p.lock.RUnlock()

	usedResources := make(map[string]string)
	for k, v := range p.usedResources {
		usedResources[k] = v
	}

	return IntrospectResponse{
		UsedResources:    usedResources,
		WarmResources:    p.warmResources,
		CoolingResources: p.coolDownQueue,
	}
}
