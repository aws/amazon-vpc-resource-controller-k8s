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

package pool

import (
	"fmt"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
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
	UpdatePool(job worker.WarmPoolJob, didSucceed bool) (shouldReconcile bool)
	ReconcilePool() worker.WarmPoolJob
}

type pool struct {
	// log is the logger initialized with the pool details
	log logr.Logger
	// capacity is the max number of resources that can be allocated from this pool
	capacity int
	// warmPoolConfig is the configuration for the given pool
	warmPoolConfig *config.WarmPoolConfig
	// lock to concurrently make modification to the poll resources
	lock sync.Mutex // following resources are guarded by the lock
	// usedResources is the key value pair of the owner id to the resource id
	usedResources map[string]string
	// warmResources is the list of free resources available to be allocated to the pods
	warmResources []string
	// coolDownQueue is the resources that sit in the queue for the cool down period
	coolDownQueue []coolDownResource
	// pendingCreate represents the number of resources being created asynchronously
	pendingCreate int
	// pendingDelete represents the number of resources being deleted asynchronously
	pendingDelete int
}

type coolDownResource struct {
	// resourceID is the unique ID of the resource
	resourceID string
	// deletionTimestamp is the time when the owner of the resource was deleted
	deletionTimestamp time.Time
}

func NewResourcePool(log logr.Logger, poolConfig *config.WarmPoolConfig, usedResources map[string]string,
	warmResources []string, capacity int) Pool {
	pool := &pool{
		log:            log,
		warmPoolConfig: poolConfig,
		usedResources:  usedResources,
		warmResources:  warmResources,
		capacity:       capacity,
	}
	return pool
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

	p.log.Info("assigned resource", "resource id", resourceID, "requester id", requesterID)

	return resourceID, true, nil
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
	resource := coolDownResource{
		resourceID:        actualResourceID,
		deletionTimestamp: time.Now(),
	}
	p.coolDownQueue = append(p.coolDownQueue, resource)

	p.log.Info("added the resource to cool down queue", "id", resourceID, "owner id", requesterID)

	return true, nil
}

// UpdatePool updates the warm pool with the result of the asynchronous job executed by the provider
func (p *pool) UpdatePool(job worker.WarmPoolJob, didSucceed bool) (shouldReconcile bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	log := p.log.WithValues("operation", job.Operations)

	if !didSucceed {
		shouldReconcile = true
		log.Error(fmt.Errorf("warm pool job failed: %v", job), "operation failed")
	}

	if job.Operations == worker.OperationCreate && didSucceed ||
		job.Operations == worker.OperationDeleted && !didSucceed {

		// TODO: limit the number of times a failed resource can be repeatedly tried to be deleted
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
		if time.Since(resource.deletionTimestamp) >= config.CoolDownPeriod {
			// Add back to the cool down queue
			p.warmResources = append(p.warmResources, resource.resourceID)
		} else {
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
func (p *pool) ReconcilePool() worker.WarmPoolJob {
	p.lock.Lock()
	defer p.lock.Unlock()

	// Total created resources includes all the resources for the instance that are not yet deleted
	totalCreatedResources := len(p.warmResources) + len(p.usedResources) + len(p.coolDownQueue) +
		p.pendingCreate + p.pendingDelete

	log := p.log.WithValues("warm", len(p.warmResources), "used", len(p.usedResources),
		"pending create", p.pendingCreate, "pending delete", &p.pendingDelete, "cool down queue",
		len(p.coolDownQueue), "total created/pending resources", totalCreatedResources, "max capacity", p.capacity)

	if totalCreatedResources == p.capacity {
		log.V(1).Info("cannot reconcile, at max capacity", "total created resources",
			totalCreatedResources)
		return worker.WarmPoolJob{}
	}

	// Consider pending create as well so we don't create multiple subsequent create request
	deviation := p.warmPoolConfig.DesiredSize - (len(p.warmResources) + p.pendingCreate)

	// Need to create more resources for warm pool
	if deviation > p.warmPoolConfig.MaxDeviation {
		// The maximum number of resources that can be created
		canCreateUpto := p.capacity - totalCreatedResources
		// Need to add to warm pool
		if deviation > canCreateUpto {
			log.V(1).Info("can only create limited resources", "can create", canCreateUpto,
				"requested", deviation, "desired", deviation)
			deviation = canCreateUpto
		}

		// Increment the pending to the size of deviation, once we get async response on creation success we can decrement
		// pending
		p.pendingCreate += deviation

		log.Info("created job to add resources for warm pool", "requested count", deviation)

		return worker.NewWarmPoolCreateJob(deviation)

	} else if - deviation > p.warmPoolConfig.MaxDeviation {
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

		return worker.NewWarmPoolDeleteJob(resourceToDelete)
	}

	log.V(1).Info("no need for reconciliation")

	return worker.WarmPoolJob{}
}
