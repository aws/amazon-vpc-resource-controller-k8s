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

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const MaxConcurrentReconciles = 3

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	client.Client
	Log     logr.Logger
	Scheme  *runtime.Scheme
	Manager node.Manager
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get;patch

func (r *NodeReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	node := &corev1.Node{}

	logger := r.Log.WithValues("node", req.NamespacedName)

	if err := r.Client.Get(ctx, req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			err := r.Manager.DeleteNode(req.Name)
			if err != nil {
				logger.Error(err, "failed to delete node from manager")
			}
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	err := r.Manager.AddOrUpdateNode(node)
	if err != nil {
		logger.Error(err, "failed to add node")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: MaxConcurrentReconciles}).
		Complete(r)
}
