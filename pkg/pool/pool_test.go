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
	"testing"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	poolConfig = &config.WarmPoolConfig{
		DesiredSize:  2,
		ReservedSize: 1,
		MaxDeviation: 1,
	}

	pod1 = "test/pod-1"
	pod2 = "default/pod-2"
	pod3 = "test/pod-3"
	pod4 = "default/pod-4"

	res1, res2, res3, res4, res5, res6 = "res-1", "res-2", "res-3", "res-4", "res-5", "res-6"

	warmPoolResources = []string{res3, res4}
	usedResources     = map[string]string{pod1: res1, pod2: res2}
)

func getMockPool(poolConfig *config.WarmPoolConfig, usedResources map[string]string,
	warmResources []string, capacity int) pool {

	usedResourcesCopy := map[string]string{}
	for k, v := range usedResources {
		usedResourcesCopy[k] = v
	}

	warmResourcesCopy := make([]string, len(warmResources))
	copy(warmResourcesCopy, warmResources)

	pool := pool{
		log:            zap.New(zap.UseDevMode(true)).WithValues("pool", "res-id/node-name"),
		warmPoolConfig: poolConfig,
		usedResources:  usedResourcesCopy,
		warmResources:  warmResourcesCopy,
		capacity:       capacity,
	}

	return pool
}

func TestPool_NewResourcePool(t *testing.T) {
	pool := NewResourcePool(nil, poolConfig, usedResources, warmPoolResources, 5)
	assert.NotNil(t, pool)
}

// TestPool_AssignResource tests resource is allocated ot pod if present in the warm pool
func TestPool_AssignResource(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, warmPoolResources, 5)

	resourceID, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.NoError(t, err)
	assert.True(t, shouldReconcile)
	assert.Equal(t, res3, resourceID)
	assert.Equal(t, res3, warmPool.usedResources[pod3])
}

// TestPool_AssignResource_AlreadyAssigned tests error is returned if a pod already was allocated a resource
func TestPool_AssignResource_AlreadyAssigned(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 2)

	_, shouldReconcile, err := warmPool.AssignResource(pod1)

	assert.Error(t, ErrResourceAlreadyAssigned, err)
	assert.False(t, shouldReconcile)
}

// TestPool_AssignResource_AtCapacity tests error is returned if pool cannot allocate anymore resources
func TestPool_AssignResource_AtCapacity(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 2)

	_, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.Error(t, ErrPoolAtMaxCapacity, err)
	assert.False(t, shouldReconcile)
}

// TestPool_AssignResources_WarmPoolEmpty tests error is returned if the warm pool is empty
func TestPool_AssignResource_WarmPoolEmpty(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 5)

	_, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.Error(t, ErrWarmPoolEmpty, err)
	assert.True(t, shouldReconcile)
}

// TestPool_AssignResource_CoolDown tests error is returned if the resource are being cooled down
func TestPool_AssignResource_CoolDown(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)
	warmPool.coolDownQueue = append(warmPool.coolDownQueue, coolDownResource{resourceID: res3})

	_, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.Error(t, ErrResourceAreBeingCooledDown, err)
	assert.False(t, shouldReconcile)
}

// TestPool_FreeResource tests no error is returned on freeing a used resource
func TestPool_FreeResource(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	shouldReconcile, err := warmPool.FreeResource(pod2, res2)
	_, present := warmPool.usedResources[pod2]

	assert.NoError(t, err)
	assert.True(t, shouldReconcile)
	assert.False(t, present)
	assert.Equal(t, res2, warmPool.coolDownQueue[0].resourceID)
}

// TestPool_FreeResource_isNotAssigned test error is returned if trying to free a resource that doesn't belong to any
// owner
func TestPool_FreeResource_isNotAssigned(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	shouldReconcile, err := warmPool.FreeResource(pod3, res3)

	assert.Error(t, ErrResourceDoesntExist, err)
	assert.False(t, shouldReconcile)
}

// TestPool_FreeResource_IncorrectOwner tests error is returned if incorrect owner id is passed
func TestPool_FreeResource_IncorrectOwner(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	shouldReconcile, err := warmPool.FreeResource(pod2, res1)

	assert.Error(t, ErrIncorrectResourceOwner, err)
	assert.False(t, shouldReconcile)
}

// TestPool_UpdatePool_OperationCreate_Succeed tests resources are added to the warm pool if resource are created
// successfully
func TestPool_UpdatePool_OperationCreate_Succeed(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	createdResources := []string{res3, res4}
	shouldReconcile := warmPool.UpdatePool(worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     createdResources,
		ResourceCount: 2,
	}, true)

	// Resources should be added to warm pool
	assert.Equal(t, createdResources, warmPool.warmResources)
	assert.False(t, shouldReconcile)
}

// TestPool_UpdatePool_OperationCreate_Failed tests that reconciler is triggered when create operation fails
func TestPool_UpdatePool_OperationCreate_Failed(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	shouldReconcile := warmPool.UpdatePool(worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     nil,
		ResourceCount: 2,
	}, false)

	assert.True(t, shouldReconcile)
}

// TestPool_UpdatePool_OperationDelete tests that reconciler is not triggered again if delete operation succeeds
func TestPool_UpdatePool_OperationDelete_Succeed(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	shouldReconcile := warmPool.UpdatePool(worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     nil,
		ResourceCount: 1,
	}, true)

	// Assert resources are not added back to the warm pool
	assert.Zero(t, len(warmPool.warmResources))
	assert.False(t, shouldReconcile)
}

// TestPool_UpdatePool_OperationDelete_Failed tests resources are added back to the warm pool if delete fails
func TestPool_UpdatePool_OperationDelete_Failed(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	failedResources := []string{res3, res4}
	shouldReconcile := warmPool.UpdatePool(worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     failedResources,
		ResourceCount: 2,
	}, false)

	// Assert resources are added back to the warm pool
	assert.Equal(t, failedResources, warmPool.warmResources)
	assert.True(t, shouldReconcile)
}

// TestPool_ProcessCoolDownQueue tests that item are added back to the warm pool once they have cooled down
func TestPool_ProcessCoolDownQueue(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)
	warmPool.coolDownQueue = []coolDownResource{
		{resourceID: res3, deletionTimestamp: time.Now().Add(- time.Second * 33)},
		{resourceID: res4, deletionTimestamp: time.Now().Add(- time.Second * 32)},
		{resourceID: res5, deletionTimestamp: time.Now().Add(- time.Second * 10)},
	}

	needFurtherProcessing := warmPool.ProcessCoolDownQueue()

	// As resource 5 is yet not deleted
	assert.True(t, needFurtherProcessing)
	// Assert resources are added back to the warm pool
	assert.Equal(t, []string{res3, res4}, warmPool.warmResources)
	// Assert resource that is not cooled down is still in the queue
	assert.Equal(t, res5, warmPool.coolDownQueue[0].resourceID)
}

// TestPool_ProcessCoolDownQueue_NoFurtherProcessingRequired tests that if all the items of the cool down queue are
// processed it should be empty
func TestPool_ProcessCoolDownQueue_NoFurtherProcessingRequired(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 3)

	warmPool.coolDownQueue = []coolDownResource{
		{resourceID: res3, deletionTimestamp: time.Now().Add(- time.Second * 33)}}

	needFurtherProcessing := warmPool.ProcessCoolDownQueue()

	// Cool down queue should now be empyy
	assert.Zero(t, len(warmPool.coolDownQueue))
	assert.False(t, needFurtherProcessing)
}

// TestPool_getPendingResources tests total pending resource returns the pending create and pending delete items
func TestPool_getPendingResources(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 7)
	warmPool.pendingCreate = 2
	warmPool.pendingDelete = 3

	assert.Equal(t, 5, warmPool.pendingDelete+warmPool.pendingCreate)
}

// TestPool_ReconcilePool_MaxCapacity tests reconciliation doesn't happen if the pod is at max capacity
func TestPool_ReconcilePool_MaxCapacity(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{pod1}, 6)
	warmPool.pendingCreate = 1
	warmPool.pendingDelete = 1

	warmPool.coolDownQueue = append(warmPool.coolDownQueue, coolDownResource{resourceID: res4})

	// used = 2, warm = 1, total pending = 2, cool down queue = 1, total = 6 & capacity = 6 => At max capacity
	job := warmPool.ReconcilePool()

	// no need to reconcile
	assert.Equal(t, worker.WarmPoolJob{}, job)
}

// TestPool_ReconcilePool_NotRequired tests if the deviation form warm pool is equal to or less than the max deviation
// then reconciliation is not triggered
func TestPool_ReconcilePool_NotRequired(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 7)
	warmPool.pendingCreate = 1

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 1(actual WP + pending create) = 1, (deviation)1 > (max deviation)1 => false,
	// so no need create right now
	assert.Equal(t, worker.WarmPoolJob{}, job)
}

// TestPool_ReconcilePool tests job with operation type create is returned when the warm pool deviates form max deviation
func TestPool_ReconcilePool_Create(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 7)

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 0(actual WP + pending create) = 0, (deviation)0 > (max deviation)1 => true,
	// create (deviation)2 resources
	assert.Equal(t, worker.WarmPoolJob{Operations: worker.OperationCreate, ResourceCount: 2}, job)
	assert.Equal(t, warmPool.pendingCreate, 2)
}

// TestPool_ReconcilePool_Create_LimitByMaxCapacity tests when the warm pool deviates from max deviation and the deviation
// is greater than the capacity of the pool, then only resources upto the max capacity are created
func TestPool_ReconcilePool_Create_LimitByMaxCapacity(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{}, 7)
	warmPool.pendingDelete = 4

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 0(actual WP + pending create) = 2, (deviation)2 >= (max deviation)1 => true, so
	// need to create (deviation)2 resources. But since remaining capacity is just 1, so we create 1 resource instead
	assert.Equal(t, worker.WarmPoolJob{Operations: worker.OperationCreate, ResourceCount: 1}, job)
	assert.Equal(t, warmPool.pendingCreate, 1)
}

// TestPool_ReconcilePool_Delete_NotRequired tests that if the warm pool is over the desired warm pool size but has not
// exceeded the max deviation then we don't return a delete job
func TestPool_ReconcilePool_Delete_NotRequired(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{res3, res4, res5}, 7)

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 3(actual WP) = -1, (-deviation)1 > (max deviation)1 => false, so no need delete
	assert.Equal(t, worker.WarmPoolJob{}, job)
	assert.Equal(t, warmPool.pendingDelete, 0)
}

// TestPool_ReconcilePool_Delete tests that if the warm pool is over the desired warm pool size and has exceed the max
// deviation then we issue a return a delete job
func TestPool_ReconcilePool_Delete(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, []string{res3, res4, res5, res6}, 7)

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 3(actual WP) = -1, (-deviation)1 > (max deviation)1 => false, so no need delete
	assert.Equal(t, worker.WarmPoolJob{Operations: worker.OperationDeleted,
		Resources: []string{res6, res5}, ResourceCount: 2}, job)
	assert.Equal(t, 2, warmPool.pendingDelete)
}
