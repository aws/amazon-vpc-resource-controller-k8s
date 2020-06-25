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
	"testing"

	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	mockResourceName = "resource-name"
	mockPodName      = "pod-name"
	mockPodNamespace = "pod-namespace"

	mockPod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      mockPodName,
			Namespace: mockPodNamespace,
		},
		Spec:   v1.PodSpec{},
		Status: v1.PodStatus{},
	}

	createJob = worker.OnDemandJob{
		Operation:    worker.OperationCreate,
		PodName:      mockPodName,
		PodNamespace: mockPodNamespace,
		RequestCount: 1,
	}

	deleteJob = worker.OnDemandJob{
		Operation:    worker.OperationDelete,
		PodName:      mockPodName,
		PodNamespace: mockPodNamespace,
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

	mockProvider.EXPECT().SubmitAsyncJob(createJob).Return(nil)

	err := handler.HandleCreate(mockResourceName, 1, mockPod)
	assert.NoError(t, err)
}

// Test_HandleDelete tests that the delete job is submitted to the respective worker on delete operation
func Test_HandleDelete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(deleteJob).Return(nil)

	err := handler.HandleDelete(mockResourceName, mockPod)
	assert.NoError(t, err)
}

// Test_HandleCreate_error tests that the create operation returns error if submit job returns an error
func Test_HandleCreate_error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(createJob).
		Return(worker.BufferOverflowError)

	err := handler.HandleCreate(mockResourceName, 1, mockPod)
	assert.Error(t, err, worker.BufferOverflowError)
}

// Test_HandleDelete_error tests that the delete operation returns error if submit job returns an error
func Test_HandleDelete_error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	handler, mockProvider := getHandlerWithMock(ctrl)

	mockProvider.EXPECT().SubmitAsyncJob(deleteJob).
		Return(worker.BufferOverflowError)

	err := handler.HandleDelete(mockResourceName, mockPod)
	assert.Error(t, err, worker.BufferOverflowError)
}
