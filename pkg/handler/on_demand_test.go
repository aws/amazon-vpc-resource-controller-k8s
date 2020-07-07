/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package handler

import (
	"fmt"
	"testing"

	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
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

	mockError = fmt.Errorf("mock-error")

	mockPod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      mockPodName,
			Namespace: mockPodNamespace,
			UID:       types.UID(mockUID),
		},
		Spec: v1.PodSpec{
			NodeName: mockNodeName,
		},
		Status: v1.PodStatus{},
	}

	createJob = worker.OnDemandJob{
		UID:          types.UID(mockUID),
		Operation:    worker.OperationCreate,
		PodName:      mockPodName,
		PodNamespace: mockPodNamespace,
		RequestCount: 1,
	}

	deletedJob = worker.OnDemandJob{
		Operation:    worker.OperationDeleted,
		PodName:      mockPodName,
		PodNamespace: mockPodNamespace,
	}

	deletingJob = worker.OnDemandJob{
		Operation:    worker.OperationDeleting,
		UID:          types.UID(mockUID),
		PodName:      mockPodName,
		PodNamespace: mockPodNamespace,
		NodeName:     mockNodeName,
	}
)

// getHandlerWithMock returns the OnDemandHandler with mock Worker
func getHandlerWithMock(ctrl *gomock.Controller) (Handler, *mock_provider.MockResourceProvider) {
	mockProvider := mock_provider.NewMockResourceProvider(ctrl)
	log := zap.New(zap.UseDevMode(true)).WithName("on demand handler")

	handler := NewOnDemandHandler(log, map[string]provider.ResourceProvider{mockResourceName: mockProvider})

	return handler, mockProvider
}

// Test_NewOnDemandHandler tests new on demand handler in not nil
func Test_NewOnDemandHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _ := getHandlerWithMock(ctrl)
	assert.NotNil(t, handler)
}

// Test_CanHandle tests if handler is the resource worker it should return true
func Test_CanHandle(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _ := getHandlerWithMock(ctrl)
	canHandle := handler.CanHandle(mockResourceName)
	assert.True(t, canHandle)
}

// Test_HandleCreate tests the create job is submitted to the respective worker on create operation
func Test_HandleCreate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(createJob)

	err := handler.HandleCreate(mockResourceName, 1, mockPod)
	assert.NoError(t, err)
}

// Test_HandleDelete tests that the delete job is submitted to the respective worker on delete operation
func Test_HandleDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(deletedJob)

	err := handler.HandleDelete(mockPodNamespace, mockPodName)
	assert.NoError(t, err)
}

func Test_HandleDeleting(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(deletingJob)

	err := handler.HandleDeleting(mockResourceName, mockPod)
	assert.NoError(t, err)
}

// Test_HandleCreate_error tests that the create operation returns error if submit job returns an error
func Test_HandleCreate_error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, _ := getHandlerWithMock(ctrl)

	err := handler.HandleCreate(mockResourceName+"not-supported", 1, mockPod)
	assert.NotNil(t, err)
}
