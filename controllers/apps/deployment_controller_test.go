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
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	mockNodeList = &v1.NodeList{
		Items: []v1.Node{
			{
				ObjectMeta: metaV1.ObjectMeta{
					Name: "node",
				},
			},
		},
	}
	request = controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Namespace: config.KubeSystemNamespace,
			Name:      config.OldVPCControllerDeploymentName,
		},
	}
	ctx = context.TODO()
)

type Mock struct {
	Conditions *mock_condition.MockConditions
	Manager    *mock_manager.MockManager
	K8sAPI     *mock_k8s.MockK8sWrapper
	Node       *mock_node.MockNode
	Reconciler DeploymentReconciler
}

func NewDeploymentMock(ctrl *gomock.Controller) Mock {
	mockManager := mock_manager.NewMockManager(ctrl)
	mockCondition := mock_condition.NewMockConditions(ctrl)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)
	return Mock{
		Reconciler: DeploymentReconciler{
			Log:         zap.New(),
			NodeManager: mockManager,
			Condition:   mockCondition,
			K8sAPI:      mockK8sAPI,
		},
		Conditions: mockCondition,
		Manager:    mockManager,
		K8sAPI:     mockK8sAPI,
		Node:       mockNode,
	}
}

func TestDeploymentReconciler_Reconcile_WrongObject(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewDeploymentMock(ctrl)
	_, err := mock.Reconciler.Reconcile(ctx, controllerruntime.Request{
		NamespacedName: types.NamespacedName{
			Name: config.OldVPCControllerDeploymentName,
		},
	})
	assert.NoError(t, err)
}

func TestDeploymentReconciler_Reconcile_StateUnchanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewDeploymentMock(ctrl)

	mock.Conditions.EXPECT().IsOldVPCControllerDeploymentPresent().Return(false)

	_, err := mock.Reconciler.Reconcile(ctx, request)
	assert.NoError(t, err)
}

func TestDeploymentReconciler_Reconcile_StateChanged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewDeploymentMock(ctrl)

	mock.Conditions.EXPECT().IsOldVPCControllerDeploymentPresent().Return(true)
	mock.K8sAPI.EXPECT().ListNodes().Return(mockNodeList, nil)
	mock.Manager.EXPECT().GetNode("node").Return(mock.Node, true)
	mock.Manager.EXPECT().UpdateNode("node").Return(nil)

	_, err := mock.Reconciler.Reconcile(ctx, request)
	assert.NoError(t, err)
}
