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
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)

	return Mocks{
		MockProviders:       mockProviders,
		ResourceProvider:    convertedProvider,
		MockResourceManager: mock_resource.NewMockResourceManager(ctrl),
		MockEC2API:          mock_api.NewMockEC2APIHelper(ctrl),
		MockK8sAPI:          mockK8sAPI,
		MockInstance:        mockInstance,
		NodeWithMock: node{
			log:          zap.New(zap.UseDevMode(true)).WithName("branch provider"),
			instance:     mockInstance,
			instanceType: nitroInstanceType,
			ec2API:       mock_api.NewMockEC2APIHelper(ctrl),
			k8sAPI:       mockK8sAPI,
		},
	}
}

// expectRestoreMiss makes InitResources use EC2 discovery.
func expectRestoreMiss(mock *Mocks) {
	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(nil, mockError).AnyTimes()
}

func histogramSampleCount(t *testing.T, observer prometheus.Observer) uint64 {
	t.Helper()
	metric, ok := observer.(prometheus.Metric)
	assert.True(t, ok)
	var value dto.Metric
	assert.NoError(t, metric.Write(&value))
	return value.GetHistogram().GetSampleCount()
}

// TestNewManagedNode tests the new node is not nil and node is managed but not ready
func TestNewManagedNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	node := NewManagedNode(zap.New(), nodeName, instanceID, nitroInstanceType, linux,
		mock_k8s.NewMockK8sWrapper(ctrl), mock_api.NewMockEC2APIHelper(ctrl))

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

func validNodeNetworkStateCNINode(instID, instType string) *rcv1alpha1.CNINode {
	return &rcv1alpha1.CNINode{
		Status: rcv1alpha1.CNINodeStatus{
			TrunkInterface: &rcv1alpha1.TrunkInterface{ID: "eni-trunk", SubnetID: "subnet-1"},
			NodeNetworkState: &rcv1alpha1.NodeNetworkState{
				InstanceID:                            instID,
				SubnetID:                              "subnet-1",
				SubnetCIDRBlock:                       "10.0.0.0/16",
				PrimaryNetworkInterfaceSecurityGroups: []string{"sg-1"},
			},
		},
	}
}

// TestNode_tryRestoreFromNodeNetworkState_Hit verifies restoration without a
// Kubernetes Node read or EC2 initialization.
func TestNode_tryRestoreFromNodeNetworkState_Hit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validNodeNetworkStateCNINode(instID, nitroInstanceType)

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState, nitroInstanceType, "eni-trunk")
	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.NodeWithMock.ec2API).Return(nil)
	mock.MockInstance.EXPECT().SubnetID().Return("subnet-1")

	assert.True(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_InitResources_RestoreSubnetLookupFailure verifies that a failed
// custom-networking subnet lookup falls back to authoritative EC2 discovery.
func TestNode_InitResources_RestoreSubnetLookupFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	cniNode := validNodeNetworkStateCNINode("i-abc", nitroInstanceType)

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState, nitroInstanceType, "eni-trunk")
	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.NodeWithMock.ec2API).Return(mockError)
	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	assert.NoError(t, mock.NodeWithMock.InitResources(mock.MockResourceManager))
	assert.True(t, mock.NodeWithMock.IsReady())
}

// TestNode_InitResources_RecordsSuccessResult verifies successful initialization
// is recorded without distinguishing how node state was loaded.
func TestNode_InitResources_RecordsSuccessResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	cniNode := validNodeNetworkStateCNINode("i-abc", nitroInstanceType)

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState, nitroInstanceType, "eni-trunk")
	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.NodeWithMock.ec2API).Return(nil)
	mock.MockInstance.EXPECT().SubnetID().Return("subnet-1")
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	metric := nodeInitDuration.WithLabelValues(initResultOK)
	before := histogramSampleCount(t, metric)

	assert.NoError(t, mock.NodeWithMock.InitResources(mock.MockResourceManager))

	assert.Equal(t, before+1, histogramSampleCount(t, metric))
}

// TestNode_InitResources_RecordsErrorResult verifies failed initialization is
// recorded without distinguishing how node state was loaded.
func TestNode_InitResources_RecordsErrorResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	cniNode := validNodeNetworkStateCNINode("i-abc", nitroInstanceType)

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState, nitroInstanceType, "eni-trunk")
	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.NodeWithMock.ec2API).Return(nil)
	mock.MockInstance.EXPECT().SubnetID().Return("subnet-1")
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(mockError)

	metric := nodeInitDuration.WithLabelValues(initResultError)
	before := histogramSampleCount(t, metric)

	assert.Error(t, mock.NodeWithMock.InitResources(mock.MockResourceManager))

	assert.Equal(t, before+1, histogramSampleCount(t, metric))
}

// TestNode_tryRestoreFromNodeNetworkState_NoState tests a GetCNINode error is a miss.
func TestNode_tryRestoreFromNodeNetworkState_NoState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(nil, mockError)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_tryRestoreFromNodeNetworkState_NoNodeNetworkState tests a CNINode
// without persisted state.
func TestNode_tryRestoreFromNodeNetworkState_NoNodeNetworkState(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(&rcv1alpha1.CNINode{}, nil)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_tryRestoreFromNodeNetworkState_MissingField tests required state
// fields.
func TestNode_tryRestoreFromNodeNetworkState_MissingField(t *testing.T) {
	for name, mutate := range map[string]func(*rcv1alpha1.NodeNetworkState){
		"instanceID":       func(c *rcv1alpha1.NodeNetworkState) { c.InstanceID = "" },
		"instanceSubnetID": func(c *rcv1alpha1.NodeNetworkState) { c.SubnetID = "" },
		"instanceSubnetCIDR": func(c *rcv1alpha1.NodeNetworkState) {
			c.SubnetCIDRBlock = ""
		},
		"primaryENISecurityGroups": func(c *rcv1alpha1.NodeNetworkState) {
			c.PrimaryNetworkInterfaceSecurityGroups = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewMock(ctrl, 0)
			instID := "i-abc"
			cniNode := validNodeNetworkStateCNINode(instID, nitroInstanceType)
			mutate(cniNode.Status.NodeNetworkState)

			mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
			mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
			mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

			assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
		})
	}
}

// TestNode_tryRestoreFromNodeNetworkState_InvalidSourceCIDR tests that an unparseable
// source (instance) CIDR is a miss, since later re-derivations read it.
func TestNode_tryRestoreFromNodeNetworkState_InvalidSourceCIDR(t *testing.T) {
	for name, mutate := range map[string]func(*rcv1alpha1.NodeNetworkState){
		"instanceSubnetCIDR": func(c *rcv1alpha1.NodeNetworkState) {
			c.SubnetCIDRBlock = "not-a-cidr"
		},
		"instanceSubnetV6CIDR": func(c *rcv1alpha1.NodeNetworkState) {
			c.SubnetV6CIDRBlock = "not-a-cidr"
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewMock(ctrl, 0)
			instID := "i-abc"
			cniNode := validNodeNetworkStateCNINode(instID, nitroInstanceType)
			mutate(cniNode.Status.NodeNetworkState)

			mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
			mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
			mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

			assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
		})
	}
}

// TestNode_tryRestoreFromNodeNetworkState_InstanceIDMismatch tests a reused CNINode
// name with a different instance id.
func TestNode_tryRestoreFromNodeNetworkState_InstanceIDMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	cniNode := validNodeNetworkStateCNINode("i-old", nitroInstanceType)

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-new").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_tryRestoreFromNodeNetworkState_MissingNodeInstanceType tests that an
// empty instance type from the Kubernetes Node is a miss, since the type is no
// longer persisted in the checkpoint and capacity cannot be sized without it.
func TestNode_tryRestoreFromNodeNetworkState_MissingNodeInstanceType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validNodeNetworkStateCNINode(instID, nitroInstanceType)
	mock.NodeWithMock.instanceType = ""

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_tryRestoreFromNodeNetworkState_UnsupportedType tests an instance type not
// in the supported limits is a miss.
func TestNode_tryRestoreFromNodeNetworkState_UnsupportedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validNodeNetworkStateCNINode(instID, "dummy.large")
	mock.NodeWithMock.instanceType = ""

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_InitResources tests the instance details is loaded and the node is initialized without error
func TestNode_InitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	expectRestoreMiss(&mock)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

func TestNode_InitResources_InstanceNotTrunkSupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	expectRestoreMiss(&mock)

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
	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(nil, mockError).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(node, nil).Times(1)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(node, "Unsupported", msg, v1.EventTypeWarning).Times(1)
	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s, error: %w", testInstanceType, utils.ErrNotFound))

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.Error(t, err)
	assert.False(t, mock.NodeWithMock.IsReady())
}

// TestNode_InitResources_LoadInstanceDetails_Error tests that error is propagated when load instance details throws an error
func TestNode_InitResources_LoadInstanceDetails_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	expectRestoreMiss(&mock)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(mockError)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.Error(t, &ErrInitResources{Err: mockError}, err)
}

// TestNode_InitResources_SecondProviderInitFails tests when one of the resource provider fails to initialize
func TestNode_InitResources_SecondProviderInitFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)

	expectRestoreMiss(&mock)

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

func TestNode_tryRestoreFromNodeNetworkState_MissingTrunkInterface(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	cniNode := validNodeNetworkStateCNINode("i-abc", nitroInstanceType)
	cniNode.Status.TrunkInterface = nil

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_tryRestoreFromNodeNetworkState_UsesObservedTrunkID verifies that the
// trunk id comes from status.trunkInterface rather than duplicated state.
func TestNode_tryRestoreFromNodeNetworkState_UsesObservedTrunkID(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	cniNode := validNodeNetworkStateCNINode("i-abc", nitroInstanceType)
	cniNode.Status.TrunkInterface.ID = "eni-current-trunk"

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState, nitroInstanceType, "eni-current-trunk")
	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.NodeWithMock.ec2API).Return(nil)
	mock.MockInstance.EXPECT().SubnetID().Return("subnet-1")

	assert.True(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

func TestNode_tryRestoreFromNodeNetworkState_ObservedSubnetMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	cniNode := validNodeNetworkStateCNINode("i-abc", nitroInstanceType)
	cniNode.Status.TrunkInterface.SubnetID = "subnet-other"

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState, nitroInstanceType, "eni-trunk")
	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.NodeWithMock.ec2API).Return(nil)
	mock.MockInstance.EXPECT().SubnetID().Return("subnet-1").Times(2)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_tryRestoreFromNodeNetworkState_ManagedByOtherController tests that a CNINode
// owned by another controller is not used for restoration.
func TestNode_tryRestoreFromNodeNetworkState_ManagedByOtherController(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validNodeNetworkStateCNINode(instID, nitroInstanceType)
	cniNode.Spec.ManagedBy = rcv1alpha1.ManagedByEKSAutoMode

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

	assert.False(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}

// TestNode_tryRestoreFromNodeNetworkState_EmptyManagedByIsOurs tests the backward
// compatible case: an empty managedBy means the vpc-resource-controller owns the
// object, so objects created before the field existed can still restore.
func TestNode_tryRestoreFromNodeNetworkState_EmptyManagedByIsOurs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validNodeNetworkStateCNINode(instID, nitroInstanceType)
	cniNode.Spec.ManagedBy = ""

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState, nitroInstanceType, "eni-trunk")
	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.NodeWithMock.ec2API).Return(nil)
	mock.MockInstance.EXPECT().SubnetID().Return("subnet-1")

	assert.True(t, mock.NodeWithMock.tryRestoreFromNodeNetworkState())
}
