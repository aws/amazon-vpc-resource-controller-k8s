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
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/controllers/custom"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s/pod"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/resource"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
)

// +kubebuilder:rbac:groups="",resources=events,verbs=create;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;patch;watch

type PodReconciler struct {
	Log logr.Logger
	// ResourceManager provides the handlers for creation/deletion of
	// resources supported by vpc-resource-controller
	ResourceManager resource.ResourceManager
	// Manager manages all the nodes on the cluster
	NodeManager manager.Manager
	// DataStore is the cache with memory optimized Pod Objects
	DataStore cache.Indexer
	// DeletedObjDataStore stores the objects deleted from etcd, reconciler
	// must remove the object after accessing it
	DeletedObjDataStore cache.Indexer
	// DataStoreSynced when set to true indicates the data store is synced
	// successfully
	DataStoreSynced *bool
}

var PodRequeueRequest = ctrl.Result{Requeue: true, RequeueAfter: time.Second}

// Reconcile handles create/update/delete event by delegating the request to the  handler
// if the resource is supported by the controller.
func (r *PodReconciler) Reconcile(request ctrl.Request) (ctrl.Result, error) {
	var isDeleteEvent bool
	var hasPodCompleted bool

	var pod *v1.Pod

	logger := r.Log.WithValues("namespaced name", request.NamespacedName.String())
	obj, exists, err := r.DataStore.GetByKey(request.NamespacedName.String())
	if err != nil {
		logger.Error(err, "failed to retrieve the pod object")
		return ctrl.Result{}, nil
	}
	if !exists {
		logger.V(1).Info("pod doesn't exists in the active pod object's cache")

		// Since the Pod doesn't exist in cache anymore, it must be a delete event. Access
		// the event and remove it from deleted object data store
		obj, exists, err = r.DeletedObjDataStore.GetByKey(request.NamespacedName.String())
		if err != nil {
			logger.Error(err, "failed to retrieve the pod object from deleted object data store")
			return ctrl.Result{}, nil
		}
		if !exists {
			logger.Error(fmt.Errorf("object not found"), "failed to find object in delete data store")
			return ctrl.Result{}, nil
		}
		err = r.DeletedObjDataStore.Delete(obj)
		if err != nil {
			// Log the error, but still process the event
			logger.Error(err, "failed to remove object from data store",
				"size", len(r.DeletedObjDataStore.List()))
		}
		isDeleteEvent = true
	}
	// convert to pod object
	pod = obj.(*v1.Pod)

	// If Pod is Completed, the networking for the Pod should be removed
	// given the container will not be restarted again
	hasPodCompleted = pod.Status.Phase == v1.PodSucceeded ||
		pod.Status.Phase == v1.PodFailed

	logger = r.Log.WithValues("UID", pod.UID, "pod", request.NamespacedName,
		"node", pod.Spec.NodeName)

	// If the Pod doesn't have a Node assigned, ignore the request instead of querying the
	// node manager
	if pod.Spec.NodeName == "" {
		return ctrl.Result{}, nil
	}

	// On Controller startup, the Pod event should be processed after the Pod's node
	// has initialized (or it will be stuck till the next re-sync period or Pod update).
	// Once the Pod has been initialized if it's managed then wait till the asynchronous
	// operation on the Node has been performed and node is ready to server requests.
	node, found := r.NodeManager.GetNode(pod.Spec.NodeName)
	if !found {
		logger.V(1).Info("pod's node is not yet initialized by the manager, will retry")
		return PodRequeueRequest, nil
	} else if !node.IsManaged() {
		logger.V(1).Info("pod's node is not managed, skipping pod event")
		return ctrl.Result{}, nil
	} else if !node.IsReady() {
		logger.Info("pod's node is not ready to handle request yet, will retry")
		return PodRequeueRequest, nil
	}
	// Get the aggregate level resource, vpc controller doesn't support allocating
	// container level resources
	aggregateResources := getAggregateResources(pod)

	// For each resource, if a handler can allocate/de-allocate a resource then delegate the
	// allocation/de-allocation task to the respective handler
	for resourceName, totalCount := range aggregateResources {
		resourceHandler, isSupported := r.ResourceManager.GetResourceHandler(resourceName)
		if !isSupported {
			continue
		}

		var err error
		var result ctrl.Result
		if isDeleteEvent || hasPodCompleted {
			result, err = resourceHandler.HandleDelete(pod)
		} else {
			result, err = resourceHandler.HandleCreate(int(totalCount), pod)
		}
		if err != nil || result.Requeue == true {
			return result, err
		}
		logger.V(1).Info("handled resource without error",
			"resource", resourceName, "is delete event", isDeleteEvent,
			"has pod completed", hasPodCompleted)
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

// SetupWithManager adds the custom Pod controller's runnable to the manager's
// list of runnable. After Manager acquire the lease the pod controller runnable
// will be started and the Pod events will be sent to Reconcile function
func (r *PodReconciler) SetupWithManager(manager ctrl.Manager,
	clientSet *kubernetes.Clientset, pageLimit int, syncPeriod time.Duration) error {

	// event chanel is used by the custom controller to send Pod events after optimizing
	// them. On the receiving end the Pod events will be processed by the High Level
	// controller which sends the Namespace/Name request to Reconcile
	sourceEventChan := make(chan event.GenericEvent)

	// Create the low level controller that optimizes Pod Object in cache
	err := custom.NewControllerManagedBy(manager).
		WithLogger(r.Log.WithName("custom pod controller")).
		UsingDataStore(r.DataStore).
		UsingDeleteDataStore(r.DeletedObjDataStore).
		WithClientSet(clientSet).
		UsingConverter(&pod.PodConverter{
			K8sResource:     "pods",
			K8sResourceType: &v1.Pod{},
		}).DataStoreSyncFlag(r.DataStoreSynced).
		Options(custom.Options{
			PageLimit:    pageLimit,
			ResyncPeriod: syncPeriod,
		}).Complete(sourceEventChan)
	if err != nil {
		return err
	}

	// Start the high level controller, low level controller will notify this controller
	// using a chanel
	podController, err := controller.New("pod-controller", manager,
		controller.Options{
			Reconciler:              r,
			MaxConcurrentReconciles: 2,
		})
	if err != nil {
		return err
	}

	return podController.Watch(&source.Channel{Source: sourceEventChan}, &handler.EnqueueRequestForObject{})
}
