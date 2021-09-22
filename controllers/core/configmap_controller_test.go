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
	"errors"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	mockConfigMap = &corev1.ConfigMap{
		TypeMeta:   metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{Name: config.VpcCniConfigMapName, Namespace: config.VpcCNIConfigMapNamespace},
		Data:       map[string]string{config.EnableWindowsIPAMKey: "true"},
	}
	mockConfigMapReq = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: config.VpcCNIConfigMapNamespace,
			Name:      config.VpcCniConfigMapName,
		},
	}
	v1Node = &corev1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: mockNodeName,
		},
	}
	nodeList = &corev1.NodeList{
		Items: append([]corev1.Node{}, *v1Node),
	}
	errMock = errors.New("Mock error")
)

type ConfigMapMock struct {
	MockNodeManager     *mock_manager.MockManager
	ConfigMapReconciler *ConfigMapReconciler
	MockNode            *mock_node.MockNode
	MockK8sAPI          *mock_k8s.MockK8sWrapper
	MockCondition       *mock_condition.MockConditions
	curWinIPAMCond      bool
}

func NewConfigMapMock(ctrl *gomock.Controller, mockObjects ...runtime.Object) ConfigMapMock {
	mockNodeManager := mock_manager.NewMockManager(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)
	mockCondition := mock_condition.NewMockConditions(ctrl)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	client := fakeClient.NewFakeClientWithScheme(scheme, mockObjects...)

	return ConfigMapMock{
		MockNodeManager: mockNodeManager,
		ConfigMapReconciler: &ConfigMapReconciler{
			Client:      client,
			Log:         zap.New(),
			NodeManager: mockNodeManager,
			K8sAPI:      mockK8sWrapper,
			Condition:   mockCondition,
		},
		MockNode:       mockNode,
		MockK8sAPI:     mockK8sWrapper,
		MockCondition:  mockCondition,
		curWinIPAMCond: false,
	}
}

func Test_Reconcile_ConfigMap_Updated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewConfigMapMock(ctrl, mockConfigMap)
	mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
	mock.MockK8sAPI.EXPECT().ListNodes().Return(nodeList, nil)
	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockNodeManager.EXPECT().UpdateNode(mockNodeName).Return(nil)

	res, err := mock.ConfigMapReconciler.Reconcile(context.TODO(), mockConfigMapReq)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})

}

func Test_Reconcile_ConfigMap_NoData(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockConfigMap_WithNoData := mockConfigMap.DeepCopy()
	mockConfigMap_WithNoData.Data = map[string]string{}
	mock := NewConfigMapMock(ctrl, mockConfigMap_WithNoData)

	mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(false)
	mock.MockK8sAPI.EXPECT().ListNodes().Return(nodeList, nil)
	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockNodeManager.EXPECT().UpdateNode(mockNodeName).Return(nil)

	res, err := mock.ConfigMapReconciler.Reconcile(context.TODO(), mockConfigMapReq)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func Test_Reconcile_ConfigMap_Deleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewConfigMapMock(ctrl)
	mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(false)

	res, err := mock.ConfigMapReconciler.Reconcile(context.TODO(), mockConfigMapReq)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func Test_Reconcile_UpdateNode_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewConfigMapMock(ctrl, mockConfigMap)
	mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
	mock.MockK8sAPI.EXPECT().ListNodes().Return(nodeList, nil)
	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockNodeManager.EXPECT().UpdateNode(mockNodeName).Return(errMock)

	res, err := mock.ConfigMapReconciler.Reconcile(context.TODO(), mockConfigMapReq)
	assert.Error(t, err)
	assert.Equal(t, res, reconcile.Result{})

}
