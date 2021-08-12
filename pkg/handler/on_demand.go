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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type onDemandResourceHandler struct {
	resourceProvider provider.ResourceProvider
	resourceName     string
	Log              logr.Logger
}

func (h *onDemandResourceHandler) GetProvider() provider.ResourceProvider {
	return h.resourceProvider
}

// NewOnDemandHandler returns a new on demand handler with all the workers that can handle particular resource types
func NewOnDemandHandler(log logr.Logger, resourceName string,
	ondDemandProvider provider.ResourceProvider) Handler {
	return &onDemandResourceHandler{
		resourceProvider: ondDemandProvider,
		resourceName:     resourceName,
		Log:              log,
	}
}

// HandleCreate provides the resource to the on demand resource by passing the Create Job to the respective Worker
func (h *onDemandResourceHandler) HandleCreate(requestCount int, pod *v1.Pod) (ctrl.Result, error) {
	job := worker.NewOnDemandCreateJob(pod.Namespace, pod.Name, requestCount)
	h.resourceProvider.SubmitAsyncJob(job)

	return ctrl.Result{}, nil
}

// HandleDelete reclaims the on demand resource by passing the Delete Job to the respective Worker
func (h *onDemandResourceHandler) HandleDelete(pod *v1.Pod) (ctrl.Result, error) {
	if _, ok := pod.Annotations[h.resourceName]; !ok {
		// Ignore the pod as it was not allocated the resource
		return ctrl.Result{}, nil
	}

	deleteJob := worker.NewOnDemandDeletedJob(pod.Spec.NodeName, pod.UID)
	h.resourceProvider.SubmitAsyncJob(deleteJob)

	return ctrl.Result{}, nil
}
