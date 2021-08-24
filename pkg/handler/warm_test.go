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

package handler

import (
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/pod"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	resourceName = config.ResourceNameIPAddress

	uid          = "uid"
	nodeName     = "node-1"
	podName      = "pod-1"
	podNamespace = "pod-ns"
	ipAddress    = "192.168.1.1"

	pod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			UID:         types.UID(uid),
			Name:        podName,
			Namespace:   podNamespace,
			Annotations: map[string]string{config.ResourceNameIPAddress: ipAddress},
		},
		Spec: v1.PodSpec{
			NodeName: nodeName,
		},
		Status: v1.PodStatus{},
	}

	job = worker.NewWarmPoolCreateJob(nodeName, 1)
)

// TestWarmResourceHandler_HandleCreate tests create assigns a resource and annotates the pod and then reconciles a pool
// and submits the job to the resource provider
func TestWarmResourceHandler_HandleCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockK8sWrapper, mockPodAPI, mockProvider, mockPool := getHandlerAndMocks(ctrl)
	podCopy := pod.DeepCopy()

	mockProvider.EXPECT().GetPool(nodeName).Return(mockPool, true)
	mockPool.EXPECT().AssignResource(uid).Return(ipAddress, true, nil)
	mockPodAPI.EXPECT().AnnotatePod(pod.Namespace, pod.Name, resourceName, ipAddress).Return(nil)
	mockK8sWrapper.EXPECT().BroadcastEvent(podCopy, ReasonResourceAllocated, gomock.Any(), v1.EventTypeNormal)

	mockPool.EXPECT().ReconcilePool().Return(job)
	mockProvider.EXPECT().SubmitAsyncJob(job)

	delete(podCopy.Annotations, config.ResourceNameIPAddress)

	_, err := handler.HandleCreate(1, podCopy)
	assert.NoError(t, err)
}

func TestWarmResourceHandler_PoolEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockK8sWrapper, _, mockProvider, mockPool := getHandlerAndMocks(ctrl)
	podCopy := pod.DeepCopy()

	mockProvider.EXPECT().GetPool(nodeName).Return(mockPool, true)
	mockPool.EXPECT().AssignResource(uid).Return("", true, pool.ErrWarmPoolEmpty)
	mockK8sWrapper.EXPECT().BroadcastEvent(podCopy, ReasonResourceAllocationFailed, gomock.Any(), v1.EventTypeWarning)
	mockPool.EXPECT().ReconcilePool().Return(job)
	mockProvider.EXPECT().SubmitAsyncJob(job)

	delete(podCopy.Annotations, config.ResourceNameIPAddress)

	rslt, err := handler.HandleCreate(1, podCopy)
	assert.NoError(t, err)
	assert.Equal(t, k8sctrl.Result{
		Requeue:      true,
		RequeueAfter: RequeueAfterWhenWPEmpty,
	}, rslt)
}

// TestWarmResourceHandler_HandleCreate_AlreadyAnnotated tests that if the pod is already annotated then the pool
// is not invoked again
func TestWarmResourceHandler_HandleCreate_AlreadyAnnotated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _, _, mockProvider, mockPool := getHandlerAndMocks(ctrl)

	mockProvider.EXPECT().GetPool(nodeName).Return(mockPool, true)

	_, err := handler.HandleCreate(1, pod)
	assert.NoError(t, err)
}

// TestNewWarmResourceHandler_HandleDelete tests resources are deleted by calling the respective resource provider
func TestWarmResourceHandler_HandleDelete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _, _, mockProvider, mockPool := getHandlerAndMocks(ctrl)

	mockProvider.EXPECT().GetPool(nodeName).Return(mockPool, true)
	mockPool.EXPECT().FreeResource(uid, ipAddress).Return(false, nil)

	_, err := handler.HandleDelete(pod)
	assert.NoError(t, err)
}

// TestNewWarmResourceHandler_HandleDelete tests resources are deleted by calling the respective resource provider
func TestWarmResourceHandler_HandleDelete_ReconcileAfter(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _, _, mockProvider, mockPool := getHandlerAndMocks(ctrl)

	mockProvider.EXPECT().GetPool(nodeName).Return(mockPool, true)
	mockPool.EXPECT().FreeResource(uid, ipAddress).Return(true, nil)
	mockPool.EXPECT().ReconcilePool().Return(job)
	mockProvider.EXPECT().SubmitAsyncJob(job)

	_, err := handler.HandleDelete(pod)
	assert.NoError(t, err)
}

// TestNewWarmResourceHandler_HandleDelete tests resources are deleted by calling the respective resource provider
func TestWarmResourceHandler_HandleDelete_ResourceNotExist(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _, _, mockProvider, mockPool := getHandlerAndMocks(ctrl)

	mockProvider.EXPECT().GetPool(nodeName).Return(mockPool, true)

	podCopy := pod.DeepCopy()
	delete(podCopy.Annotations, config.ResourceNameIPAddress)

	_, err := handler.HandleDelete(podCopy)
	assert.NoError(t, err)
}

// TestNewWarmResourceHandler_HandleDelete_Error asserts error is returned if the resource pool is not found
func TestWarmResourceHandler_HandleDelete_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _, _, mockProvider, _ := getHandlerAndMocks(ctrl)

	mockProvider.EXPECT().GetPool(nodeName).Return(nil, false)

	_, err := handler.HandleDelete(pod)
	assert.NotNil(t, err)
}

// TestWarmResourceHandler_getResourcePool returns the resource pool for the given node name
func TestWarmResourceHandler_getResourcePool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _, _, mockProvider, mockPool := getHandlerAndMocks(ctrl)

	mockProvider.EXPECT().GetPool(nodeName).Return(mockPool, true)

	resourcePool, err := handler.getResourcePool(nodeName)

	assert.NoError(t, err)
	assert.Equal(t, mockPool, resourcePool)
}

// TestWarmResourceHandler_getResourcePool_PoolNotFound returns error if the resource pool is not present
func TestWarmResourceHandler_getResourcePool_PoolNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _, _, mockProvider, _ := getHandlerAndMocks(ctrl)

	mockProvider.EXPECT().GetPool(nodeName).Return(nil, false)

	_, err := handler.getResourcePool(nodeName)

	assert.NotNil(t, err)
}

func getHandlerAndMocks(ctrl *gomock.Controller) (*warmResourceHandler, *mock_k8s.MockK8sWrapper,
	*mock_pod.MockPodClientAPIWrapper, *mock_provider.MockResourceProvider, *mock_pool.MockPool) {

	mockWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	mockPool := mock_pool.NewMockPool(ctrl)
	mockPodAPI := mock_pod.NewMockPodClientAPIWrapper(ctrl)

	handler := &warmResourceHandler{
		log: zap.New(zap.UseDevMode(true)).WithName("warm handler"),
		APIWrapper: api.Wrapper{
			K8sAPI: mockWrapper,
			PodAPI: mockPodAPI,
		},
		resourceName:     resourceName,
		resourceProvider: mockProvider,
	}

	return handler, mockWrapper, mockPodAPI, mockProvider, mockPool
}
