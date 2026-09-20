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
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_node "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	mock_manager "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	controllermetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
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

func NewNodeMock(ctrl *gomock.Controller, mockObjects ...client.Object) NodeMock {
	mockManager := mock_manager.NewMockManager(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	client := fakeClient.NewClientBuilder().WithScheme(scheme).WithObjects(mockObjects...).Build()

	return NodeMock{
		Conditions: mockConditions,
		Manager:    mockManager,
		MockNode:   mockNode,
		Reconciler: NodeReconciler{
			Scheme:     scheme,
			Client:     client,
			Log:        zap.New(),
			Manager:    mockManager,
			Conditions: mockConditions,
		},
	}
}

func TestNodeReconciler_Reconcile_AddNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl, mockNodeObj)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, false).Times(1)
	mock.Manager.EXPECT().AddNode(mockNodeName).Return(nil)
	mock.Manager.EXPECT().CheckNodeForLeakedENIs(mockNodeName).Times(0)

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func TestNodeReconciler_Reconcile_UpdateNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	currentNode := mockNodeObj.DeepCopy()
	currentNode.Spec.ProviderID = "aws:///us-west-2c/i-current"
	mock := NewNodeMock(ctrl, currentNode)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true).Times(1)
	mock.MockNode.EXPECT().GetNodeInstanceID().Return("i-current")
	mock.Manager.EXPECT().UpdateNode(mockNodeName).Return(nil)
	mock.Manager.EXPECT().CheckNodeForLeakedENIs(mockNodeName).Times(1)

	mismatchBefore := prometheusCounterValue(t, "node_instance_id_mismatch_total")
	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
	assert.Equal(t, mismatchBefore, prometheusCounterValue(t, "node_instance_id_mismatch_total"))
}

func TestNodeReconciler_Reconcile_UpdateNode_InstanceIDMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	currentNode := mockNodeObj.DeepCopy()
	currentNode.Spec.ProviderID = "aws:///us-west-2c/i-current"
	mock := NewNodeMock(ctrl, currentNode)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true).Times(1)
	mock.MockNode.EXPECT().GetNodeInstanceID().Return("i-cached")
	mock.Manager.EXPECT().UpdateNode(mockNodeName).Return(nil)
	mock.Manager.EXPECT().CheckNodeForLeakedENIs(mockNodeName).Times(1)

	mismatchBefore := prometheusCounterValue(t, "node_instance_id_mismatch_total")
	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
	assert.Equal(t, mismatchBefore+1, prometheusCounterValue(t, "node_instance_id_mismatch_total"))
}

func TestNodeReconciler_Reconcile_DeleteNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
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

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func TestNodeReconciler_Reconcile_DeleteNonExistentUnmanagedNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, false)

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func TestNodeReconciler_Reconcile_DeleteNonExistentUnmanagedWithoutInstanceNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewNodeMock(ctrl)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, false)

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func TestNodeReconciler_Reconcile_AddNode_Internal_Server_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	internalServerError := errors.NewInternalError(fmt.Errorf("internal server error"))
	mock := NewNodeMock(ctrl, mockNodeObj)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().GetNode(mockNodeName).Return(nil, false).Times(1)
	mock.Manager.EXPECT().AddNode(mockNodeName).Return(internalServerError)
	mock.Manager.EXPECT().CheckNodeForLeakedENIs(mockNodeName).Times(0)
	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)

	assert.Error(t, err, "We return error on internal server error to make sure it gets requeued by controller-runtime.")
	assert.False(t, res.Requeue)
}

func TestNodeReconciler_Reconcile_SkipAutoComputeType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	autoComputeNode := &corev1.Node{
		ObjectMeta: v1.ObjectMeta{
			Name: mockNodeName,
			Labels: map[string]string{
				computeTypeLabelKey: autoComputeTypeLabelValue,
			},
		},
	}

	mock := NewNodeMock(ctrl, autoComputeNode)

	mock.Conditions.EXPECT().GetPodDataStoreSyncStatus().Return(true)
	mock.Manager.EXPECT().AddNode(gomock.Any()).Times(0)
	mock.Manager.EXPECT().UpdateNode(gomock.Any()).Times(0)
	mock.Manager.EXPECT().GetNode(gomock.Any()).Times(0)
	mock.Manager.EXPECT().CheckNodeForLeakedENIs(gomock.Any()).Times(0)

	res, err := mock.Reconciler.Reconcile(context.TODO(), reconcileRequest)
	assert.NoError(t, err)
	assert.Equal(t, res, reconcile.Result{})
}

func prometheusCounterValue(t *testing.T, name string) float64 {
	t.Helper()
	metricFamilies, err := controllermetrics.Registry.Gather()
	assert.NoError(t, err)
	for _, metricFamily := range metricFamilies {
		if metricFamily.GetName() == name && len(metricFamily.Metric) == 1 {
			return metricFamily.Metric[0].GetCounter().GetValue()
		}
	}
	return 0
}
