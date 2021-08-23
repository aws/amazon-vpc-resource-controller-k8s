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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	RequeueAfterWhenWPEmpty         = time.Millisecond * 600
	RequeueAfterWhenResourceCooling = time.Second * 20
	ReasonResourceAllocationFailed  = "ResourceAllocationFailed"
	ReasonResourceAllocated         = "ResourceAllocated"
)

type warmResourceHandler struct {
	log              logr.Logger
	APIWrapper       api.Wrapper
	resourceProvider provider.ResourceProvider
	resourceName     string
}

func NewWarmResourceHandler(log logr.Logger, wrapper api.Wrapper,
	resourceName string, resourceProviders provider.ResourceProvider) Handler {

	return &warmResourceHandler{
		log:              log,
		APIWrapper:       wrapper,
		resourceProvider: resourceProviders,
		resourceName:     resourceName,
	}
}

func (w *warmResourceHandler) HandleCreate(_ int, pod *v1.Pod) (ctrl.Result, error) {
	resourcePool, err := w.getResourcePool(pod.Spec.NodeName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if _, present := pod.Annotations[w.resourceName]; present {
		// Pod has already been allocated the resource, skip the event
		return ctrl.Result{}, nil
	}

	resID, shouldReconcile, err := resourcePool.AssignResource(string(pod.UID))
	if err != nil {
		// Reconcile the pool before retrying or returning an error
		w.reconcilePool(shouldReconcile, resourcePool)
		if err == pool.ErrResourceAreBeingCooledDown {
			w.APIWrapper.K8sAPI.BroadcastEvent(pod, ReasonResourceAllocationFailed,
				fmt.Sprintf("Resource %s are being cooled down, will retry in %s", w.resourceName,
					RequeueAfterWhenResourceCooling), v1.EventTypeWarning)
			return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfterWhenResourceCooling}, nil
		} else if err == pool.ErrResourcesAreBeingCreated || err == pool.ErrWarmPoolEmpty {
			w.APIWrapper.K8sAPI.BroadcastEvent(pod, ReasonResourceAllocationFailed,
				fmt.Sprintf("Warm pool for resource %s is currently empty, will retry in %s", w.resourceName,
					RequeueAfterWhenWPEmpty), v1.EventTypeWarning)
			return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfterWhenWPEmpty}, nil
		} else {
			return ctrl.Result{}, err
		}
	}

	err = w.APIWrapper.PodAPI.AnnotatePod(pod.Namespace, pod.Name, w.resourceName, resID)
	if err != nil {
		_, errFree := resourcePool.FreeResource(string(pod.UID), resID)
		if errFree != nil {
			err = fmt.Errorf("failed to annotate %v, failed to free %v", err, errFree)
		}
	}

	w.APIWrapper.K8sAPI.BroadcastEvent(pod, ReasonResourceAllocated, fmt.Sprintf("Allocated Resource %s: %s to the pod",
		w.resourceName, resID), v1.EventTypeNormal)

	w.log.Info("successfully allocated and annotated resource", "UID", string(pod.UID),
		"namespace", pod.Namespace, "name", pod.Name, "resource id", w.resourceName)

	w.reconcilePool(shouldReconcile, resourcePool)

	return ctrl.Result{}, err
}

func (w *warmResourceHandler) reconcilePool(shouldReconcile bool, resourcePool pool.Pool) {
	if shouldReconcile {
		job := resourcePool.ReconcilePool()
		if job.Operations != worker.Operations("") {
			w.resourceProvider.SubmitAsyncJob(job)
		}
	}
}

// HandleDelete deletes the resource used by the pod
func (w *warmResourceHandler) HandleDelete(pod *v1.Pod) (ctrl.Result, error) {
	resourcePool, err := w.getResourcePool(pod.Spec.NodeName)
	if err != nil {
		return ctrl.Result{}, err
	}
	resource, present := pod.Annotations[w.resourceName]
	if !present {
		// Resource was not allocated to the pod
		return ctrl.Result{}, nil
	}
	shouldReconcile, err := resourcePool.FreeResource(string(pod.UID), resource)
	if err != nil {
		w.log.Error(err, "failed to free resource")
		return ctrl.Result{}, err
	}

	w.reconcilePool(shouldReconcile, resourcePool)

	w.log.Info("successfully freed resource", "UID", string(pod.UID), "namespace", pod.Namespace,
		"name", pod.Name, "resource id", w.resourceName)

	return ctrl.Result{}, nil
}

// getResourcePool returns the resource pool for the given resource name and node name
func (w *warmResourceHandler) getResourcePool(nodeName string) (pool.Pool, error) {
	resourcePool, found := w.resourceProvider.GetPool(nodeName)
	if !found {
		return nil, fmt.Errorf("failed to find the resource pool %s for node %s",
			w.resourceName, nodeName)
	}

	return resourcePool, nil
}
