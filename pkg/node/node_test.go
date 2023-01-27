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

package node

import (
	"fmt"
	"strconv"
	"testing"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	nodeName  = "node-name"
	mockError = fmt.Errorf("mock error")
	mockNode  = v1.Node{
		ObjectMeta: metaV1.ObjectMeta{
			Name: nodeName,
		},
	}
)

type Mocks struct {
	MockProviders       map[string]*mock_provider.MockResourceProvider
	ResourceProvider    map[string]provider.ResourceProvider
	MockResourceManager *mock_resource.MockResourceManager
	MockInstance        *mock_ec2.MockEC2Instance
	MockEC2API          *mock_api.MockEC2APIHelper
	NodeWithMock        node
}

func NewMock(ctrl *gomock.Controller, mockProviderCount int) Mocks {
	mockProviders := map[string]*mock_provider.MockResourceProvider{}
	convertedProvider := map[string]provider.ResourceProvider{}
	for i := 0; i < mockProviderCount; i++ {
		mockProvider := mock_provider.NewMockResourceProvider(ctrl)
		mockProviders[strconv.Itoa(i)] = mockProvider
		convertedProvider[strconv.Itoa(i)] = mockProvider
	}
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)

	return Mocks{
		MockProviders:       mockProviders,
		ResourceProvider:    convertedProvider,
		MockResourceManager: mock_resource.NewMockResourceManager(ctrl),
		MockEC2API:          mock_api.NewMockEC2APIHelper(ctrl),
		MockInstance:        mockInstance,
		NodeWithMock: node{
			log:      zap.New(zap.UseDevMode(true)).WithName("branch provider"),
			instance: mockInstance,
		},
	}
}

// TestNewManagedNode tests the new node is not nil and node is managed but not ready
func TestNewManagedNode(t *testing.T) {
	node := NewManagedNode(zap.New(), nodeName, "", "")

	assert.NotNil(t, node)
	assert.True(t, node.IsManaged())
	assert.False(t, node.IsReady())
}

// TestNewUnManagedNode tests the new node is not nil and node is not managed
func TestNewUnManagedNode(t *testing.T) {
	node := NewUnManagedNode()

	assert.NotNil(t, node)
	assert.False(t, node.IsManaged())
	assert.False(t, node.IsReady())
}

// TestNode_InitResources tests the instance details is loaded and the node is initialized without error
func TestNode_InitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager, mock.MockEC2API)
	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

func TestNode_InitResources_InstanceNotSupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(false)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager, mock.MockEC2API)
	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

// TestNode_InitResources_LoadInstanceDetails_Error tests that error is propagated when load instance details throws an error
func TestNode_InitResources_LoadInstanceDetails_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(mockError)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager, mock.MockEC2API)
	assert.Error(t, &ErrInitResources{Err: mockError}, err)
}

// TestNode_InitResources_SecondProviderInitFails tests when one of the resource provider fails to initialize
func TestNode_InitResources_SecondProviderInitFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	// Second provider throws an error
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true).AnyTimes()
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil).AnyTimes()

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true).AnyTimes()
	mock.MockProviders["1"].EXPECT().InitResource(mock.MockInstance).Return(mockError).AnyTimes()

	// Expect first provider to be de initialized
	mock.MockProviders["0"].EXPECT().DeInitResource(mock.MockInstance).Return(nil).AnyTimes()

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager, mock.MockEC2API)
	assert.NotNil(t, err)
}

// TestNode_DeleteResources tests that delete resources doesn't return an error when all resources are deleted without error
func TestNode_DeleteResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["1"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.DeleteResources(mock.MockResourceManager, mock.MockEC2API)
	assert.NoError(t, err)
}

// TestNode_DeleteResources_SomeFail tests that delete returns an error when some of the resources fail to delete
func TestNode_DeleteResources_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 3)

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["1"].EXPECT().DeInitResource(mock.MockInstance).Return(mockError)

	mock.MockProviders["2"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["2"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.DeleteResources(mock.MockResourceManager, mock.MockEC2API)
	assert.NotNil(t, err)
}

// TestNode_UpdateResources tests that no error is returned when node is updated successfully
func TestNode_UpdateResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)
	mock.NodeWithMock.ready = true

	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.MockEC2API).Return(nil)

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(false)

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager, mock.MockEC2API)
	assert.NoError(t, err)
}

// TestNode_UpdateResources_SomeFail tests that error is returned if some of the resource fail to advertise the capacity
func TestNode_UpdateResources_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 3)
	mock.NodeWithMock.ready = true

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["1"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(mockError)

	mock.MockProviders["2"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["2"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager, mock.MockEC2API)
	assert.NotNil(t, err)
}

// TestNode_UpdateResources_NodeNotReady tests that if the node is not ready then update on resources
// is not invoked
func TestNode_UpdateResources_NodeNotReady(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager, mock.MockEC2API)
	assert.Nil(t, err)
}
