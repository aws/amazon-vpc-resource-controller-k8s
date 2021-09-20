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

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

var (
	resourceName = config.ResourceNamePodENI
	nodeName     = "ip-192-168-1-2.us-west-2.compute.internal"
	response     = "introspection-response"
)

type MockIntrospect struct {
	mockManager  *mock_resource.MockResourceManager
	mockProvider *mock_provider.MockResourceProvider
	handler      IntrospectHandler
	response     map[string]string
}

func NewMockIntrospectHandler(ctrl *gomock.Controller) MockIntrospect {
	mockManager := mock_resource.NewMockResourceManager(ctrl)
	return MockIntrospect{
		mockManager:  mockManager,
		mockProvider: mock_provider.NewMockResourceProvider(ctrl),
		handler: IntrospectHandler{
			ResourceManager: mockManager,
		},
		response: map[string]string{resourceName: response},
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

	mock.mockProvider.EXPECT().IntrospectNode(nodeName).Return(response)

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
	mock.mockProvider.EXPECT().Introspect().Return(response)

	mock.handler.ResourceHandler(rr, req)
	VerifyResponse(t, rr, mock.response)
}

func VerifyResponse(t *testing.T, rr *httptest.ResponseRecorder, response map[string]string) {
	got := &map[string]string{}
	err := json.Unmarshal(rr.Body.Bytes(), got)
	assert.NoError(t, err)
	assert.Equal(t, rr.Code, http.StatusOK)
	assert.Equal(t, response, *got)
}
