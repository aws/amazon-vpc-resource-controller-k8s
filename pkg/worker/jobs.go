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

// Operations are the supported operations for on demand resource handler
type Operations string

const (
	// Create represent create resource operation
	OperationCreate Operations = "Create"
	// Delete represents delete resource operation
	OperationDelete Operations = "Delete"
)

// OnDemandJob represents the job that will be executed by the respective worker
type OnDemandJob struct {
	// Operation is the type of operation to perform on the given pod
	Operation Operations
	// PodName is the name of the pod
	PodName string
	// PodNamespace is the pod's namespace
	PodNamespace string
	// RequestCount is the number of resources to create for operations of type Create. Optional for other operations
	RequestCount int64
}

// NewOnDemandDeleteJob returns a job type OnDemand with create operation
func NewOnDemandCreateJob(podNamespace string, podName string, requestCount int64) OnDemandJob {
	return OnDemandJob{
		Operation:    OperationCreate,
		PodName:      podName,
		PodNamespace: podNamespace,
		RequestCount: requestCount,
	}
}

// NewOnDemandDeleteJob returns a job type OnDemand with delete operation
func NewOnDemandDeleteJob(podNamespace string, podName string) OnDemandJob {
	return OnDemandJob{
		Operation:    OperationDelete,
		PodName:      podName,
		PodNamespace: podNamespace,
	}
}
