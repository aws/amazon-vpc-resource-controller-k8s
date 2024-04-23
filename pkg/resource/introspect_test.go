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

package resource

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mock_pod "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s/pod"
	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"k8s.io/apimachinery/pkg/types"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

var (
	resourceName              = config.ResourceNamePodENI
	nodeName                  = "ip-192-168-1-2.us-west-2.compute.internal"
	introspectionResponse     = "introspection-response"
	mockPodIntrospectResponse = map[string]interface{}{
		"PodDatastoreKeys": []interface{}{
			types.NamespacedName{Namespace: "mock-namespace", Name: "mock-pod1"}.String(),
			types.NamespacedName{Namespace: "mock-namespace", Name: "mock-pod2"}.String(),
		},
	}
)

type MockIntrospect struct {
	mockManager       *mock_resource.MockResourceManager
	mockProvider      *mock_provider.MockResourceProvider
	mockPodAPIWrapper *mock_pod.MockPodClientAPIWrapper
	handler           IntrospectHandler
	response          map[string]interface{}
}

func NewMockIntrospectHandler(ctrl *gomock.Controller) MockIntrospect {
	mockManager := mock_resource.NewMockResourceManager(ctrl)
	mockPodApiWrapper := mock_pod.NewMockPodClientAPIWrapper(ctrl)
	return MockIntrospect{
		mockManager:       mockManager,
		mockProvider:      mock_provider.NewMockResourceProvider(ctrl),
		mockPodAPIWrapper: mockPodApiWrapper,
		handler: IntrospectHandler{
			ResourceManager: mockManager,
			PodAPIWrapper:   mockPodApiWrapper,
		},
		response: map[string]interface{}{resourceName: introspectionResponse},
	}
}

func TestIntrospectHandler_NodeResourceHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMockIntrospectHandler(ctrl)

	req, err := http.NewRequest("GET", GetNodeResourcesPath+nodeName, nil)
	assert.NoError(t, err)
	rr := httptest.NewRecorder()

	mock.mockManager.EXPECT().GetResourceProviders().
		Return(map[string]provider.ResourceProvider{resourceName: mock.mockProvider})

	mock.mockProvider.EXPECT().IntrospectNode(nodeName).Return(introspectionResponse)

	mock.handler.NodeResourceHandler(rr, req)
	VerifyResponse(t, rr, mock.response)
}

func TestIntrospectHandler_ResourceHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMockIntrospectHandler(ctrl)

	req, err := http.NewRequest("GET", GetAllResourcesPath+nodeName, nil)
	assert.NoError(t, err)
	rr := httptest.NewRecorder()

	mock.mockManager.EXPECT().GetResourceProviders().
		Return(map[string]provider.ResourceProvider{resourceName: mock.mockProvider})
	mock.mockProvider.EXPECT().Introspect().Return(introspectionResponse)

	mock.mockPodAPIWrapper.EXPECT().Introspect().Return(mockPodIntrospectResponse)

	mock.handler.ResourceHandler(rr, req)
	mockResourceHandlerResponse := make(map[string]interface{})
	mockResourceHandlerResponse[config.ResourceNamePodENI] = introspectionResponse
	mockResourceHandlerResponse[PodAPIWrapperResourcesKey] = mockPodIntrospectResponse
	VerifyResponse(t, rr, mockResourceHandlerResponse)
}

func TestIntrospectHandler_SummaryHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMockIntrospectHandler(ctrl)

	req, err := http.NewRequest("GET", GetAllResourcesPath+nodeName, nil)
	assert.NoError(t, err)
	rr := httptest.NewRecorder()

	mock.mockManager.EXPECT().GetResourceProviders().
		Return(map[string]provider.ResourceProvider{resourceName: mock.mockProvider})
	mock.mockProvider.EXPECT().IntrospectSummary().Return(introspectionResponse)

	mock.handler.ResourceSummaryHandler(rr, req)
	VerifyResponse(t, rr, mock.response)
}

func VerifyResponse(t *testing.T, rr *httptest.ResponseRecorder, response map[string]interface{}) {
	got := make(map[string]interface{})
	err := json.Unmarshal(rr.Body.Bytes(), &got)
	assert.NoError(t, err)
	assert.Equal(t, rr.Code, http.StatusOK)
	assert.Equal(t, response, got)
}
