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
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
)

type onDemandResourceHandler struct {
	providers map[string]provider.ResourceProvider
	Log       logr.Logger
}

// NewOnDemandHandler returns a new on demand handler with all the workers that can handle particular resource types
func NewOnDemandHandler(log logr.Logger, ondDemandProviders map[string]provider.ResourceProvider) Handler {
	return &onDemandResourceHandler{
		providers: ondDemandProviders,
		Log:       log,
	}
}

// CanHandle returns true if the resource can be handled by the on demand handler
func (h *onDemandResourceHandler) CanHandle(resourceName string) bool {
	_, canHandle := h.providers[resourceName]
	return canHandle
}

// HandleCreate provides the resource to the on demand resource by passing the Create Job to the respective Worker
func (h *onDemandResourceHandler) HandleCreate(resourceName string, requestCount int, pod *v1.Pod) error {
	resourceProvider, isPresent := h.providers[resourceName]

	if !isPresent {
		return fmt.Errorf("cannot handle resource %s, check canHandle before submitting jobs", resourceName)
	}

	job := worker.NewOnDemandCreateJob(string(pod.UID), pod.Namespace, pod.Name, requestCount)
	resourceProvider.SubmitAsyncJob(job)

	return nil
}

func (h *onDemandResourceHandler) HandleDeleting(resourceName string, pod *v1.Pod) error {
	resourceProvider, isPresent := h.providers[resourceName]

	if !isPresent {
		return fmt.Errorf("cannot handle resource %s, check canHandle before submitting jobs", resourceName)
	}

	job := worker.NewOnDemandDeletingJob(string(pod.UID), pod.Namespace, pod.Name, pod.Spec.NodeName)
	resourceProvider.SubmitAsyncJob(job)

	return nil
}

// HandleDelete reclaims the on demand resource by passing the Delete Job to the respective Worker
func (h *onDemandResourceHandler) HandleDelete(podNamespace string, podName string) error {
	deleteJob := worker.NewOnDemandDeletedJob(podNamespace, podName)
	// Since we don't have the node name nor the resource used by the pod after it's deleted, submit the job to all
	// the providers.
	for _, resourceProvider := range h.providers {
		resourceProvider.SubmitAsyncJob(deleteJob)
	}
	return nil
}
