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
	"errors"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/controllers/custom"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_handler "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/handler"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_node "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	mock_manager "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager"
	mock_pool "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/pool"
	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s/pod"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	controllerruntime "sigs.k8s.io/controller-runtime"
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
			Name:        mockPodName,
			Namespace:   mockPodNS,
			Annotations: map[string]string{config.ResourceNameIPAddress: "192.168.10.0/32"},
			UID:         "pod-id",
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

type Mock struct {
	MockNodeManager     *mock_manager.MockManager
	MockK8sAPI          *mock_k8s.MockK8sWrapper
	MockResourceManager *mock_resource.MockResourceManager
	MockNode            *mock_node.MockNode
	PodReconciler       *PodReconciler
	MockHandler         *mock_handler.MockHandler
	MockProvider        *mock_provider.MockResourceProvider
	MockCondition       *mock_condition.MockConditions
}

func NewMock(ctrl *gomock.Controller, mockPod *v1.Pod) Mock {
	mockHandler := mock_handler.NewMockHandler(ctrl)
	mockNodeManager := mock_manager.NewMockManager(ctrl)
	mockResourceManager := mock_resource.NewMockResourceManager(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	converter := pod.PodConverter{}
	mockIndexer := cache.NewIndexer(converter.Indexer, pod.NodeNameIndexer())
	mockIndexer.Add(mockPod)
	mockCondition := mock_condition.NewMockConditions(ctrl)

	return Mock{
		MockNodeManager:     mockNodeManager,
		MockK8sAPI:          mockK8sWrapper,
		MockResourceManager: mockResourceManager,
		MockNode:            mockNode,
		MockHandler:         mockHandler,
		MockCondition:       mockCondition,
		PodReconciler: &PodReconciler{
			Log:             zap.New(),
			ResourceManager: mockResourceManager,
			NodeManager:     mockNodeManager,
			K8sAPI:          mockK8sWrapper,
			DataStore:       mockIndexer,
			Condition:       mockCondition,
		},
	}
}

// TestPodReconciler_Reconcile_Create test that the resource handler is invoked for supported resource type in case of
// a pod create/update event
func TestPodReconciler_Reconcile_Create(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockK8sAPI.EXPECT().GetNode(mockNodeName).Return(nil, nil)
	mock.MockNode.EXPECT().IsManaged().Return(true)
	mock.MockNode.EXPECT().IsReady().Return(true)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mock.MockHandler, true)
	mock.MockHandler.EXPECT().HandleCreate(3, gomock.Any()).Return(reconcile.Result{}, nil)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	result, err := mock.PodReconciler.Reconcile(mockReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// TestPodReconciler_Reconcile_Delete test that the resource handler is invoked for supported resource type in case of
// a pod delete event
func TestPodReconciler_Reconcile_Delete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockNode.EXPECT().IsManaged().Return(true)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mock.MockHandler, true)
	mock.MockHandler.EXPECT().HandleDelete(mockPod).Return(reconcile.Result{}, nil)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	delReq := custom.Request{
		DeletedObject: mockPod,
	}

	result, err := mock.PodReconciler.Reconcile(delReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// TestPodReconciler_Reconcile_NonManaged tests that the request is ignore if the node is not managed
func TestPodReconciler_Reconcile_NonManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockK8sAPI.EXPECT().GetNode(mockNodeName).Return(nil, nil)
	mock.MockNode.EXPECT().IsManaged().Return(false)

	result, err := mock.PodReconciler.Reconcile(mockReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// TestPodReconciler_Reconcile_NoNodeAssigned tests that the request for a Pod with no Node assigned
// should be ignored
func TestPodReconciler_Reconcile_NoNodeAssigned(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	podWithoutNode := mockPod.DeepCopy()
	podWithoutNode.Spec.NodeName = ""

	mock := NewMock(ctrl, podWithoutNode)

	result, err := mock.PodReconciler.Reconcile(mockReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// TestPodReconciler_Reconcile_NodeNotReady tests that the request is ignored when the node is not ready
func TestPodReconciler_Reconcile_NodeNotReady(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockK8sAPI.EXPECT().GetNode(mockNodeName).Return(nil, nil)
	mock.MockNode.EXPECT().IsManaged().Return(true)
	mock.MockNode.EXPECT().IsReady().Return(false)

	result, err := mock.PodReconciler.Reconcile(mockReq)
	assert.Equal(t, result, PodRequeueRequest)
	assert.NoError(t, err)
}

func TestPodReconcile_Reconcile_NodeNotInitialized(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(nil, false)
	mock.MockK8sAPI.EXPECT().GetNode(mockNodeName).Return(nil, nil)

	result, err := mock.PodReconciler.Reconcile(mockReq)
	assert.Equal(t, result, PodRequeueRequest)
	assert.NoError(t, err)
}

func TestPodReconcile_Reconcile_NodeDeletedFromCache(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(nil, false)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mock.MockHandler, true)
	mock.MockHandler.EXPECT().HandleDelete(mockPod).Return(reconcile.Result{}, nil)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	delReq := custom.Request{
		DeletedObject: mockPod,
	}

	result, err := mock.PodReconciler.Reconcile(delReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// test not cached node which is also not existing in cluster
func TestPodReconcile_Reconcile_NodeDeletedFromCluster(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(nil, false)
	mock.MockK8sAPI.EXPECT().GetNode(mockNodeName).Return(nil, errors.New("Resource not found")).AnyTimes()
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mock.MockHandler, true)
	mock.MockHandler.EXPECT().HandleDelete(mockPod).Return(reconcile.Result{}, nil)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	result, err := mock.PodReconciler.Reconcile(mockReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// test node in cache and managed but deleted in cluster, which should be rare case and pod should be ignored from retry
func TestPodReconcile_Reconcile_ManagedNodeDeletedFromCluster(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockK8sAPI.EXPECT().GetNode(mockNodeName).Return(nil, errors.New("Resource not found")).AnyTimes()
	mock.MockNode.EXPECT().IsManaged().Return(true)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mock.MockHandler, true)
	mock.MockHandler.EXPECT().HandleDelete(mockPod).Return(reconcile.Result{}, nil)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	result, err := mock.PodReconciler.Reconcile(mockReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// test pod delete event and node is managed in cache but no longer existing in cluster, which should be a rare case and the pod should be ignored
func TestPodReconcile_Reconcile_PodDeletedManagedNodeDeletedFromCluster(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)

	mock.MockNodeManager.EXPECT().GetNode(mockNodeName).Return(mock.MockNode, true)
	mock.MockK8sAPI.EXPECT().GetNode(mockNodeName).Return(nil, errors.New("Resource not found")).AnyTimes()
	mock.MockNode.EXPECT().IsManaged().Return(true)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockResourceName).Return(mock.MockHandler, true)
	mock.MockHandler.EXPECT().HandleDelete(mockPod).Return(reconcile.Result{}, nil)
	mock.MockResourceManager.EXPECT().GetResourceHandler(mockUnsupportedResourceName).Return(nil, false)

	delReq := custom.Request{
		DeletedObject: mockPod,
	}

	result, err := mock.PodReconciler.Reconcile(delReq)
	assert.NoError(t, err)
	assert.Equal(t, result, controllerruntime.Result{})
}

// TestUpdateResourceName_IsDeleteEvent_PrefixIP tests for pod deletion events, prefix IP handler is used when ip is managed by prefix
// ip pool
func TestUpdateResourceName_IsDeleteEvent_PrefixIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)
	mockPool := mock_pool.NewMockPool(ctrl)
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	mock.MockResourceManager.EXPECT().GetResourceProvider(config.ResourceNameIPAddressFromPrefix).Return(mockProvider, true)
	mockProvider.EXPECT().GetPool(mockPod.Spec.NodeName).Return(mockPool, true)
	mockPool.EXPECT().GetAssignedResource(string(mockPod.UID)).Return("ip-1", true)
	resourceName := mock.PodReconciler.updateResourceName(true, mockPod, mock.MockNode)

	assert.Equal(t, config.ResourceNameIPAddressFromPrefix, resourceName)
}

// TestUpdateResourceName_IsDeleteEvent_SecondaryIP tests for pod deletion events, secondary ip handler is used when ip is managed
// by secondary ip pool even if PD is enabled for the cluster
func TestUpdateResourceName_IsDeleteEvent_SecondaryIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)
	mockPool := mock_pool.NewMockPool(ctrl)
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	mock.MockResourceManager.EXPECT().GetResourceProvider(config.ResourceNameIPAddressFromPrefix).Return(mockProvider, true)
	mockProvider.EXPECT().GetPool(mockPod.Spec.NodeName).Return(mockPool, true)
	mockPool.EXPECT().GetAssignedResource(string(mockPod.UID)).Return("", false)
	resourceName := mock.PodReconciler.updateResourceName(true, mockPod, mock.MockNode)

	// since resource ip is not managed by prefix ip pool, resource name remains unchanged
	assert.Equal(t, config.ResourceNameIPAddress, resourceName)
}

// TestUpdateResourceName_NonNitroInstance_SecondaryIP tests for pod creation events, non-nitro instances should use
// secondary ip handler
func TestUpdateResourceName_NonNitroInstance_SecondaryIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	mock.MockResourceManager.EXPECT().GetResourceProvider(config.ResourceNameIPAddressFromPrefix).Return(mockProvider, true)
	mock.MockCondition.EXPECT().IsWindowsPrefixDelegationEnabled().Return(true)
	mock.MockNode.EXPECT().IsNitroInstance().Return(false)
	resourceName := mock.PodReconciler.updateResourceName(false, mockPod, mock.MockNode)

	// since resource ip is not managed by prefix ip pool, resource name remains unchanged
	assert.Equal(t, config.ResourceNameIPAddress, resourceName)
}

// TestUpdateResourceName_NitroInstance_PrefixIP tests for pod creation events, supported instances (nitro system) should use
// active ip handler; in this case it is prefix IP handler
func TestUpdateResourceName_NitroInstance_PrefixIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	mock.MockResourceManager.EXPECT().GetResourceProvider(config.ResourceNameIPAddressFromPrefix).Return(mockProvider, true)
	mock.MockCondition.EXPECT().IsWindowsPrefixDelegationEnabled().Return(true)
	mock.MockNode.EXPECT().IsNitroInstance().Return(true)
	resourceName := mock.PodReconciler.updateResourceName(false, mockPod, mock.MockNode)

	// since PD is enabled, resource name is updated to be prefix IP so that prefix IP handler will be used
	assert.Equal(t, config.ResourceNameIPAddressFromPrefix, resourceName)
}

// TestUpdateResourceName_NitroInstance_SecondaryIP tests for pod creation events, supported instances (nitro system) should use
// active ip handler; in this case it is secondary IP handler
func TestUpdateResourceName_NitroInstance_SecondaryIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	mock.MockResourceManager.EXPECT().GetResourceProvider(config.ResourceNameIPAddressFromPrefix).Return(mockProvider, true)
	mock.MockCondition.EXPECT().IsWindowsPrefixDelegationEnabled().Return(false)
	resourceName := mock.PodReconciler.updateResourceName(false, mockPod, mock.MockNode)

	// since resource ip is not managed by prefix ip pool, resource name remains unchanged
	assert.Equal(t, config.ResourceNameIPAddress, resourceName)
}

// TestUpdateResourceName_WinPDFeatureOFF tests that when Windows PD feature flag is off, should return resource name for secondary IP
func TestUpdateResourceName_WinPDFeatureOFF(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, mockPod)
	mock.MockResourceManager.EXPECT().GetResourceProvider(config.ResourceNameIPAddressFromPrefix).Return(nil, false)
	resourceName := mock.PodReconciler.updateResourceName(true, mockPod, mock.MockNode)

	// since Windows PD feature flag is off, resource name remains ResourceNameIPAddress for secondary IP mode
	assert.Equal(t, config.ResourceNameIPAddress, resourceName)
}
