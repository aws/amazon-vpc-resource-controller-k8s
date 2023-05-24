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
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_manager "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	request = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      mockNodeName,
			Namespace: config.KubeDefaultNamespace,
		},
	}
	mockNodeName     = "node-name"
	reconcileRequest = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: mockNodeName,
		},
	}
)

func TestCNINodeReconciler_Reconcile_UpdateCNINode_TrunkEnabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockManager := mock_manager.NewMockManager(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)

	cniNode := &v1alpha1.CNINode{
		ObjectMeta: v1.ObjectMeta{
			Name:      mockNodeName,
			Namespace: config.KubeDefaultNamespace,
		},

		Spec: v1alpha1.CNINodeSpec{
			Features: []v1alpha1.Feature{
				{
					Name: v1alpha1.SecurityGroupsForPods,
				},
			},
		},
	}
	mockNode := node.NewManagedNode(zap.New(), mockNodeName, "i-12345", "Linux", mockK8sWrapper, nil)
	k8sNode := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{
			Name: mockNodeName,
		},
	}

	cniNodeController := CNINodeController{
		NodeManager: mockManager,
		K8sAPI:      mockK8sWrapper,
		Context:     context.TODO(),
		Log:         zap.New(),
	}

	mockK8sWrapper.EXPECT().GetCNINode(
		types.NamespacedName{Name: reconcileRequest.Name, Namespace: config.KubeDefaultNamespace},
	).Return(cniNode, nil).Times(1)
	mockK8sWrapper.EXPECT().GetNode(mockNodeName).Return(k8sNode, nil).Times(1)
	mockManager.EXPECT().GetNode(mockNodeName).Return(mockNode, true).Times(1)
	mockManager.EXPECT().UpdateNode(mockNodeName).Return(nil).Times(1)

	rst, err := cniNodeController.Reconcile(context.TODO(), request)
	assert.NoError(t, err)
	assert.False(t, rst.Requeue)
}

func TestCNINodeReconciler_Reconcile_UpdateCNINode_TrunkDisabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockManager := mock_manager.NewMockManager(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)

	cniNode := &v1alpha1.CNINode{
		ObjectMeta: v1.ObjectMeta{
			Name:      mockNodeName,
			Namespace: config.KubeDefaultNamespace,
		},

		Spec: v1alpha1.CNINodeSpec{},
	}
	mockNode := node.NewManagedNode(zap.New(), mockNodeName, "i-12345", "Linux", mockK8sWrapper, nil)
	k8sNode := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{
			Name: mockNodeName,
		},
	}

	cniNodeController := CNINodeController{
		NodeManager: mockManager,
		K8sAPI:      mockK8sWrapper,
		Context:     context.TODO(),
		Log:         zap.New(),
	}

	mockK8sWrapper.EXPECT().GetCNINode(
		types.NamespacedName{Name: reconcileRequest.Name, Namespace: config.KubeDefaultNamespace},
	).Return(cniNode, nil).Times(1)
	mockK8sWrapper.EXPECT().GetNode(mockNodeName).Return(k8sNode, nil).Times(0)
	mockManager.EXPECT().GetNode(mockNodeName).Return(mockNode, true).Times(0)
	mockManager.EXPECT().UpdateNode(mockNodeName).Return(nil).Times(0)

	rst, err := cniNodeController.Reconcile(context.TODO(), request)
	assert.NoError(t, err)
	assert.False(t, rst.Requeue)
}
