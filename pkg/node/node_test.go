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
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	nodeName  = "node-name"
	mockError = fmt.Errorf("mock error")

	cidrBlock  = "0.0.0.0/24"
	mockSubnet = &ec2.Subnet{CidrBlock: &cidrBlock}
)

func getMocks(ctrl *gomock.Controller, count int) (*mock_resource.MockResourceManager,
	[]*mock_provider.MockResourceProvider) {

	mockResourceManager := mock_resource.NewMockResourceManager(ctrl)
	var mockProviders []*mock_provider.MockResourceProvider
	for i := 0; i < count; i++ {
		mockProviders = append(mockProviders, mock_provider.NewMockResourceProvider(ctrl))
	}
	return mockResourceManager, mockProviders
}

func getMockEC2APIHelper(ctrl *gomock.Controller) *mock_api.MockEC2APIHelper {
	return mock_api.NewMockEC2APIHelper(ctrl)
}

func getNodeWithInstanceMock(ctrl *gomock.Controller) (node, *mock_ec2.MockEC2Instance) {
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	return node{
		log:      zap.New(zap.UseDevMode(true)).WithName("branch provider"),
		instance: mockInstance,
	}, mockInstance
}

// TestNewNode tests the new node is not nil and node is not ready
func TestNewNode(t *testing.T) {
	node := NewNode(nil, nodeName, instanceID, config.OSLinux)
	assert.NotNil(t, node)
	assert.False(t, node.IsReady())
}

func convertMockTypeToProviderSlice(mockProviders []*mock_provider.MockResourceProvider) []provider.ResourceProvider {
	var providers []provider.ResourceProvider
	for _, provider := range mockProviders {
		providers = append(providers, provider)
	}
	return providers
}

// TestNode_InitResources tests the instance details is loaded and the node is initialized without error
func TestNode_InitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	mockResourceManager, mockProviders := getMocks(ctrl, 1)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockInstance.EXPECT().LoadDetails(mockHelper).Return(nil)
	mockResourceManager.EXPECT().GetResourceProviders().Return(convertMockTypeToProviderSlice(mockProviders))

	mockProviders[0].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[0].EXPECT().InitResource(mockInstance).Return(nil)

	err := node.InitResources(mockResourceManager, mockHelper)
	assert.NoError(t, err)
	assert.True(t, node.IsReady())
}

func TestNode_InitResources_InstanceNotSupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	mockResourceManager, mockProviders := getMocks(ctrl, 1)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockInstance.EXPECT().LoadDetails(mockHelper).Return(nil)
	mockResourceManager.EXPECT().GetResourceProviders().Return(convertMockTypeToProviderSlice(mockProviders))
	mockProviders[0].EXPECT().IsInstanceSupported(mockInstance).Return(false)

	err := node.InitResources(mockResourceManager, mockHelper)
	assert.NoError(t, err)
	assert.True(t, node.IsReady())
}

// TestNode_InitResources_LoadInstanceDetails_Error tests that error is propagated when load instance details throws an error
func TestNode_InitResources_LoadInstanceDetails_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	mockResourceManager, _ := getMocks(ctrl, 1)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockInstance.EXPECT().LoadDetails(mockHelper).Return(mockError)

	err := node.InitResources(mockResourceManager, mockHelper)
	assert.Error(t, &ErrInitResources{Err: mockError}, err)
}

// TestNode_InitResources_SecondProviderInitFails tests when one of the resource provider fails to initialize
func TestNode_InitResources_SecondProviderInitFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	mockResourceManager, mockProviders := getMocks(ctrl, 2)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockInstance.EXPECT().LoadDetails(mockHelper).Return(nil)

	mockResourceManager.EXPECT().GetResourceProviders().Return(convertMockTypeToProviderSlice(mockProviders))

	// Second provider throws an error
	mockProviders[0].EXPECT().InitResource(mockInstance).Return(nil)
	mockProviders[0].EXPECT().IsInstanceSupported(mockInstance).Return(true)

	mockProviders[1].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[1].EXPECT().InitResource(mockInstance).Return(mockError)

	// Expect first provider to be de initialized
	mockProviders[0].EXPECT().DeInitResource(mockInstance).Return(nil)

	err := node.InitResources(mockResourceManager, mockHelper)
	assert.NotNil(t, err)
}

// TestNode_DeleteResources tests that delete resources doesn't return an error when all resources are deleted without error
func TestNode_DeleteResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	mockResourceManager, mockProviders := getMocks(ctrl, 2)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockResourceManager.EXPECT().GetResourceProviders().Return(convertMockTypeToProviderSlice(mockProviders))

	mockProviders[0].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[0].EXPECT().DeInitResource(mockInstance).Return(nil)

	mockProviders[1].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[1].EXPECT().DeInitResource(mockInstance).Return(nil)

	err := node.DeleteResources(mockResourceManager, mockHelper)
	assert.NoError(t, err)
}

// TestNode_DeleteResources_SomeFail tests that delete returns an error when some of the resources fail to delete
func TestNode_DeleteResources_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	mockResourceManager, mockProviders := getMocks(ctrl, 3)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockResourceManager.EXPECT().GetResourceProviders().Return(convertMockTypeToProviderSlice(mockProviders))

	mockProviders[0].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[0].EXPECT().DeInitResource(mockInstance).Return(nil)

	mockProviders[1].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[1].EXPECT().DeInitResource(mockInstance).Return(mockError)

	mockProviders[2].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[2].EXPECT().DeInitResource(mockInstance).Return(nil)

	err := node.DeleteResources(mockResourceManager, mockHelper)
	assert.NotNil(t, err)
}

// TestNode_UpdateResources tests that no error is returned when node is updated successfully
func TestNode_UpdateResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	node.ready = true
	mockResourceManager, mockProviders := getMocks(ctrl, 2)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mockHelper).Return(nil)

	mockResourceManager.EXPECT().GetResourceProviders().Return(convertMockTypeToProviderSlice(mockProviders))

	mockProviders[0].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[0].EXPECT().UpdateResourceCapacity(mockInstance).Return(nil)

	mockProviders[1].EXPECT().IsInstanceSupported(mockInstance).Return(false)

	err := node.UpdateResources(mockResourceManager, mockHelper)
	assert.NoError(t, err)
}

// TestNode_UpdateResources_SomeFail tests that error is returned if some of the resource fail to advertise the capacity
func TestNode_UpdateResources_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, mockInstance := getNodeWithInstanceMock(ctrl)
	node.ready = true
	mockResourceManager, mockProviders := getMocks(ctrl, 3)
	mockHelper := getMockEC2APIHelper(ctrl)

	mockResourceManager.EXPECT().GetResourceProviders().Return(convertMockTypeToProviderSlice(mockProviders))

	mockProviders[0].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[0].EXPECT().UpdateResourceCapacity(mockInstance).Return(nil)

	mockProviders[1].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[1].EXPECT().UpdateResourceCapacity(mockInstance).Return(mockError)

	mockProviders[2].EXPECT().IsInstanceSupported(mockInstance).Return(true)
	mockProviders[2].EXPECT().UpdateResourceCapacity(mockInstance).Return(nil)

	err := node.UpdateResources(mockResourceManager, mockHelper)
	assert.NotNil(t, err)
}

// TestNode_UpdateResources_NodeNotReady tests that if the node is not ready then update on resources
// is not invoked
func TestNode_UpdateResources_NodeNotReady(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node, _ := getNodeWithInstanceMock(ctrl)
	node.ready = false
	mockResourceManager, _ := getMocks(ctrl, 3)
	mockHelper := getMockEC2APIHelper(ctrl)

	err := node.UpdateResources(mockResourceManager, mockHelper)
	assert.Nil(t, err)
}
