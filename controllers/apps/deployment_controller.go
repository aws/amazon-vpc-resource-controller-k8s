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

package apps

import (
	"context"

	controllers "github.com/aws/amazon-vpc-resource-controller-k8s/controllers/core"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"

	"github.com/go-logr/logr"
	appV1 "k8s.io/api/apps/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

type DeploymentReconciler struct {
	Log                      logr.Logger
	NodeManager              manager.Manager
	K8sAPI                   k8s.K8sWrapper
	Condition                condition.Conditions
	wasOldControllerDeployed bool
}

func (r *DeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var isOldControllerDeployed bool
	// Only process old controller deployment events
	if req.Name != config.OldVPCControllerDeploymentName ||
		req.Namespace != config.KubeSystemNamespace {
		return ctrl.Result{}, nil
	}

	isOldControllerDeployed = r.Condition.IsOldVPCControllerDeploymentPresent()

	// State didn't change, no ops
	if isOldControllerDeployed == r.wasOldControllerDeployed {
		return ctrl.Result{}, nil
	}

	r.wasOldControllerDeployed = isOldControllerDeployed

	r.Log.Info("condition changed", "was deployment present before",
		r.wasOldControllerDeployed, "is deployment present now", isOldControllerDeployed)

	err := controllers.UpdateNodesOnConfigMapChanges(r.K8sAPI, r.NodeManager)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *DeploymentReconciler) SetupWithManager(mgr ctrl.Manager, healthzHandler *rcHealthz.HealthzHandler) error {
	// add health check on subpath for deployment controller
	// TODO: this is a simple controller and unlikely hit blocking issue but we can revisit this after subpaths are released for a while
	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{"health-deploy-controller": rcHealthz.SimplePing("deployment controller", r.Log)},
	)

	return ctrl.NewControllerManagedBy(mgr).
		For(&appV1.Deployment{}).
		Complete(r)
}
