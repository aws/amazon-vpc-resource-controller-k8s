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

const MaxConcurrentReconciles = 1

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

func (r *NodeReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	r.Conditions.WaitTillPodDataStoreSynced()

	ctx := context.TODO()
	k8sNode := &corev1.Node{}
	var err error

	logger := r.Log.WithValues("node", req.NamespacedName)

	if err := r.Client.Get(ctx, req.NamespacedName, k8sNode); err != nil {
		if errors.IsNotFound(err) {
			err := r.Manager.DeleteNode(req.Name)
			if err != nil {
				logger.Error(err, "failed to delete node from manager")
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
		WithOptions(controller.Options{MaxConcurrentReconciles: MaxConcurrentReconciles}).
		Complete(r)
}
