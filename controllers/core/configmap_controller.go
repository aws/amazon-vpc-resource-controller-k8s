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

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	cooldown "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// ConfigMapReconciler reconciles a ConfigMap object
type ConfigMapReconciler struct {
	client.Client
	Log                               logr.Logger
	Scheme                            *runtime.Scheme
	NodeManager                       manager.Manager
	K8sAPI                            k8s.K8sWrapper
	Condition                         condition.Conditions
	curWinIPAMEnabledCond             bool
	curWinPrefixDelegationEnabledCond bool
	curWinPDWarmIPTarget              int
	curWinPDMinIPTarget               int
	curWinPDWarmPrefixTarget          int
	Context                           context.Context
}

//+kubebuilder:rbac:groups=core,resources=configmaps,namespace=kube-system,resourceNames=amazon-vpc-cni,verbs=get;list;watch

// Reconcile handles configmap create/update/delete events by invoking NodeManager
// to update the status of the nodes as per the enable-windows-ipam flag value.

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("configmap", req.NamespacedName)

	// only update nodes on amazon-vpc-cni updates, return here for other updates
	if req.Name != config.VpcCniConfigMapName || req.Namespace != config.KubeSystemNamespace {
		return ctrl.Result{}, nil
	}
	configmap := &corev1.ConfigMap{}
	if err := r.Client.Get(ctx, req.NamespacedName, configmap); err != nil {
		if errors.IsNotFound(err) {
			// If the configMap is deleted, de-register all the nodes
			logger.Info("amazon-vpc-cni configMap is deleted")
		} else {
			// Error reading the object
			logger.Error(err, "Failed to get configMap")
			return ctrl.Result{}, err
		}
	}

	// Check if branch ENI cooldown period is updated
	curCoolDownPeriod := cooldown.GetCoolDown().GetCoolDownPeriod()
	if newCoolDownPeriod, err := cooldown.GetVpcCniConfigMapCoolDownPeriodOrDefault(r.K8sAPI, r.Log); err == nil {
		if curCoolDownPeriod != newCoolDownPeriod {
			r.Log.Info("Branch ENI cool down period has been updated", "newCoolDownPeriod", newCoolDownPeriod, "OldCoolDownPeriod", curCoolDownPeriod)
			cooldown.GetCoolDown().SetCoolDownPeriod(newCoolDownPeriod)
			utils.SendBroadcastNodeEvent(
				r.K8sAPI,
				utils.BranchENICoolDownUpdateReason,
				fmt.Sprintf("Branch ENI cool down period has been updated to %s", cooldown.GetCoolDown().GetCoolDownPeriod()),
				v1.EventTypeNormal,
				r.Log,
			)
		}
	} else {
		r.Log.Error(err, "failed to retrieve branch ENI cool down period from amazon-vpc-cni configmap, will retain the current cooldown period", "cool down period", curCoolDownPeriod)
	}

	// Check if the Windows IPAM flag has changed
	newWinIPAMEnabledCond := r.Condition.IsWindowsIPAMEnabled()

	var isIPAMFlagUpdated bool
	if r.curWinIPAMEnabledCond != newWinIPAMEnabledCond {
		r.curWinIPAMEnabledCond = newWinIPAMEnabledCond
		logger.Info("updated configmap", config.EnableWindowsIPAMKey, r.curWinIPAMEnabledCond)

		isIPAMFlagUpdated = true
	}

	// Check if the prefix delegation flag has changed
	newWinPrefixDelegationEnabledCond := r.Condition.IsWindowsPrefixDelegationEnabled()

	var isPrefixFlagUpdated bool
	if r.curWinPrefixDelegationEnabledCond != newWinPrefixDelegationEnabledCond {
		r.curWinPrefixDelegationEnabledCond = newWinPrefixDelegationEnabledCond
		logger.Info("updated configmap", config.EnableWindowsPrefixDelegationKey, r.curWinPrefixDelegationEnabledCond)

		isPrefixFlagUpdated = true
	}

	// Check if configurations for Windows prefix delegation have changed
	var isPDConfigUpdated bool
	warmIPTarget, minIPTarget, warmPrefixTarget := config.ParseWinPDTargets(r.Log, configmap)
	if r.curWinPDWarmIPTarget != warmIPTarget || r.curWinPDMinIPTarget != minIPTarget || r.curWinPDWarmPrefixTarget != warmPrefixTarget {
		r.curWinPDWarmIPTarget = warmIPTarget
		r.curWinPDMinIPTarget = minIPTarget
		r.curWinPDWarmPrefixTarget = warmPrefixTarget
		logger.Info("updated PD configs from configmap", config.WarmIPTarget, r.curWinPDWarmIPTarget,
			config.MinimumIPTarget, r.curWinPDMinIPTarget, config.WarmPrefixTarget, r.curWinPDWarmPrefixTarget)

		isPDConfigUpdated = true
	}

	// Flag is updated, update all nodes
	if isIPAMFlagUpdated || isPrefixFlagUpdated || isPDConfigUpdated {
		err := UpdateNodesOnConfigMapChanges(r.K8sAPI, r.NodeManager)
		if err != nil {
			// Error in updating nodes
			logger.Error(err, "Failed to update nodes on configmap changes")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager, healthzHandler *rcHealthz.HealthzHandler) error {
	// add health check on subpath for CM controller
	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{"health-cm-controller": r.check()},
	)

	// Explicitly set MaxConcurrentReconciles to 1 to ensure concurrent reconciliation NOT supported for config map controller.
	// Don't change to more than 1 unless the struct is guarded against concurrency issues.
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}

func UpdateNodesOnConfigMapChanges(k8sAPI k8s.K8sWrapper, nodeManager manager.Manager) error {
	nodeList, err := k8sAPI.ListNodes()
	if err != nil {
		return err
	}
	var errList []error
	for _, node := range nodeList.Items {
		_, found := nodeManager.GetNode(node.Name)
		if found {
			err = nodeManager.UpdateNode(node.Name)
			if err != nil {
				errList = append(errList, err)
			}
		}
	}
	if len(errList) > 0 {
		return fmt.Errorf("failed to update one or more nodes %v", errList)
	}
	return nil
}

func (r *ConfigMapReconciler) check() healthz.Checker {
	r.Log.Info("ConfigMap controller's healthz subpath was added")
	// We can revisit this to use PingWithTimeout() instead if we have concerns on this controller.
	return rcHealthz.SimplePing("configmap controller", r.Log)
}
