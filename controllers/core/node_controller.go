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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// MaxNodeConcurrentReconciles is the number of go routines that can invoke
// Reconcile in parallel. Since Node Reconciler, performs local operation
// on cache only a single go routine should be sufficient. Using more than
// one routines to help high rate churn and larger nodes groups restarting
// when the controller has to be restarted for various reasons.
const (
	NodeTerminationFinalizer  = "networking.k8s.aws/resource-cleanup"
	computeTypeLabelKey       = "eks.amazonaws.com/compute-type"
	autoComputeTypeLabelValue = "auto"
)

// NodeReconciler reconciles a Node object
type NodeReconciler struct {
	client.Client
	K8sAPI     k8s.K8sWrapper
	Log        logr.Logger
	Scheme     *runtime.Scheme
	Manager    manager.Manager
	Conditions condition.Conditions
	Context    context.Context
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
		r.Log.Info("waiting for pod datastore to sync")
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	node := &corev1.Node{}

	logger := r.Log.WithValues("node", req.NamespacedName)

	if nodeErr := r.Client.Get(ctx, req.NamespacedName, node); nodeErr != nil {
		if errors.IsNotFound(nodeErr) {
			// clean up local cached nodes
			_, found := r.Manager.GetNode(req.Name)
			if found {
				cacheErr := r.Manager.DeleteNode(req.Name)
				if cacheErr != nil {
					// The request is not retryable so not returning the error
					logger.Error(cacheErr, "failed to delete node from manager")
					return ctrl.Result{}, nil
				}
				logger.V(1).Info("deleted the node from manager")
			}
		}
		return ctrl.Result{}, client.IgnoreNotFound(nodeErr)
	}
	computeKey, ok := node.Labels[computeTypeLabelKey]
	if ok && computeKey == autoComputeTypeLabelValue {
		logger.Info("node is auto compute type, skipping")
		return ctrl.Result{}, nil
	}
	var err error

	_, found := r.Manager.GetNode(req.Name)
	if found {
		logger.V(1).Info("updating node")
		err = r.Manager.UpdateNode(req.Name)

		// ReconcileNode actually run a branch ENI leaking check from an independent goroutine on added nodes.
		r.Manager.CheckNodeForLeakedENIs(req.Name)
	} else {
		logger.Info("adding node")
		err = r.Manager.AddNode(req.Name)
	}

	return ctrl.Result{}, err
}

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager, maxConcurrentReconciles int, healthzHandler *rcHealthz.HealthzHandler) error {
	// add health check on subpath for node controller
	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{"health-node-controller": r.Check()},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles}).
		Owns(&v1alpha1.CNINode{}).
		Complete(r)
}

func (r *NodeReconciler) Check() healthz.Checker {
	r.Log.Info("Node controller's healthz subpath was added")
	return rcHealthz.SimplePing("node-reconciler", r.Log)
}
