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

package controllers

import (
	"context"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log            logr.Logger
	Scheme         *runtime.Scheme
	Handlers       []handler.Handler
	Manager        node.Manager
	lock           sync.Mutex // guards the following items
	DeletePodQueue map[string]*corev1.Pod
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create

// Reconcile reconciles the VPC Resources for the pod. Resources allocations are delegated to the respective handlers
// based on the resource type
func (r *PodReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()

	pod := &corev1.Pod{}
	var isDeleteEvent bool
	if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			// Pod is deleted and no longer available in cache, get the pod cached inside the delete event listener
			pod = r.removeFromDeletedObjectStore(req.NamespacedName.String())
			if pod == nil {
				// If pod is not found in the delete queue too, ignore the event
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			isDeleteEvent = true
		}
	}

	logger := r.Log.WithValues("UID", pod.UID, "pod", req.NamespacedName, "node", pod.Spec.NodeName)

	node, managed := r.Manager.GetNode(pod.Spec.NodeName)
	if !managed {
		logger.V(1).Info("pod's node is not managed, skipping pod event")
		return ctrl.Result{}, nil
	} else if !node.IsReady() {
		logger.Info("pod's node is not ready to handle request yet, will retry")
		return ctrl.Result{Requeue: true, RequeueAfter: time.Millisecond * 500}, nil
	}

	aggregateResources := r.getAggregateResources(pod)

	// For each resource, if a handler can allocate/de-allocate a resource then delegate the allocation/de-allocation
	// task to the respective handler
	for resourceName, totalCount := range aggregateResources {
		for _, resourceHandler := range r.Handlers {
			if resourceHandler.CanHandle(resourceName) {
				var err error
				var result ctrl.Result
				if isDeleteEvent {
					result, err = resourceHandler.HandleDelete(resourceName, pod)
				} else {
					result, err = resourceHandler.HandleCreate(resourceName, int(totalCount), pod)
				}
				if err != nil || result.Requeue == true {
					return result, err
				}
				logger.V(1).Info("handled resource without error",
					"resource", resourceName, "is delete event", isDeleteEvent)
			}
		}
	}

	return ctrl.Result{}, nil
}

// getAggregateResources computes the aggregate resources across all containers for each resource type
func (r *PodReconciler) getAggregateResources(pod *corev1.Pod) map[string]int64 {
	aggregateResources := make(map[string]int64)
	for _, container := range pod.Spec.Containers {
		for resourceName, request := range container.Resources.Requests {
			quantity, isConvertible := request.AsInt64()
			if isConvertible {
				resCount := aggregateResources[resourceName.String()]
				aggregateResources[resourceName.String()] = resCount + quantity
			}
		}
	}
	return aggregateResources
}

// AddDeletedObjectToQueue adds the delete object to the data store by intercepting the delete event before it is
// processed by the kube-builder watcher.
func (r *PodReconciler) AddDeletedObjectToQueue(deleteEvent event.DeleteEvent) bool {
	obj := deleteEvent.Object.DeepCopyObject()
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		r.Log.Info("not allowing object of non pod type", "kind", deleteEvent.Object.GetObjectKind())
		return false
	}

	namespacedName := types.NamespacedName{
		Namespace: deleteEvent.Meta.GetNamespace(),
		Name:      deleteEvent.Meta.GetName(),
	}
	r.putToDeletedObjectStore(namespacedName.String(), pod)
	return true
}

// pushToDeleteQueue puts the pod object into the map storing all the deleted objects that's queue item is not yet
// processed
func (r *PodReconciler) putToDeletedObjectStore(namespacedName string, pod *corev1.Pod) {
	r.lock.Lock()
	defer r.lock.Unlock()

	r.DeletePodQueue[namespacedName] = pod
}

// removeFromDeleteObjectMap removes the pod object from the map and returns it, after calling this the deleted object
// will be permanently forgotten
func (r *PodReconciler) removeFromDeletedObjectStore(namespacedName string) *corev1.Pod {
	r.lock.Lock()
	defer r.lock.Unlock()

	pod, found := r.DeletePodQueue[namespacedName]
	if !found {
		// Should not happen
		r.Log.Info("failed to find the pod in the delete queue %s", namespacedName)
		return nil
	}

	delete(r.DeletePodQueue, namespacedName)
	return pod
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).WithEventFilter(predicate.Funcs{
		DeleteFunc: r.AddDeletedObjectToQueue,
	}).Complete(r)
}
