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

package handler

import (
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
)

type onDemandResourceHandler struct {
	resourceWorkers map[string]worker.Worker
	Log             logr.Logger
}

// Operations are the supported operations for on demand resource handler
type Operations string

const (
	// Create represent create resource operation
	Create Operations = "Create"
	// Delete represents delete resource operation
	Delete Operations = "Delete"
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

// NewOnDemandHandler returns a new on demand handler with all the workers that can handle particular resource types
func NewOnDemandHandler(log logr.Logger, onDemandResHandlers map[string]worker.Worker) Handler {
	return &onDemandResourceHandler{
		resourceWorkers: onDemandResHandlers,
		Log:             log,
	}
}

// CanHandle returns true if the resource can be handled by the on demand handler
func (h *onDemandResourceHandler) CanHandle(resourceName string) bool {
	_, canHandle := h.resourceWorkers[resourceName]
	return canHandle
}

// HandleCreate provides the resource to the on demand resource by passing the Create Job to the respective Worker
func (h *onDemandResourceHandler) HandleCreate(resourceName string, requestCount int64, pod *v1.Pod) error {
	logger := h.Log.WithValues("resource", resourceName, "pod NS", pod.Namespace, "pod name", pod.Name)
	job := OnDemandJob{
		Operation:    Create,
		PodName:      pod.Name,
		PodNamespace: pod.Namespace,
		RequestCount: requestCount,
	}

	worker := h.resourceWorkers[resourceName]

	err := worker.SubmitJob(job)

	if err != nil {
		logger.Error(err, "failed to process create request")
		return err
	}

	return nil
}

// HandleDelete reclaims the on demand resource by passing the Delete Job to the respective Worker
func (h *onDemandResourceHandler) HandleDelete(resourceName string, pod *v1.Pod) error {
	logger := h.Log.WithValues("resource", resourceName, "pod NS", pod.Namespace, "pod name", pod.Name)
	job := OnDemandJob{
		Operation:    Delete,
		PodName:      pod.Name,
		PodNamespace: pod.Namespace,
	}

	worker := h.resourceWorkers[resourceName]

	err := worker.SubmitJob(job)

	if err != nil {
		logger.Error(err, "failed to process delete request")
		return err
	}

	return nil
}
