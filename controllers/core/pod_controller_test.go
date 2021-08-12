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
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/controllers/custom"
	mockhandler "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/handler"
	mock_node "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s/pod"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	mockPodName                 = "pod-name"
	mockNodeName                = "node-name"
	mockPodNS                   = "pod-namespace"
	mockResourceName            = "vpc.amazonaws.com/pod-eni"
	mockUnsupportedResourceName = "cpu"

	n = types.NamespacedName{
		Namespace: mockPodNS,
		Name:      mockPodName,
	}
	mockReq = custom.Request{NamespacedName: n}

	mockPod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      mockPodName,
			Namespace: mockPodNS,
		},
		Spec: v1.PodSpec{
			NodeName: mockNodeName,
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceName(mockResourceName): *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
				},
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceName(mockResourceName): *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
				},
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceName(mockUnsupportedResourceName): *resource.NewQuantity(10, resource.DecimalExponent),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{},
	}
)

// getPodReconcilerAndMockHandler returns PodReconciler and mockHandler used to setup the pod reconciler
func getPodReconcilerAndMocks(ctrl *gomock.Controller, mockPod *v1.Pod) (*PodReconciler, *mockhandler.MockHandler,
	*mock_node.MockManager, *mock_node.MockNode, *mock_resource.MockResourceManager) {

	mockHandler := mockhandler.NewMockHandler(ctrl)
	mockNodeManager := mock_node.NewMockManager(ctrl)
	mockResourceManager := mock_resource.NewMockResourceManager(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)

	converter := pod.PodConverter{}
	mockIndexer := cache.NewIndexer(converter.Indexer, pod.NodeNameIndexer())
	mockIndexer.Add(mockPod)

	podReconciler := &PodReconciler{
		NodeManager:     mockNodeManager,
		Log:             zap.New(zap.UseDevMode(true)).WithName("on demand handler"),
		ResourceManager: mockResourceManager,
		DataStore:       mockIndexer,
	}
	return podReconciler, mockHandler, mockNodeManager, mockNode, mockResourceManager
}

// TestPodReconciler_Reconcile_Create test that the resource handler is invoked for supported resource type in case of
// a pod create/update event
func TestPodReconciler_Reconcile_Create(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	reconciler, mockHandler, mockManager, mockNode, mockResourceManager :=
		getPodReconcilerAndMocks(ctrl, mockPod)

	mockManager.EXPECT().GetNode(mockNodeName).Return(mockNode, true)
	mockNode.EXPECT().IsReady().Return(true)
	mockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mockHandler, true)
	mockHandler.EXPECT().HandleCreate(3, gomock.Any()).Return(reconcile.Result{}, nil)
	mockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	_, err := reconciler.Reconcile(mockReq)
	assert.NoError(t, err)
}

// TestPodReconciler_Reconcile_Delete test that the resource handler is invoked for supported resource type in case of
// a pod delete event
func TestPodReconciler_Reconcile_Delete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	reconciler, mockHandler, mockManager, mockNode, mockResourceManager :=
		getPodReconcilerAndMocks(ctrl, mockPod)

	mockManager.EXPECT().GetNode(mockNodeName).Return(mockNode, true)
	mockNode.EXPECT().IsReady().Return(true)

	mockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mockHandler, true)
	mockHandler.EXPECT().HandleDelete(mockPod).Return(reconcile.Result{}, nil)
	mockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	delReq := custom.Request{
		DeletedObject: mockPod,
	}
	_, err := reconciler.Reconcile(delReq)
	assert.NoError(t, err)
}

// TestPodReconciler_Reconcile_NonManaged tests that the request is ignore if the node is not managed
func TestPodReconciler_Reconcile_NonManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	reconciler, _, mockManager, _, _ := getPodReconcilerAndMocks(ctrl, mockPod)

	mockManager.EXPECT().GetNode(mockNodeName).Return(nil, false)

	_, err := reconciler.Reconcile(mockReq)
	assert.NoError(t, err)
}

// TestPodReconciler_Reconcile_NodeNotReady tests that the request is ignored when the node is not ready
func TestPodReconciler_Reconcile_NodeNotReady(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	reconciler, _, mockManager, mockNode, _ := getPodReconcilerAndMocks(ctrl, mockPod)

	mockManager.EXPECT().GetNode(mockNodeName).Return(mockNode, true)
	mockNode.EXPECT().IsReady().Return(false)

	_, err := reconciler.Reconcile(mockReq)
	assert.NoError(t, err)
}
