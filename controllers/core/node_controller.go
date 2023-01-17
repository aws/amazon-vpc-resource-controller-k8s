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
	goErr "errors"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

// MaxNodeConcurrentReconciles is the number of go routines that can invoke
// Reconcile in parallel. Since Node Reconciler, performs local operation
// on cache only a single go routine should be sufficient. Using more than
// one routines to help high rate churn and larger nodes groups restarting
// when the controller has to be restarted for various reasons.
const MaxNodeConcurrentReconciles = 3

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	client.Client
	Log        logr.Logger
	Scheme     *runtime.Scheme
	Manager    manager.Manager
	Conditions condition.Conditions
}

// +kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=nodes/status,verbs=get;patch

// Reconcile Adds a new node by calling the Node Manager. A node can be added as a
// Managed Node in which case the controller can provide resources for Pod's scheduled
// on the node or it can be added as a un managed Node in which case controller doesn't
// do any operations on the Node or any Pods scheduled on the Node. A node can be toggled
// from Un-Managed to Managed and vice-versa in which case the Node Manager updates it's
// status accordingly
func (r *NodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if !r.Conditions.GetPodDataStoreSyncStatus() {
		// if pod cache is not ready, let's exponentially requeue the requests instead of letting routines wait
		return ctrl.Result{Requeue: true}, goErr.New("pod datastore hasn't been synced, node controller need wait to retry")
	}

	node := &corev1.Node{}
	var err error

	logger := r.Log.WithValues("node", req.NamespacedName)

	if err := r.Client.Get(ctx, req.NamespacedName, node); err != nil {
		if errors.IsNotFound(err) {
			_, found := r.Manager.GetNode(req.Name)
			if found {
				err := r.Manager.DeleteNode(req.Name)
				if err != nil {
					// The request is not retryable so not returning the error
					logger.Error(err, "failed to delete node from manager")
					return ctrl.Result{}, nil
				}
				logger.Info("deleted the node from manager")
			}
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	_, found := r.Manager.GetNode(req.Name)
	if found {
		logger.V(1).Info("updating node")
		err = r.Manager.UpdateNode(req.Name)
	} else {
		logger.Info("adding node")
		err = r.Manager.AddNode(req.Name)
	}

	return ctrl.Result{}, err
}

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: MaxNodeConcurrentReconciles}).
		Complete(r)
}
