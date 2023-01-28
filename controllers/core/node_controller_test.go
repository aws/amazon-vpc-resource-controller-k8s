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

	bigcache "github.com/allegro/bigcache/v3"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_node "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	mock_manager "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	reconcileRequest = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: mockNodeName,
		},
	}
	mockNodeObj = &corev1.Node{
		ObjectMeta: v1.ObjectMeta{
			Name: mockNodeName,
		},
	}
)

type NodeMock struct {
	Conditions *mock_condition.MockConditions
	Manager    *mock_manager.MockManager
	MockNode   *mock_node.MockNode
	Reconciler NodeReconciler
}

func NewNodeMock(ctrl *gomock.Controller, mockObjects ...runtime.Object) NodeMock {
	mockManager := mock_manager.NewMockManager(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	client := fakeClient.NewFakeClientWithScheme(scheme, mockObjects...)
	testCache, _ := bigcache.NewBigCache(bigcache.DefaultConfig(testCacheExpiry))
	return NodeMock{
		Conditions: mockConditions,
		Manager:    mockManager,
		MockNode:   mockNode,
		Reconciler: NodeReconciler{
			Scheme:         scheme,
			Client:         client,
			Log:            zap.New(),
			Manager:        mockManager,
			Conditions:     mockConditions,
			NodeEventCache: testCache,
		},
	}
}

func TestNodeReconciler_Reconcile_AddNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl, mockNodeObj)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, false)
	mock.Manager.EXPECT().AddNode(mockNodeName).Return(nil)

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func TestNodeReconciler_Reconcile_UpdateNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl, mockNodeObj)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.Manager.EXPECT().UpdateNode(mockNodeName).Return(nil)

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func TestNodeReconciler_Reconcile_DeleteNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockNode.EXPECT().GetNodeInstanceID().Return("i-00000000000000001")
	mock.Manager.EXPECT().DeleteNode(mockNodeName).Return(nil)

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func TestNodeReconciler_Reconcile_DeleteNonExistentNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, false)
	mock.MockNode.EXPECT().GetNodeInstanceID().Return("i-00000000000000001")

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}
