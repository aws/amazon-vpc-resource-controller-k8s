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

package worker

import "k8s.io/apimachinery/pkg/types"

// Operations are the supported operations for on demand resource handler
type Operations string

const (
	OperationProcessDeleteQueue Operations = "ProcessDeleteQueue"
	// OperationReconcile represents a reconcile operation that reclaims dangling network interfaces
	OperationReconcile Operations = "Reconcile"
	// OperationCreate represents pods that are in created state
	OperationCreate Operations = "Create"
	// OperationDelete represents pods that have been deleted
	OperationDeleted Operations = "Deleted"
	// OperationDeleting represents pods that are being deleted
	OperationDeleting Operations = "Deleting"
)

// OnDemandJob represents the job that will be executed by the respective worker
type OnDemandJob struct {
	// Operation is the type of operation to perform on the given pod
	Operation Operations
	// UID is the unique ID of the pod
	UID types.UID
	// PodName is the name of the pod
	PodName string
	// PodNamespace is the pod's namespace
	PodNamespace string
	// RequestCount is the number of resources to create for operations of type Create. Optional for other operations
	RequestCount int
	// NodeName is the k8s node name
	NodeName string
}

// NewOnDemandDeleteJob returns an on demand job for operation Create or Update
func NewOnDemandCreateJob(uid string, podNamespace string, podName string, requestCount int) OnDemandJob {
	return OnDemandJob{
		Operation:    OperationCreate,
		UID:          types.UID(uid),
		PodName:      podName,
		PodNamespace: podNamespace,
		RequestCount: requestCount,
	}
}

// NewOnDemandDeletingJob returns an on demand job for operation Deleting
func NewOnDemandDeletingJob(uid string, podNamespace string, podName string, nodeName string) OnDemandJob {
	return OnDemandJob{
		Operation:    OperationDeleting,
		UID:          types.UID(uid),
		PodName:      podName,
		PodNamespace: podNamespace,
		NodeName:     nodeName,
	}
}

// NewOnDemandDeleteJob returns an on demand job for operation Deleted
func NewOnDemandDeletedJob(podNamespace string, podName string) OnDemandJob {
	return OnDemandJob{
		Operation:    OperationDeleted,
		PodName:      podName,
		PodNamespace: podNamespace,
	}
}

// NewOnDemandReconcileJob returns a reconcile job
func NewOnDemandReconcileJob(nodeName string) OnDemandJob {
	return OnDemandJob{
		Operation: OperationReconcile,
		NodeName:  nodeName,
	}
}

// NewOnDemandProcessDeleteQueueJob returns a process delete queue job
func NewOnDemandProcessDeleteQueueJob(nodeName string) OnDemandJob {
	return OnDemandJob{
		Operation: OperationProcessDeleteQueue,
		NodeName:  nodeName,
	}
}

// WarmPoolJob represents the job for a resource handler for warm pool resources
type WarmPoolJob struct {
	// Operation is the type of operation on warm pool
	Operations Operations
	// Resources can hold the resource to delete or the created resources
	Resources []string
	// ResourceCount is the number of resource to be created
	ResourceCount int
}

// NewWarmPoolCreateJob returns a job on warm pool of resource
func NewWarmPoolCreateJob(count int) WarmPoolJob {
	return WarmPoolJob{
		Operations: OperationCreate,
		ResourceCount: count,
	}
}

func NewWarmPoolDeleteJob(resourcesToDelete []string) WarmPoolJob {
	return WarmPoolJob{
		Operations: OperationDeleted,
		Resources:  resourcesToDelete,
		ResourceCount: len(resourcesToDelete),
	}
}
