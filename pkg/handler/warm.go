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
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
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
)

type warmResourceHandler struct {
	lock              sync.Mutex
	log               logr.Logger
	k8sWrapper        k8s.K8sWrapper
	resourceProviders map[string]provider.ResourceProvider
}

func NewWarmResourceHandler(log logr.Logger, k8sWrapper k8s.K8sWrapper,
	resourceProviders map[string]provider.ResourceProvider) Handler {
	return &warmResourceHandler{
		log:               log,
		k8sWrapper:        k8sWrapper,
		resourceProviders: resourceProviders,
	}
}

func (w *warmResourceHandler) CanHandle(resourceName string) bool {
	_, ok := w.resourceProviders[resourceName]
	return ok
}

func (w *warmResourceHandler) HandleCreate(resourceName string, requestCount int, pod *v1.Pod) (ctrl.Result, error) {
	resourcePool, err := w.getResourcePool(resourceName, pod.Spec.NodeName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if _, present := pod.Annotations[resourceName]; present {
		// Pod has already been allocated the resource, skip the event
		return ctrl.Result{}, nil
	}

	resID, shouldReconcile, err := resourcePool.AssignResource(string(pod.UID))
	if err != nil {
		// Reconcile the pool before retrying or returning an error
		w.reconcilePool(shouldReconcile, resourceName, resourcePool)
		if err == pool.ErrResourceAreBeingCooledDown {
			return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfterWhenResourceCooling}, nil
		} else if err == pool.ErrResourcesAreBeingCreated ||
			err == pool.ErrWarmPoolEmpty {
			return ctrl.Result{Requeue: true, RequeueAfter: RequeueAfterWhenWPEmpty}, nil
		} else {
			return ctrl.Result{}, err
		}
	}

	err = w.k8sWrapper.AnnotatePod(pod.Namespace, pod.Name, resourceName, resID)
	if err != nil {
		_, errFree := resourcePool.FreeResource(string(pod.UID), resID)
		if errFree != nil {
			err = fmt.Errorf("failed to annotate %v, failed to free %v", err, errFree)
		}
	}

	w.reconcilePool(shouldReconcile, resourceName, resourcePool)

	return ctrl.Result{}, err
}

func (w *warmResourceHandler) reconcilePool(shouldReconcile bool, resourceName string, resourcePool pool.Pool) {
	if shouldReconcile {
		job := resourcePool.ReconcilePool()
		if job.Operations != worker.Operations("") {
			w.resourceProviders[resourceName].SubmitAsyncJob(job)
		}
	}
}

// HandleDelete deletes the resource used by the pod
func (w *warmResourceHandler) HandleDelete(resourceName string, pod *v1.Pod) (ctrl.Result, error) {
	resourcePool, err := w.getResourcePool(resourceName, pod.Spec.NodeName)
	if err != nil {
		return ctrl.Result{}, err
	}
	resource, present := pod.Annotations[resourceName]
	if !present {
		// Resource was not allocated to the pod
		return ctrl.Result{}, nil
	}
	shouldReconcile, err := resourcePool.FreeResource(string(pod.UID), resource)
	if err != nil {
		w.log.Error(err, "failed to free resource")
		return ctrl.Result{}, err
	}

	w.reconcilePool(shouldReconcile, resourceName, resourcePool)

	w.log.Info("successfully freed resource", "uid", string(pod.UID),
		"resource id", resourceName)

	return ctrl.Result{}, nil
}

// getResourcePool returns the resource pool for the given resource name and node name
func (w *warmResourceHandler) getResourcePool(resourceName string, nodeName string) (pool.Pool, error) {
	resourceProvider, ok := w.resourceProviders[resourceName]
	if !ok {
		return nil, fmt.Errorf("failed to find the requested resource, call canHandle before sending request")
	}
	resourcePool, found := resourceProvider.GetPool(nodeName)
	if !found {
		return nil, fmt.Errorf("failed to find the resource pool %s  for node %s",
			resourceName, nodeName)
	}

	return resourcePool, nil
}
