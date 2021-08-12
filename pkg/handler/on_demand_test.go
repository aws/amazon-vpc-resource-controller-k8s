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

	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	mockResourceName = "resource-name"
	mockPodName      = "pod-name"
	mockPodNamespace = "pod-namespace"
	mockUID          = "pod-uid"
	mockNodeName     = "node-name"

	mockPod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        mockPodName,
			Namespace:   mockPodNamespace,
			UID:         types.UID(mockUID),
			Annotations: map[string]string{mockResourceName: "resource-id"},
		},
		Spec: v1.PodSpec{
			NodeName: mockNodeName,
		},
		Status: v1.PodStatus{},
	}

	createJob = worker.OnDemandJob{
		Operation:    worker.OperationCreate,
		PodName:      mockPodName,
		PodNamespace: mockPodNamespace,
		RequestCount: 1,
	}

	deletedJob = worker.OnDemandJob{
		Operation: worker.OperationDeleted,
		NodeName:  mockNodeName,
		UID:       mockUID,
	}

	deletingJob = worker.OnDemandJob{
		Operation:    worker.OperationDeleting,
		UID:          mockUID,
		PodName:      mockPodName,
		PodNamespace: mockPodNamespace,
		NodeName:     mockNodeName,
	}
)

// getHandlerWithMock returns the OnDemandHandler with mock Worker
func getHandlerWithMock(ctrl *gomock.Controller) (Handler, *mock_provider.MockResourceProvider) {
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	log := zap.New(zap.UseDevMode(true)).WithName("on demand handler")

	handler := NewOnDemandHandler(log, mockResourceName, mockProvider)

	return handler, mockProvider
}

// Test_NewOnDemandHandler tests new on demand handler in not nil
func Test_NewOnDemandHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _ := getHandlerWithMock(ctrl)
	assert.NotNil(t, handler)
}

// Test_GetProvider tests if handler returns the provider for the resource
func Test_GetProvider(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)
	provider := handler.GetProvider()

	assert.Equal(t, provider, mockProvider)
}

// Test_HandleCreate tests the create job is submitted to the respective worker on create operation
func Test_HandleCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(createJob)

	_, err := handler.HandleCreate(1, mockPod)
	assert.NoError(t, err)
}

// Test_HandleDelete tests that the delete job is submitted to the respective worker on delete operation
func Test_HandleDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(deletedJob)

	_, err := handler.HandleDelete(mockPod)
	assert.NoError(t, err)
}
