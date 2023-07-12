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

package crds

import (
	"context"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type CNINodeController struct {
	Log         logr.Logger
	NodeManager manager.Manager
	K8sAPI      k8s.K8sWrapper
}

const MaxNodeConcurrentReconciles = 3

func (c *CNINodeController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CNINode{}).WithOptions(
		controller.Options{
			MaxConcurrentReconciles: MaxNodeConcurrentReconciles,
		},
	).Complete(reconcile.Func(func(ctx context.Context, r reconcile.Request) (reconcile.Result, error) {
		// check local datastore if the node was cached/added
		if _, found := c.NodeManager.GetNode(r.Name); found {
			return ctrl.Result{}, c.NodeManager.UpdateNode(r.Name)
		} else {
			return ctrl.Result{}, c.NodeManager.AddNode(r.Name)
		}
	}))
}
