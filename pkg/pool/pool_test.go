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
	"reflect"
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

	nodeName = "node-name"

	pod1 = "test/pod-1"
	pod2 = "default/pod-2"
	pod3 = "test/pod-3"

	//grp1, grp2, grp3, grp4, grp5, grp6, grp7 = "grp-1", "grp-2", "grp-3", "grp-4", "grp-5", "grp-6", "grp-7"
	res1, res2, res3, res4, res5, res6, res7 = "res-1", "res-2", "res-3", "res-4", "res-5", "res-6", "res-7"

	warmPoolResources = map[string][]Resource{
		res3: {
			{GroupID: res3, ResourceID: res3},
		},
	}

	usedResources = map[string]Resource{
		pod1: {GroupID: res1, ResourceID: res1},
		pod2: {GroupID: res2, ResourceID: res2},
	}
)

func getMockPool(poolConfig *config.WarmPoolConfig, usedResources map[string]Resource,
	warmResources map[string][]Resource, capacity int, isPDPool bool) *pool {

	usedResourcesCopy := map[string]Resource{}
	for k, v := range usedResources {
		usedResourcesCopy[k] = Resource{GroupID: v.GroupID, ResourceID: v.ResourceID}
	}

	warmResourcesCopy := make(map[string][]Resource, len(warmResources))
	for k, resources := range warmResources {
		var resourcesCopy []Resource
		for _, res := range resources {
			resourcesCopy = append(resourcesCopy, Resource{GroupID: res.GroupID, ResourceID: res.ResourceID})
		}
		warmResourcesCopy[k] = resourcesCopy
	}

	pool := &pool{
		log:            zap.New(zap.UseDevMode(true)).WithValues("pool", "res-id/node-name"),
		warmPoolConfig: poolConfig,
		usedResources:  usedResourcesCopy,
		warmResources:  warmResourcesCopy,
		capacity:       capacity,
		isPDPool:       isPDPool,
	}

	return pool
}

func TestPool_NewResourcePool(t *testing.T) {
	pool := NewResourcePool(zap.New(), poolConfig, usedResources, warmPoolResources, nodeName, 5, false)
	assert.NotNil(t, pool)
}

// TestPool_AssignResource tests resource is allocated to pod if present in the warm pool
func TestPool_AssignResource(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, warmPoolResources, 5, false)

	resourceID, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.NoError(t, err)
	assert.True(t, shouldReconcile)
	assert.Equal(t, res3, resourceID)
	assert.Equal(t, res3, warmPool.usedResources[pod3].ResourceID)
}

// TestPool_AssignResource_AlreadyAssigned tests error is returned if a pod already was allocated a resource
func TestPool_AssignResource_AlreadyAssigned(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 2, false)

	_, shouldReconcile, err := warmPool.AssignResource(pod1)

	assert.Error(t, ErrResourceAlreadyAssigned, err)
	assert.False(t, shouldReconcile)
}

// TestPool_AssignResource_AtCapacity tests error is returned if pool cannot allocate anymore resources
func TestPool_AssignResource_AtCapacity(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 2, false)

	_, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.Error(t, ErrPoolAtMaxCapacity, err)
	assert.False(t, shouldReconcile)
}

// TestPool_AssignResources_WarmPoolEmpty tests error is returned if the warm pool is empty
func TestPool_AssignResource_WarmPoolEmpty(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 5, false)

	_, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.Error(t, ErrWarmPoolEmpty, err)
	assert.True(t, shouldReconcile)
}

// TestPool_AssignResource_CoolDown tests error is returned if the resource are being cooled down
func TestPool_AssignResource_CoolDown(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)
	warmPool.coolDownQueue = append(warmPool.coolDownQueue, CoolDownResource{
		Resource: Resource{GroupID: res3, ResourceID: res3}})

	_, shouldReconcile, err := warmPool.AssignResource(pod3)

	assert.Error(t, ErrResourceAreBeingCooledDown, err)
	assert.False(t, shouldReconcile)
}

// TestPool_FreeResource tests no error is returned on freeing a used resource
func TestPool_FreeResource(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)

	shouldReconcile, err := warmPool.FreeResource(pod2, res2)
	_, present := warmPool.usedResources[pod2]

	assert.NoError(t, err)
	assert.True(t, shouldReconcile)
	assert.False(t, present)
	assert.Equal(t, res2, warmPool.coolDownQueue[0].Resource.ResourceID)
}

// TestPool_FreeResource_isNotAssigned test error is returned if trying to free a resource that doesn't belong to any
// owner
func TestPool_FreeResource_isNotAssigned(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)

	shouldReconcile, err := warmPool.FreeResource(pod3, res3)

	assert.Error(t, ErrResourceDoesntExist, err)
	assert.False(t, shouldReconcile)
}

// TestPool_FreeResource_IncorrectOwner tests error is returned if incorrect owner id is passed
func TestPool_FreeResource_IncorrectOwner(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)

	shouldReconcile, err := warmPool.FreeResource(pod2, res1)

	assert.Error(t, ErrIncorrectResourceOwner, err)
	assert.False(t, shouldReconcile)
}

// TestPool_UpdatePool_OperationCreate_Succeed tests resources are added to the warm pool if resource are created
// successfully
func TestPool_UpdatePool_OperationCreate_Succeed(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)

	createdResources := []string{res3, res4}
	shouldReconcile := warmPool.UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     createdResources,
		ResourceCount: 2,
	}, true)

	warmResources := make(map[string][]Resource)
	warmResources[res3] = []Resource{{GroupID: res3, ResourceID: res3}}
	warmResources[res4] = []Resource{{GroupID: res4, ResourceID: res4}}
	// Resources should be added to warm pool
	assert.Equal(t, warmResources, warmPool.warmResources)
	assert.False(t, shouldReconcile)
}

// TestPool_UpdatePool_OperationCreate_Failed tests that reconciler is triggered when create operation fails
func TestPool_UpdatePool_OperationCreate_Failed(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)

	shouldReconcile := warmPool.UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     nil,
		ResourceCount: 2,
	}, false)

	assert.True(t, shouldReconcile)
	assert.True(t, warmPool.reSyncRequired)
}

// TestPool_UpdatePool_OperationDelete tests that reconciler is not triggered again if delete operation succeeds
func TestPool_UpdatePool_OperationDelete_Succeed(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)

	shouldReconcile := warmPool.UpdatePool(&worker.WarmPoolJob{
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
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)
	failedResources := []string{res3, res4}
	shouldReconcile := warmPool.UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     failedResources,
		ResourceCount: 2,
	}, false)

	failed := make(map[string][]Resource)
	failed[res3] = []Resource{{GroupID: res3, ResourceID: res3}}
	failed[res4] = []Resource{{GroupID: res4, ResourceID: res4}}

	// Assert resources are added back to the warm pool
	assert.Equal(t, failed, warmPool.warmResources)
	assert.True(t, shouldReconcile)
}

// TestPool_ProcessCoolDownQueue tests that item are added back to the warm pool once they have cooled down
func TestPool_ProcessCoolDownQueue(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)
	warmPool.coolDownQueue = []CoolDownResource{
		{Resource: Resource{GroupID: res3, ResourceID: res3}, DeletionTimestamp: time.Now().Add(-time.Second * 33)},
		{Resource: Resource{GroupID: res4, ResourceID: res4}, DeletionTimestamp: time.Now().Add(-time.Second * 32)},
		{Resource: Resource{GroupID: res5, ResourceID: res5}, DeletionTimestamp: time.Now().Add(-time.Second * 10)},
	}

	expectedWarm := make(map[string][]Resource)
	expectedWarm[res3] = []Resource{{GroupID: res3, ResourceID: res3}}
	expectedWarm[res4] = []Resource{{GroupID: res4, ResourceID: res4}}

	needFurtherProcessing := warmPool.ProcessCoolDownQueue()

	// As resource 5 is yet not deleted
	assert.True(t, needFurtherProcessing)
	// Assert resources are added back to the warm pool
	assert.Equal(t, expectedWarm, warmPool.warmResources)
	// Assert resource that is not cooled down is still in the queue
	assert.Equal(t, res5, warmPool.coolDownQueue[0].Resource.ResourceID)
}

// TestPool_ProcessCoolDownQueue_NoFurtherProcessingRequired tests that if all the items of the cool down queue are
// processed it should be empty
func TestPool_ProcessCoolDownQueue_NoFurtherProcessingRequired(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 3, false)

	warmPool.coolDownQueue = []CoolDownResource{
		{Resource: Resource{GroupID: res3, ResourceID: res3}, DeletionTimestamp: time.Now().Add(-time.Second * 33)}}

	needFurtherProcessing := warmPool.ProcessCoolDownQueue()

	// Cool down queue should now be empty
	assert.Zero(t, len(warmPool.coolDownQueue))
	assert.False(t, needFurtherProcessing)
}

// TestPool_getPendingResources tests total pending resource returns the pending create and pending delete items
func TestPool_getPendingResources(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 7, false)
	warmPool.pendingCreate = 2
	warmPool.pendingDelete = 3

	assert.Equal(t, 5, warmPool.pendingDelete+warmPool.pendingCreate)
}

// TestPool_ReconcilePool_MaxCapacity tests reconciliation doesn't happen if the pod is at max capacity
func TestPool_ReconcilePool_MaxCapacity(t *testing.T) {
	warmResources := make(map[string][]Resource)
	warmResources[res1] = []Resource{{GroupID: res1, ResourceID: res1}}

	warmPool := getMockPool(poolConfig, usedResources, warmResources, 6, false)
	warmPool.pendingCreate = 1
	warmPool.pendingDelete = 1

	warmPool.coolDownQueue = append(warmPool.coolDownQueue, CoolDownResource{
		Resource: Resource{GroupID: res4, ResourceID: res4}})

	// used = 2, warm = 1, total pending = 2, cool down queue = 1, total = 6 & capacity = 6 => At max capacity
	job := warmPool.ReconcilePool()

	// no need to reconcile
	assert.Equal(t, &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}, job)
}

// TestPool_ReconcilePool_NotRequired tests if the deviation form warm pool is equal to or less than the max deviation
// then reconciliation is not triggered
func TestPool_ReconcilePool_NotRequired(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 7, false)
	warmPool.pendingCreate = 1

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 1(actual WP + pending create) = 1, (deviation)1 > (max deviation)1 => false,
	// so no need create right now
	assert.Equal(t, &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}, job)
}

// TestPool_ReconcilePool tests job with operation type create is returned when the warm pool deviates form max deviation
func TestPool_ReconcilePool_Create(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 7, false)

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 0(actual WP + pending create) = 0, (deviation)0 > (max deviation)1 => true,
	// create (deviation)2 resources
	assert.Equal(t, &worker.WarmPoolJob{Operations: worker.OperationCreate, ResourceCount: 2}, job)
	assert.Equal(t, warmPool.pendingCreate, 2)
}

// TestPool_ReconcilePool_Create_LimitByMaxCapacity tests when the warm pool deviates from max deviation and the deviation
// is greater than the capacity of the pool, then only resources upto the max capacity are created
func TestPool_ReconcilePool_Create_LimitByMaxCapacity(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 7, false)
	warmPool.pendingDelete = 4

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 0(actual WP + pending create) = 2, (deviation)2 >= (max deviation)1 => true, so
	// need to create (deviation)2 resources. But since remaining capacity is just 1, so we create 1 resource instead
	assert.Equal(t, &worker.WarmPoolJob{Operations: worker.OperationCreate, ResourceCount: 1}, job)
	assert.Equal(t, warmPool.pendingCreate, 1)
}

// TestPool_ReconcilePool_Delete_NotRequired tests that if the warm pool is over the desired warm pool size but has not
// exceeded the max deviation then we don't return a delete job
func TestPool_ReconcilePool_Delete_NotRequired(t *testing.T) {
	warmResources := make(map[string][]Resource)
	warmResources[res3] = []Resource{{GroupID: res3, ResourceID: res3}}
	warmResources[res4] = []Resource{{GroupID: res4, ResourceID: res4}}
	warmResources[res5] = []Resource{{GroupID: res5, ResourceID: res5}}
	warmPool := getMockPool(poolConfig, usedResources, warmResources, 7, false)

	job := warmPool.ReconcilePool()

	// deviation = 2(desired WP) - 3(actual WP) = -1, (-deviation)1 > (max deviation)1 => false, so no need delete
	assert.Equal(t, &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}, job)
	assert.Equal(t, warmPool.pendingDelete, 0)
}

// TestPool_ReconcilePool_Delete tests that if the warm pool is over the desired warm pool size and has exceed the max
// deviation then we issue a return a delete job
func TestPool_ReconcilePool_Delete(t *testing.T) {
	warmResources := make(map[string][]Resource)
	warmResources[res3] = []Resource{{GroupID: res3, ResourceID: res3}}
	warmResources[res4] = []Resource{{GroupID: res4, ResourceID: res4}}
	warmResources[res5] = []Resource{{GroupID: res5, ResourceID: res5}}
	warmResources[res6] = []Resource{{GroupID: res6, ResourceID: res6}}
	warmPool := getMockPool(poolConfig, usedResources, warmResources, 7, false)

	job := warmPool.ReconcilePool()
	// deviation = 2(desired WP) - 4(actual WP) = -2, (-deviation)2 > (max deviation)1 => true, need to delete
	// since the warm resources is a map, there is no particular order to delete ip address from secondary ip pool,
	// we can't assert which two ips would get deleted here
	assert.Equal(t, 2, job.ResourceCount)
	assert.Equal(t, worker.OperationDeleted, job.Operations)
	assert.Equal(t, 2, warmPool.pendingDelete)
}

func TestPool_Reconcile_ReSync(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, map[string][]Resource{}, 4, false)

	warmPool.reSyncRequired = true
	warmPool.nodeName = nodeName

	job := warmPool.ReconcilePool()
	assert.Equal(t, job, worker.NewWarmPoolReSyncJob(nodeName))

	warmPool.pendingDelete = 1
	job = warmPool.ReconcilePool()
	assert.Equal(t, job, &worker.WarmPoolJob{
		Operations: worker.OperationReconcileNotRequired,
	})
}

func TestPool_ReSync(t *testing.T) {
	warm := make(map[string][]Resource)
	warm[res3] = []Resource{{GroupID: res3, ResourceID: res3}}
	warm[res4] = []Resource{{GroupID: res4, ResourceID: res4}}

	warm2 := make(map[string][]Resource)
	warm2[res3] = []Resource{{GroupID: res3, ResourceID: res3}}
	warm2[res7] = []Resource{{GroupID: res7, ResourceID: res7}}

	coolDown := []CoolDownResource{
		{Resource: Resource{GroupID: res5, ResourceID: res5}},
		{Resource: Resource{GroupID: res6, ResourceID: res6}},
	}

	tests := []struct {
		name string

		shouldResync bool

		warmPool      map[string][]Resource
		coolDownQueue []CoolDownResource

		expectedWarmPool      map[string][]Resource
		expectedCoolDownQueue []CoolDownResource

		upstreamResources []string
	}{
		{
			name:                  "when re-sync flag is not set, don't sync",
			shouldResync:          false,
			warmPool:              warm,
			coolDownQueue:         coolDown,
			expectedWarmPool:      warm,
			expectedCoolDownQueue: coolDown,
		},
		{
			name:                  "when upstream and pool state match",
			shouldResync:          true,
			warmPool:              warm,
			coolDownQueue:         coolDown,
			expectedWarmPool:      warm,
			expectedCoolDownQueue: coolDown,
			upstreamResources:     []string{res1, res2, res3, res4, res5, res6},
		},
		{
			name:                  "when upstream and pool are out of sync",
			shouldResync:          true,
			warmPool:              warm,
			coolDownQueue:         coolDown,
			expectedWarmPool:      warm2,
			expectedCoolDownQueue: []CoolDownResource{{Resource: Resource{GroupID: res6, ResourceID: res6}}},
			//Resource 4, 6 and got deleted from upstream and resource 7 added
			upstreamResources: []string{res1, res2, res3, res6, res7},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			warmPool := getMockPool(poolConfig, usedResources, test.warmPool, 7, false)
			warmPool.coolDownQueue = test.coolDownQueue
			warmPool.reSyncRequired = test.shouldResync

			warmPool.ReSync(test.upstreamResources)

			assert.False(t, warmPool.reSyncRequired)
			assert.True(t, reflect.DeepEqual(warmPool.usedResources, usedResources))
			assert.True(t, reflect.DeepEqual(warmPool.warmResources, test.expectedWarmPool))
			assert.ElementsMatch(t, warmPool.coolDownQueue, test.expectedCoolDownQueue)
		})
	}
}

func TestPool_GetAssignedResource(t *testing.T) {
	warmPool := getMockPool(poolConfig, usedResources, nil, 7, false)

	resID, found := warmPool.GetAssignedResource(pod1)
	assert.True(t, found)
	assert.Equal(t, resID, res1)

	resID, found = warmPool.GetAssignedResource(pod3)
	assert.False(t, found)
	assert.Equal(t, resID, "")
}

func TestPool_Introspect(t *testing.T) {
	coolingResources := []CoolDownResource{{
		Resource: Resource{GroupID: res6, ResourceID: res6},
	}}
	warmPool := getMockPool(poolConfig, usedResources, warmPoolResources, 7, false)
	warmPool.coolDownQueue = coolingResources
	resp := warmPool.Introspect()
	assert.True(t, reflect.DeepEqual(warmPool.usedResources, resp.UsedResources))
	assert.True(t, reflect.DeepEqual(warmPool.warmResources, resp.WarmResources))
	assert.ElementsMatch(t, warmPool.coolDownQueue, resp.CoolingResources)
}
