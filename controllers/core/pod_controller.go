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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	Handlers []handler.Handler
	Manager  node.Manager
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch

// Reconcile reconciles the VPC Resources for the pod. Resources allocations are delegated to the respective handlers
// based on the resource type
func (r *PodReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()

	pod := &corev1.Pod{}
	if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
		if errors.IsNotFound(err) {
			// Pod is deleted and no longer available in cache, notify all handlers to clean up
			for _, handler := range r.Handlers {
				err := handler.HandleDelete(req.Namespace, req.Name)
				if err != nil {
					r.Log.Error(err, "failed to clean up pod resources")
				}
			}
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	logger := r.Log.WithValues("pod", req.NamespacedName, "node", pod.Spec.NodeName)

	node, managed := r.Manager.GetNode(pod.Spec.NodeName)
	if !managed {
		r.Log.V(1).Info("pod's node is not managed, skipping pod event")
		return ctrl.Result{}, nil
	} else if !node.IsReady() {
		r.Log.Info("pod's node is not ready to handle request yet, will retry")
		return ctrl.Result{Requeue: true, RequeueAfter: time.Millisecond * 500}, nil
	}

	// Compute the aggregate resources across all containers for each resource type
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

	// For each resource, if a handler can allocate/de-allocate a resource then delegate the allocation/de-allocation
	// task to the respective handler
	for resourceName, totalCount := range aggregateResources {
		for _, handler := range r.Handlers {
			if handler.CanHandle(resourceName) {
				var err error
				if !pod.DeletionTimestamp.IsZero() {
					err = handler.HandleDeleting(resourceName, pod)
				} else {
					err = handler.HandleCreate(resourceName, int(totalCount), pod)
				}
				if err != nil {
					logger.Error(err, "error handling resources")
					return ctrl.Result{}, err
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
