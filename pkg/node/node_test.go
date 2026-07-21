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

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	"github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	nodeName   = "node-name"
	instanceID = "i-00000000001"
	linux      = "linux"
	mockError  = fmt.Errorf("mock error")
	mockNode   = v1.Node{
		ObjectMeta: metaV1.ObjectMeta{
			Name: nodeName,
		},
	}
	nitroInstanceType     = "t3.xlarge"
	nonNitroInstanceType  = "c1.medium"
	bareMetalInstanceType = "c5.metal"
)

type Mocks struct {
	MockProviders       map[string]*mock_provider.MockResourceProvider
	ResourceProvider    map[string]provider.ResourceProvider
	MockResourceManager *mock_resource.MockResourceManager
	MockInstance        *mock_ec2.MockEC2Instance
	MockEC2API          *mock_api.MockEC2APIHelper
	MockK8sAPI          *mock_k8s.MockK8sWrapper
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
		MockK8sAPI:          mock_k8s.NewMockK8sWrapper(ctrl),
		MockInstance:        mockInstance,
		NodeWithMock: node{
			log:      zap.New(zap.UseDevMode(true)).WithName("branch provider"),
			instance: mockInstance,
			ec2API:   mock_api.NewMockEC2APIHelper(ctrl),
		},
	}
}

// TestNewManagedNode tests the new node is not nil and node is managed but not ready
func TestNewManagedNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node := NewManagedNode(zap.New(), nodeName, instanceID, linux, mock_k8s.NewMockK8sWrapper(ctrl), mock_api.NewMockEC2APIHelper(ctrl))

	assert.NotNil(t, node)
	assert.True(t, node.GetNodeInstanceID() == instanceID)
	assert.True(t, node.IsManaged())
	assert.False(t, node.IsReady())
}

// TestNewUnManagedNode tests the new node is not nil and node is not managed
func TestNewUnManagedNode(t *testing.T) {
	node := NewUnManagedNode(zap.New(), nodeName, instanceID, linux)

	assert.NotNil(t, node)
	assert.False(t, node.IsManaged())
	assert.False(t, node.IsReady())
	assert.True(t, node.GetNodeInstanceID() == instanceID)
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

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

func TestNode_InitResources_CNINodeStatusHydrated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	mock.NodeWithMock.k8sAPI = mock.MockK8sAPI
	status := rcv1alpha1.CNINodeStatus{
		SnapshotVersion: rcv1alpha1.CNINodeStatusSnapshotVersion,
	}

	mock.MockInstance.EXPECT().Name().Return(nodeName)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeName}).Return(&rcv1alpha1.CNINode{
		Status: status,
	}, nil)
	mock.MockInstance.EXPECT().HydrateFromCNINodeStatus(status).Return(true, "hit")
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)

	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

// TestNode_InitResources_ProofMetric_Hydrated verifies the proof metric increments the "hydrated"
// result (EC2 LoadDetails skipped) when the instance hydrates from the CNINode status snapshot.
func TestNode_InitResources_ProofMetric_Hydrated(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	before := testutil.ToFloat64(nodeReinitEC2Skipped.WithLabelValues(reinitResultHydrated))
	beforeLatencyCount := summarySampleCount(t, nodeInitStageLatency.WithLabelValues(stageLoadDetails))

	mock := NewMock(ctrl, 1)
	mock.NodeWithMock.k8sAPI = mock.MockK8sAPI
	status := rcv1alpha1.CNINodeStatus{SnapshotVersion: rcv1alpha1.CNINodeStatusSnapshotVersion}

	mock.MockInstance.EXPECT().Name().Return(nodeName)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeName}).Return(&rcv1alpha1.CNINode{
		Status: status,
	}, nil)
	mock.MockInstance.EXPECT().HydrateFromCNINodeStatus(status).Return(true, "hit")
	// LoadDetails must NOT be called on the hydrated path.
	mock.MockInstance.EXPECT().LoadDetails(gomock.Any()).Times(0)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NoError(t, err)

	after := testutil.ToFloat64(nodeReinitEC2Skipped.WithLabelValues(reinitResultHydrated))
	assert.Equal(t, before+1, after)

	// The hydrated fast path must still record a load-details datapoint (a 0s
	// observation) so the summary proves LoadDetails was skipped instead of
	// carrying no samples at all.
	afterLatencyCount := summarySampleCount(t, nodeInitStageLatency.WithLabelValues(stageLoadDetails))
	assert.Equal(t, beforeLatencyCount+1, afterLatencyCount)
}

// summarySampleCount returns the number of observations recorded on a summary metric child.
func summarySampleCount(t *testing.T, observer prometheus.Observer) uint64 {
	metric, ok := observer.(prometheus.Metric)
	assert.True(t, ok, "observer must implement prometheus.Metric")
	var dtoMetric dto.Metric
	assert.NoError(t, metric.Write(&dtoMetric))
	return dtoMetric.GetSummary().GetSampleCount()
}

// TestNode_InitResources_ProofMetric_EC2Fallback verifies the proof metric increments the
// "ec2_fallback" result when hydrate misses and LoadDetails runs.
func TestNode_InitResources_ProofMetric_EC2Fallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	before := testutil.ToFloat64(nodeReinitEC2Skipped.WithLabelValues(reinitResultEC2Fallback))

	mock := NewMock(ctrl, 1)
	// k8sAPI nil -> tryHydrate returns false -> fallback path.
	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NoError(t, err)

	after := testutil.ToFloat64(nodeReinitEC2Skipped.WithLabelValues(reinitResultEC2Fallback))
	assert.Equal(t, before+1, after)
}

func TestNode_InitResources_InstanceNotTrunkSupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(false)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

func TestNode_InitResources_InstanceNotListed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	testInstanceType := "dummy.large"
	nodeName = "testInstance"
	node := &v1.Node{
		ObjectMeta: metaV1.ObjectMeta{Name: nodeName, UID: types.UID(nodeName)},
	}

	msg := "The instance type dummy.large is not supported yet by the vpc resource controller"

	mock.MockInstance.EXPECT().Type().Return(testInstanceType).Times(1)
	mock.MockInstance.EXPECT().Name().Return(nodeName).Times(2)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeName}).Return(&rcv1alpha1.CNINode{}, mockError)
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(node, nil).Times(1)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(node, "Unsupported", msg, v1.EventTypeWarning).Times(1)
	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s, error: %w", testInstanceType, utils.ErrNotFound))

	mock.NodeWithMock.k8sAPI = mock.MockK8sAPI
	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.Error(t, err)
	assert.False(t, mock.NodeWithMock.IsReady())
}

// TestNode_InitResources_LoadInstanceDetails_Error tests that error is propagated when load instance details throws an error
func TestNode_InitResources_LoadInstanceDetails_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(mockError)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
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

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
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

	err := mock.NodeWithMock.DeleteResources(mock.MockResourceManager)
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

	err := mock.NodeWithMock.DeleteResources(mock.MockResourceManager)
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

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager)
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

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager)
	assert.NotNil(t, err)
}

// TestNode_UpdateResources_NodeNotReady tests that if the node is not ready then update on resources
// is not invoked
func TestNode_UpdateResources_NodeNotReady(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager)
	assert.Nil(t, err)
}

// TestNode_IsNitroInstance_Nitro tests that if the node is nitro instance type, it should return true
func TestNode_IsNitroInstance_Nitro(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	mock.MockInstance.EXPECT().Type().Return(nitroInstanceType)

	assert.True(t, mock.NodeWithMock.IsNitroInstance())
}

// TestNode_IsNitroInstance_BareMetal tests that if the node is bare metal, which means it's built on nitro system, it should return true
func TestNode_IsNitroInstance_BareMetal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	mock.MockInstance.EXPECT().Type().Return(bareMetalInstanceType)

	assert.True(t, mock.NodeWithMock.IsNitroInstance())
}

// TestNode_IsNitroInstance_NonNitro tests that if the node is non-nitro instance type, it should return false
func TestNode_IsNitroInstance_NonNitro(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	mock.MockInstance.EXPECT().Type().Return(nonNitroInstanceType)

	assert.False(t, mock.NodeWithMock.IsNitroInstance())
}
