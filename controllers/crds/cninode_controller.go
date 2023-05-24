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

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type CNINodeController struct {
	Log         logr.Logger
	NodeManager manager.Manager
	Context     context.Context
	K8sAPI      k8s.K8sWrapper
}

func (s *CNINodeController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cniNode, err := s.K8sAPI.GetCNINode(types.NamespacedName{Name: req.Name, Namespace: req.Namespace})
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if len(cniNode.Spec.Features) == 0 {
		return ctrl.Result{}, nil
	}

	nodeName := cniNode.Name
	s.Log.Info("The CNINode has been updated", "CNINode", nodeName, "Features", cniNode.Spec.Features)

	if _, err := s.K8sAPI.GetNode(nodeName); err != nil {
		return ctrl.Result{}, err
	} else {
		// make sure we have the node in cache already
		if _, found := s.NodeManager.GetNode(nodeName); !found {
			s.Log.Info("CNINode controller could not find node from cache, will try again", "NodeName", nodeName)
			return ctrl.Result{Requeue: true}, nil
		} else {
			// add the node into working queue as UPDATE event
			s.Log.Info("CNINode controller found the node and sending the node update")
			if err = s.NodeManager.UpdateNode(nodeName); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (s *CNINodeController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CNINode{}).Complete(s)
}
