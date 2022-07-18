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
	"context"
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/ipam"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ipamResourceHandler struct {
	log              logr.Logger
	APIWrapper       api.Wrapper
	resourceProvider provider.ResourceProvider
	resourceName     string
	ctx              context.Context
}

func NewIpamResourceHandler(log logr.Logger, wrapper api.Wrapper,
	resourceName string, resourceProviders provider.ResourceProvider, ctx context.Context) Handler {

	return &ipamResourceHandler{
		log:              log,
		APIWrapper:       wrapper,
		resourceProvider: resourceProviders,
		resourceName:     resourceName,
		ctx:              ctx,
	}
}

func (w *ipamResourceHandler) HandleCreate(_ int, pod *v1.Pod) (ctrl.Result, error) {
	resourceIpam, err := w.getResourceIPAM(pod.Spec.NodeName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if _, present := pod.Annotations[w.resourceName]; present {
		// Pod has already been allocated the resource, skip the event
		return ctrl.Result{}, nil
	}

	log := w.log.WithValues("UID", string(pod.UID), "namespace",
		pod.Namespace, "name", pod.Name)

	resourceInfo, shouldReconcile, err := resourceIpam.AssignResource(string(pod.UID))
	if err != nil {
		// Reconcile the pool before retrying or returning an error
		w.reconcilePool(shouldReconcile, resourceIpam)
		switch err {
		case pool.ErrResourceAreBeingCooledDown:
			log.V(1).Info("resources are currently being cooled down, will retry")
			w.APIWrapper.K8sAPI.BroadcastEvent(pod, ReasonResourceAllocationFailed,
				fmt.Sprintf("Resource %s are being cooled down, will retry in %s",
					w.resourceName, RequeueAfterWhenResourceCooling), v1.EventTypeWarning)
			return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfterWhenResourceCooling}, nil
		case pool.ErrResourcesAreBeingCreated, pool.ErrWarmPoolEmpty:
			log.V(1).Info("resources are currently being created or warm pool is empty, will retry")
			w.APIWrapper.K8sAPI.BroadcastEvent(pod, ReasonResourceAllocationFailed,
				fmt.Sprintf("Warm pool for resource %s is currently empty, will retry in %s",
					w.resourceName, RequeueAfterWhenWPEmpty), v1.EventTypeWarning)
			return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfterWhenWPEmpty}, nil
		case pool.ErrResourceAlreadyAssigned:
			// The Pod may already have the request annotated, however the cache may not have
			// may not reflect the change immediately.
			pod, err := w.APIWrapper.PodAPI.GetPodFromAPIServer(w.ctx, pod.Namespace, pod.Name)
			if err != nil {
				return ctrl.Result{}, err
			}
			resourceID, present := pod.Annotations[w.resourceName]
			if present {
				log.Info("cache had stale entry, pod already has resource",
					"resource from annotation", resourceID,
					"resource from data store", resourceInfo.ResourceID)
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		default:
			return ctrl.Result{}, err
		}
	}

	err = w.APIWrapper.PodAPI.AnnotatePod(pod.Namespace, pod.Name, pod.UID, w.resourceName, resourceInfo.ResourceID)
	if err != nil {
		_, errFree := resourceIpam.FreeResource(string(pod.UID), resourceInfo.ResourceID)
		if errFree != nil {
			err = fmt.Errorf("failed to annotate %v, failed to free %v", err, errFree)
		}
	}

	w.APIWrapper.K8sAPI.BroadcastEvent(pod, ReasonResourceAllocated,
		fmt.Sprintf("Allocated Resource %s: %s to the pod", w.resourceName, resourceInfo.ResourceID), v1.EventTypeNormal)

	log.Info("successfully allocated and annotated resource", "resource id", resourceInfo.ResourceID)

	w.reconcilePool(shouldReconcile, resourceIpam)

	return ctrl.Result{}, err
}

func (w *ipamResourceHandler) reconcilePool(shouldReconcile bool, resourceIpam ipam.Ipam) {
	if shouldReconcile {
		job := resourceIpam.ReconcilePool()
		if job.Operations != worker.Operations("") {
			w.resourceProvider.SubmitAsyncJob(job)
		}
	}
}

// HandleDelete deletes the resource used by the pod
func (w *ipamResourceHandler) HandleDelete(pod *v1.Pod) (ctrl.Result, error) {
	resourceIpam, err := w.getResourceIPAM(pod.Spec.NodeName)
	if err != nil {
		w.log.Error(err, "failed to find resource pool for node",
			"node", pod.Spec.NodeName)
		return ctrl.Result{}, nil
	}
	resourceID, present := pod.Annotations[w.resourceName]
	if !present {
		// When a Pod with TerminationGracePeriodSeconds set to 0 is created and
		// deleted immediately, the delete event doesnt' contain the resource
		// annotation, in such cases, query the data store to get the assigned resource
		resourceInfo, present := resourceIpam.GetAssignedResource(string(pod.UID))
		resourceID = resourceInfo.ResourceID
		if !present {
			return ctrl.Result{}, nil
		}
		w.log.Info("resource ID was not found in annotation, fetched from pool",
			"resource from data store", resourceID)
	}
	log := w.log.WithValues("UID", string(pod.UID), "namespace", pod.Namespace,
		"name", pod.Name, "resource id", resourceID)

	// Handle Delete can be invoked multiple times for same object. For instance
	// Once a Pod has Succeeded/Failed and once the object is actually deleted
	shouldReconcile, err := resourceIpam.FreeResource(string(pod.UID), resourceID)
	if err != nil {
		if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
			log.V(1).Info("failed to free resource, resource likely freed when pod succeed/failed")
			return ctrl.Result{}, nil
		}
		// Only Log the error, since this error is not retryable
		log.Error(err, "failed to free resource")
		return ctrl.Result{}, nil
	}

	w.reconcilePool(shouldReconcile, resourceIpam)

	log.Info("successfully freed resource")

	return ctrl.Result{}, nil
}

// getResourcePool returns the resource pool for the given resource name and node name
func (w *ipamResourceHandler) getResourceIPAM(nodeName string) (ipam.Ipam, error) {
	resourceIpam, found := w.resourceProvider.GetIPAM(nodeName)
	if !found {
		return nil, fmt.Errorf("failed to find the resource pool %s for node %s",
			w.resourceName, nodeName)
	}

	return resourceIpam, nil
}
