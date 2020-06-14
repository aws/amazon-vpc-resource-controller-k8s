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

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PodReconciler reconciles a Pod object
type PodReconciler struct {
	client.Client
	Log      logr.Logger
	Scheme   *runtime.Scheme
	handlers []handler.Handler
}

// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods/status,verbs=get;update;patch

// Reconcile reconciles the VPC Resources for the pod. Resources allocations are delegated to the respective handlers
// based on the resource type
func (r *PodReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	logger := r.Log.WithValues("pod", req.NamespacedName)

	pod := &corev1.Pod{}
	if err := r.Client.Get(ctx, req.NamespacedName, pod); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// TODO: Check if node is managed. Pending CR

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
		for _, handler := range r.handlers {
			if handler.CanHandle(resourceName) {
				var err error
				if !pod.DeletionTimestamp.IsZero() {
					err = handler.HandleDelete(resourceName, pod)
				} else {
					// TODO: If resource is already allocated, don't send the request.
					err = handler.HandleCreate(resourceName, totalCount, pod)
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
