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
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/controllers/custom"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s/pod"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/resource"
	"github.com/google/uuid"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
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
	K8sAPI      k8s.K8sWrapper
	// DataStore is the cache with memory optimized Pod Objects
	DataStore cache.Indexer
	Condition condition.Conditions
}

var (
	PodRequeueRequest          = ctrl.Result{Requeue: true, RequeueAfter: time.Second}
	MaxPodConcurrentReconciles = 4
)

// Reconcile handles create/update/delete event by delegating the request to the  handler
// if the resource is supported by the controller.
func (r *PodReconciler) Reconcile(request custom.Request) (ctrl.Result, error) {
	var isDeleteEvent bool
	var hasPodCompleted bool

	var pod *v1.Pod

	if request.DeletedObject != nil {
		isDeleteEvent = true
		pod = request.DeletedObject.(*v1.Pod)
	} else {
		obj, exists, err := r.DataStore.GetByKey(request.NamespacedName.String())
		if err != nil {
			r.Log.Error(err, "failed to retrieve the pod object",
				"namespace name", request.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		if !exists {
			r.Log.V(1).Info("pod doesn't exists in the cache anymore",
				"namespace name", request.NamespacedName.String())
			return ctrl.Result{}, nil
		}
		// convert to pod object
		pod = obj.(*v1.Pod)
	}

	// If Pod is Completed, the networking for the Pod should be removed
	// given the container will not be restarted again
	hasPodCompleted = pod.Status.Phase == v1.PodSucceeded ||
		pod.Status.Phase == v1.PodFailed

	logger := r.Log.WithValues("UID", pod.UID, "pod", request.NamespacedName,
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
	node, foundInCache := r.NodeManager.GetNode(pod.Spec.NodeName)

	nodeDeletedInCluster := false
	if !isDeleteEvent && r.isNodeExistingInCluster(pod, logger) {
		if !foundInCache {
			logger.V(1).Info("pod's node is not yet initialized by the manager, will retry", "Requested", request.NamespacedName.String(), "Cached pod name", pod.ObjectMeta.Name, "Cached pod namespace", pod.ObjectMeta.Namespace)
			return PodRequeueRequest, nil
		} else if !node.IsManaged() {
			logger.V(1).Info("pod's node is not managed, skipping pod event", "Requested", request.NamespacedName.String(), "Cached pod name", pod.ObjectMeta.Name, "Cached pod namespace", pod.ObjectMeta.Namespace)
			return ctrl.Result{}, nil
		} else if !node.IsReady() {
			logger.V(1).Info("pod's node is not ready to handle request yet, will retry", "Requested", request.NamespacedName.String(), "Cached pod name", pod.ObjectMeta.Name, "Cached pod namespace", pod.ObjectMeta.Namespace)
			return PodRequeueRequest, nil
		}
	} else if foundInCache && !node.IsManaged() {
		return ctrl.Result{}, nil
	} else if !isDeleteEvent {
		nodeDeletedInCluster = true
	}

	// Get the aggregate level resource, vpc controller doesn't support allocating
	// container level resources
	aggregateResources := getAggregateResources(pod)
	logger.V(1).Info("Pod controller logs local variables", "isDeleteEvent", isDeleteEvent, "hasPodCompleted", hasPodCompleted, "nodeDeletedInCluster", nodeDeletedInCluster)
	// For each resource, if a handler can allocate/de-allocate a resource then delegate the
	// allocation/de-allocation task to the respective handler
	for resourceName, totalCount := range aggregateResources {
		// Pod annotation ResourceNameIPAddress has two resource managers: secondary IP and prefix IP;
		// backend needs to distinguish which resource provider and handler should be used here accordingly
		if resourceName == config.ResourceNameIPAddress {
			resourceName = r.updateResourceName(isDeleteEvent || hasPodCompleted || nodeDeletedInCluster, pod, node)
		}

		resourceHandler, isSupported := r.ResourceManager.GetResourceHandler(resourceName)
		if !isSupported {
			continue
		}

		var err error
		var result ctrl.Result
		if isDeleteEvent || hasPodCompleted || nodeDeletedInCluster {
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

func (r *PodReconciler) isNodeExistingInCluster(pod *v1.Pod, logger logr.Logger) bool {
	if _, err := r.K8sAPI.GetNode(pod.Spec.NodeName); err != nil {
		logger.V(1).Info("The requested pod's node has been deleted from the cluster", "PodName", pod.ObjectMeta.Name, "PodNamespace", pod.ObjectMeta.Namespace, "NodeName", pod.Spec.NodeName)
		return false
	}
	return true
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
func (r *PodReconciler) SetupWithManager(ctx context.Context, manager ctrl.Manager,
	clientSet *kubernetes.Clientset, pageLimit int, syncPeriod time.Duration, healthzHandler *rcHealthz.HealthzHandler) error {
	r.Log.Info("The pod controller is using MaxConcurrentReconciles", "Routines", MaxPodConcurrentReconciles)

	customChecker, err := custom.NewControllerManagedBy(ctx, manager).
		WithLogger(r.Log.WithName("custom pod controller")).
		UsingDataStore(r.DataStore).
		WithClientSet(clientSet).
		UsingConverter(&pod.PodConverter{
			K8sResource:     "pods",
			K8sResourceType: &v1.Pod{},
		}).Options(custom.Options{
		PageLimit:               pageLimit,
		ResyncPeriod:            syncPeriod,
		MaxConcurrentReconciles: MaxPodConcurrentReconciles,
	}).UsingConditions(r.Condition).Complete(r)

	// add health check on subpath for pod and pod customized controllers
	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{
			"health-pod-controller":        r.check(),
			"health-custom-pod-controller": customChecker,
		},
	)

	return err
}

func (r *PodReconciler) check() healthz.Checker {
	r.Log.Info("Pod controller's healthz subpath was added")
	// more meaningful ping
	return func(req *http.Request) error {
		err := rcHealthz.PingWithTimeout(func(c chan<- error) {
			pingRequest := &custom.Request{
				NamespacedName: types.NamespacedName{
					Namespace: v1.NamespaceDefault,
					Name:      uuid.New().String(),
				},
				DeletedObject: nil,
			}
			// calling reconcile will test pod cache
			_, rErr := r.Reconcile(*pingRequest)
			r.Log.V(1).Info("***** pod controller healthz endpoint tested reconcile *****")
			c <- rErr
		}, r.Log)

		return err
	}
}

// updateResourceName updates resource name according to pod event and which IP allocation mode is enabled
func (r *PodReconciler) updateResourceName(isDeletionEvent bool, pod *v1.Pod, node node.Node) string {
	resourceName := config.ResourceNameIPAddress

	// Pod deletion must use the handler that assigned the IP resource regardless which IP allocation mode is active
	if isDeletionEvent {
		// Check prefix provider to see if it's a prefix deconstructed IP
		prefixProvider, found := r.ResourceManager.GetResourceProvider(config.ResourceNameIPAddressFromPrefix)
		if !found {
			// If prefix provider not found, log the error and continue to use the secondary IP provider
			r.Log.Error(fmt.Errorf("resource provider was not found"), "failed to find resource provider",
				"resource", resourceName)
			return resourceName
		}
		resourcePool, ok := prefixProvider.GetPool(pod.Spec.NodeName)
		// If IP is managed by prefix IP pool, update resource name so that prefix IP handler will be used
		if ok {
			if _, ownsResource := resourcePool.GetAssignedResource(string(pod.UID)); ownsResource {
				resourceName = config.ResourceNameIPAddressFromPrefix
			}
		}
	} else {
		// Pod creation should use the currently active resource handler. But if pod is scheduled on a non-nitro instance,
		// will skip updating the resource name and continue to use secondary IP instead.
		if r.Condition.IsWindowsPrefixDelegationEnabled() && node.IsNitroInstance() {
			// If prefix delegation is enabled, update resource name so that prefix IP handler will be used
			resourceName = config.ResourceNameIPAddressFromPrefix
		}
	}
	return resourceName
}
