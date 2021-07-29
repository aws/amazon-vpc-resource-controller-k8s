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

package controllers

import (
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/controllers/custom"
	reqHandler "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

type PodReconciler struct {
	Log logr.Logger
	// Handlers is the resource handler for handling supported request on specified in the
	// container requests.
	Handlers []reqHandler.Handler
	// Manager manages all the nodes on the cluster
	Manager node.Manager
	// PodController to get the cached data store
	PodController custom.Controller
}

// +kubebuilder:rbac:groups="",resources=events,verbs=create;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;patch;watch

// CreateUpdateReconciler to handle create and update events on the pod objects
type CreateUpdateReconciler struct {
	PodReconciler            *PodReconciler
	CreateUpdateEventChannel chan event.GenericEvent
}

// DeleteReconciler to handle the delete events on pod objects
type DeleteReconciler struct {
	PodReconciler      *PodReconciler
	DeleteEventChannel chan event.GenericEvent
}

// Reconcile handles create and update events
func (r *CreateUpdateReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	return r.PodReconciler.ProcessEvent(req, false)
}

// Reconcile handles delete events
func (d *DeleteReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	return d.PodReconciler.ProcessEvent(req, true)
}

// ProcessEvent handles create/update event by querying the handler if it can process
// container request and pass the pod to the handler if it is capable of handling the request.
// The Delete event passes the pod object to supported handlers to clean up the resources and
// remove the entry from the data store.
func (r *PodReconciler) ProcessEvent(req ctrl.Request, isDeleteEvent bool) (ctrl.Result, error) {
	obj, exists, err := r.PodController.GetDataStore().GetByKey(req.NamespacedName.String())
	if err != nil {
		r.Log.Error(err, "failed to retrieve the pod object",
			"namespace name", req.NamespacedName.String())
		return ctrl.Result{}, nil
	}
	if !exists {
		r.Log.Info("pod doesn't exists in the cache anymore",
			"namespace name", req.NamespacedName.String())
		return ctrl.Result{}, nil
	}
	// convert to pod object
	pod := obj.(*v1.Pod)

	// If it's a delete event then after processing it should be removed from the data store
	if isDeleteEvent {
		defer r.PodController.GetDataStore().Delete(pod)
	}

	logger := r.Log.WithValues("UID", pod.UID, "pod", req.NamespacedName,
		"node", pod.Spec.NodeName)

	// Verify the node is managed by the controller and the node is ready to handle
	// incoming requests
	node, managed := r.Manager.GetNode(pod.Spec.NodeName)
	if !managed {
		logger.V(1).Info("pod's node is not managed, skipping pod event")
		return ctrl.Result{}, nil
	} else if !node.IsReady() {
		logger.Info("pod's node is not ready to handle request yet, will retry")
		return ctrl.Result{Requeue: true, RequeueAfter: time.Millisecond * 500}, nil
	}

	// Get the aggregate level resource, vpc controller doesn't support allocating
	// container level resources
	aggregateResources := getAggregateResources(pod)

	// For each resource, if a handler can allocate/de-allocate a resource then delegate the
	// allocation/de-allocation task to the respective handler
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
func getAggregateResources(pod *v1.Pod) map[string]int64 {
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

// Ideally we should create single controller, but given that the Reconcile method doesn't provide
// us the event type and since we want to capture the pod event after it has been removed from the
// etcd (we can't use deletion timestamp) we are creating two different controllers to process add/
// update and delete event separately.

// SetupWithManager sets controller to listen on the create events and update events
func (c *CreateUpdateReconciler) SetupWithManager(manager ctrl.Manager) error {
	// Create and update event controller
	createUpdateController, err := controller.New("pod-create", manager, controller.Options{Reconciler: c})
	if err != nil {
		return err
	}
	return createUpdateController.Watch(&source.Channel{Source: c.CreateUpdateEventChannel}, &handler.EnqueueRequestForObject{})
}

// SetupWithManager sets controller to listen on the delete events
func (d *DeleteReconciler) SetupWithManager(manager ctrl.Manager) error {
	// Delete event controller
	deleteController, err := controller.New("pod-delete", manager, controller.Options{Reconciler: d})
	if err != nil {
		return err
	}
	return deleteController.Watch(&source.Channel{Source: d.DeleteEventChannel}, &handler.EnqueueRequestForObject{})
}
