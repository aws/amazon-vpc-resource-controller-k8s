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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

var (
	resourceName          = config.ResourceNamePodENI
	nodeName              = "ip-192-168-1-2.us-west-2.compute.internal"
	introspectionResponse = "introspection-response"

	mockNodeName = "node-name"
	mockPod1     = "mock-pod1"
	mockPod2     = "mock-pod2"
	defaultNS    = "default"
	mockNS       = "mock-ns"
	mockListPods = &v1.PodList{
		Items: []v1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mockPod1,
					Namespace: defaultNS,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      mockPod2,
					Namespace: mockNS,
				},
			},
		},
	}
	mockPods = []v1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      mockPod1,
				Namespace: mockNS,
			},
		},
	}
	mockPodDataStoreResponse = map[string]interface{}{
		"PodList": []interface{}{
			types.NamespacedName{Namespace: mockNS, Name: mockPod1}.String(),
			types.NamespacedName{Namespace: mockNS, Name: mockPod2}.String(),
		},
	}
	mockNodePodsResponse = map[string]interface{}{
		"Pods": map[string]interface{}{
			"PodList": []interface{}{
				types.NamespacedName{Namespace: defaultNS, Name: mockPod1}.String(),
				types.NamespacedName{Namespace: mockNS, Name: mockPod2}.String(),
			},
		},
		"RunningPods": map[string]interface{}{
			"PodList": []interface{}{
				types.NamespacedName{Namespace: mockNS, Name: mockPod1}.String(),
			},
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
	VerifyResponse(t, rr, mock.response, http.StatusOK)
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

	mock.handler.ResourceHandler(rr, req)
	VerifyResponse(t, rr, mock.response, http.StatusOK)
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
	VerifyResponse(t, rr, mock.response, http.StatusOK)
}

func TestIntrospectHandler_DatastoreResourceHandler(t *testing.T) {
	type args struct {
		request    string
		response   map[string]interface{}
		statusCode int
	}
	type fields struct {
		mockPodAPIWrapper *mock_pod.MockPodClientAPIWrapper
	}
	tests := []struct {
		name    string
		prepare func(f *fields)
		args    args
	}{
		{
			name: "TestListPods, verify response for list of pods on the node",
			prepare: func(f *fields) {
				f.mockPodAPIWrapper.EXPECT().ListPods(mockNodeName).Return(mockListPods, nil)
				f.mockPodAPIWrapper.EXPECT().GetRunningPodsOnNode(mockNodeName).Return(mockPods, nil)
			},
			args: args{
				request: GetDatastoreResourcePrefix + "node/" + mockNodeName,
				response: map[string]interface{}{
					mockNodeName: mockNodePodsResponse,
				},
				statusCode: http.StatusOK,
			},
		},
		{
			name: "TestPodDatastore, verify response for pod datastore",
			prepare: func(f *fields) {
				f.mockPodAPIWrapper.EXPECT().Introspect().Return(mockPodDataStoreResponse)
			},
			args: args{
				request: GetDatastoreResourcePrefix + "all",
				response: map[string]interface{}{
					"PodDatastore": mockPodDataStoreResponse,
				},
				statusCode: http.StatusOK,
			},
		},
		{
			name: "TestInvalidRequest, verify response for invalid request",
			args: args{
				request:    GetDatastoreResourcePrefix + "invalid",
				response:   map[string]interface{}{},
				statusCode: http.StatusNotFound,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewMockIntrospectHandler(ctrl)
			req, err := http.NewRequest("GET", tt.args.request, nil)
			assert.NoError(t, err)
			rr := httptest.NewRecorder()

			f := fields{
				mockPodAPIWrapper: mock.mockPodAPIWrapper,
			}
			if tt.prepare != nil {
				tt.prepare(&f)
			}
			mock.handler.DatastoreResourceHandler(rr, req)
			VerifyResponse(t, rr, tt.args.response, tt.args.statusCode)
		})
	}

}

func VerifyResponse(t *testing.T, rr *httptest.ResponseRecorder, response map[string]interface{}, statusCode int) {
	assert.Equal(t, statusCode, rr.Code)
	// cannot unmarshal response into map[string]interface{} when request is invalid and status is not OK
	if statusCode != http.StatusOK {
		return
	}
	got := make(map[string]interface{})
	err := json.Unmarshal(rr.Body.Bytes(), &got)
	assert.NoError(t, err)
	assert.Equal(t, response, got)
}
