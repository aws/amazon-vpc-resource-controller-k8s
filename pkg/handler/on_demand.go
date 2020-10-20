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

package handler

import (
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
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
func (h *onDemandResourceHandler) HandleCreate(resourceName string, requestCount int, pod *v1.Pod) (ctrl.Result, error) {
	resourceProvider, isPresent := h.providers[resourceName]

	if !isPresent {
		return ctrl.Result{}, fmt.Errorf("cannot handle resource %s, check canHandle before submitting jobs", resourceName)
	}

	job := worker.NewOnDemandCreateJob(pod.Namespace, pod.Name, requestCount)
	resourceProvider.SubmitAsyncJob(job)

	return ctrl.Result{}, nil
}

// HandleDelete reclaims the on demand resource by passing the Delete Job to the respective Worker
func (h *onDemandResourceHandler) HandleDelete(resourceName string, pod *v1.Pod) (ctrl.Result, error) {
	resourceProvider, isPresent := h.providers[resourceName]

	if !isPresent {
		return ctrl.Result{}, fmt.Errorf("cannot handle resource %s, check canHandle before submitting jobs", resourceName)
	}

	if _, ok := pod.Annotations[resourceName]; !ok {
		// Ignore the pod as it was not allocated the resource
		return ctrl.Result{}, nil
	}

	deleteJob := worker.NewOnDemandDeletedJob(pod.Spec.NodeName, pod.UID)
	resourceProvider.SubmitAsyncJob(deleteJob)

	return ctrl.Result{}, nil
}
