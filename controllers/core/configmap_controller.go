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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	// Check if the flag value has changed
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

	// Flag is updated, update all nodes
	if isIPAMFlagUpdated || isPrefixFlagUpdated {
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
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
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
