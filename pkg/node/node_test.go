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
			log:      zap.New(zap.UseDevMode(true)).WithName("branch provider"),
			instance: mockInstance,
			ec2API:   mock_api.NewMockEC2APIHelper(ctrl),
			k8sAPI:   mockK8sAPI,
		},
	}
}

// expectFastPathMiss makes the zero-EC2 fast path in InitResources miss
// (GetCNINode returns an error) so tests exercise the existing EC2 LoadDetails
// path unchanged.
func expectFastPathMiss(mock *Mocks) {
	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(nil, mockError).AnyTimes()
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

func validCheckpointCNINode(instID, instType string) *rcv1alpha1.CNINode {
	return &rcv1alpha1.CNINode{
		Status: rcv1alpha1.CNINodeStatus{
			TrunkInterface: &rcv1alpha1.TrunkInterface{ID: "eni-trunk", SubnetID: "subnet-1"},
			ReinitCheckpoint: &rcv1alpha1.ReinitCheckpoint{
				TrunkENIID:                            "eni-trunk",
				InstanceID:                            instID,
				InstanceType:                          instType,
				InstanceSubnetID:                      "subnet-1",
				InstanceSubnetCIDRBlock:               "10.0.0.0/16",
				CurrentSubnetID:                       "subnet-1",
				CurrentSubnetCIDRBlock:                "10.0.0.0/16",
				CurrentInstanceSecurityGroups:         []string{"sg-1"},
				PrimaryNetworkInterfaceSecurityGroups: []string{"sg-1"},
			},
		},
	}
}

// expectNoNodeLabel makes the best-effort Node instance-type label read a no-op
// (GetNode returns an error, so the label check is skipped).
func expectNoNodeLabel(mock *Mocks) {
	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(nil, mockError).AnyTimes()
}

// TestNode_tryHydrateFromCheckpoint_Hit tests that a valid reinit checkpoint
// rebuilds the instance via LoadFromCheckpoint with no EC2 LoadDetails call.
func TestNode_tryHydrateFromCheckpoint_Hit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validCheckpointCNINode(instID, nitroInstanceType)

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	expectNoNodeLabel(&mock)
	// Hit path must hydrate from the checkpoint and never call LoadDetails.
	mock.MockInstance.EXPECT().LoadFromCheckpoint(*cniNode.Status.ReinitCheckpoint)

	assert.True(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_tryHydrateFromCheckpoint_NoCheckpoint tests a GetCNINode error is a miss.
func TestNode_tryHydrateFromCheckpoint_NoCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(nil, mockError)

	assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_tryHydrateFromCheckpoint_NoReinitCheckpoint tests a CNINode without a
// reinit checkpoint (e.g. first restart after upgrade) is a miss.
func TestNode_tryHydrateFromCheckpoint_NoReinitCheckpoint(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-abc").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(&rcv1alpha1.CNINode{}, nil)
	expectNoNodeLabel(&mock)

	assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_tryHydrateFromCheckpoint_MissingField tests that a checkpoint missing
// any required field is a miss. Both the effective (current*) fields consumed
// immediately and the source fields later re-derivations read are required.
func TestNode_tryHydrateFromCheckpoint_MissingField(t *testing.T) {
	for name, mutate := range map[string]func(*rcv1alpha1.ReinitCheckpoint){
		"trunkENIID":       func(c *rcv1alpha1.ReinitCheckpoint) { c.TrunkENIID = "" },
		"instanceID":       func(c *rcv1alpha1.ReinitCheckpoint) { c.InstanceID = "" },
		"instanceType":     func(c *rcv1alpha1.ReinitCheckpoint) { c.InstanceType = "" },
		"instanceSubnetID": func(c *rcv1alpha1.ReinitCheckpoint) { c.InstanceSubnetID = "" },
		"instanceSubnetCIDR": func(c *rcv1alpha1.ReinitCheckpoint) {
			c.InstanceSubnetCIDRBlock = ""
		},
		"primaryENISecurityGroups": func(c *rcv1alpha1.ReinitCheckpoint) {
			c.PrimaryNetworkInterfaceSecurityGroups = nil
		},
		"currentSubnetID":   func(c *rcv1alpha1.ReinitCheckpoint) { c.CurrentSubnetID = "" },
		"currentSubnetCIDR": func(c *rcv1alpha1.ReinitCheckpoint) { c.CurrentSubnetCIDRBlock = "" },
		"currentSecurityGroups": func(c *rcv1alpha1.ReinitCheckpoint) {
			c.CurrentInstanceSecurityGroups = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewMock(ctrl, 0)
			instID := "i-abc"
			cniNode := validCheckpointCNINode(instID, nitroInstanceType)
			mutate(cniNode.Status.ReinitCheckpoint)

			mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
			mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
			mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
			expectNoNodeLabel(&mock)

			assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
		})
	}
}

// TestNode_tryHydrateFromCheckpoint_InvalidSourceCIDR tests that an unparseable
// source (instance) CIDR is a miss, since later re-derivations read it.
func TestNode_tryHydrateFromCheckpoint_InvalidSourceCIDR(t *testing.T) {
	for name, mutate := range map[string]func(*rcv1alpha1.ReinitCheckpoint){
		"instanceSubnetCIDR": func(c *rcv1alpha1.ReinitCheckpoint) {
			c.InstanceSubnetCIDRBlock = "not-a-cidr"
		},
		"instanceSubnetV6CIDR": func(c *rcv1alpha1.ReinitCheckpoint) {
			c.InstanceSubnetV6CIDRBlock = "not-a-cidr"
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewMock(ctrl, 0)
			instID := "i-abc"
			cniNode := validCheckpointCNINode(instID, nitroInstanceType)
			mutate(cniNode.Status.ReinitCheckpoint)

			mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
			mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
			mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
			expectNoNodeLabel(&mock)

			assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
		})
	}
}

// TestNode_tryHydrateFromCheckpoint_InstanceIDMismatch tests a reused CNINode
// name (checkpoint instance id != live instance id) is a miss.
func TestNode_tryHydrateFromCheckpoint_InstanceIDMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	cniNode := validCheckpointCNINode("i-old", nitroInstanceType)

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return("i-new").AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	expectNoNodeLabel(&mock)

	assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_tryHydrateFromCheckpoint_InstanceTypeLabelMismatch tests a checkpoint
// whose instance type disagrees with the Kubernetes Node label is a miss.
func TestNode_tryHydrateFromCheckpoint_InstanceTypeLabelMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validCheckpointCNINode(instID, nitroInstanceType)
	labeledNode := &v1.Node{ObjectMeta: metaV1.ObjectMeta{
		Labels: map[string]string{v1.LabelInstanceTypeStable: "m5.large"},
	}}

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(labeledNode, nil).AnyTimes()

	assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_tryHydrateFromCheckpoint_InvalidCIDR tests an unparseable effective
// subnet CIDR is a miss.
func TestNode_tryHydrateFromCheckpoint_InvalidCIDR(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validCheckpointCNINode(instID, nitroInstanceType)
	cniNode.Status.ReinitCheckpoint.CurrentSubnetCIDRBlock = "not-a-cidr"

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	expectNoNodeLabel(&mock)

	assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_tryHydrateFromCheckpoint_UnsupportedType tests an instance type not
// in the supported limits is a miss.
func TestNode_tryHydrateFromCheckpoint_UnsupportedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validCheckpointCNINode(instID, "dummy.large")

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	expectNoNodeLabel(&mock)

	assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_InitResources tests the instance details is loaded and the node is initialized without error
func TestNode_InitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	expectFastPathMiss(&mock)

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

	expectFastPathMiss(&mock)

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

	expectFastPathMiss(&mock)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(mockError)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.Error(t, &ErrInitResources{Err: mockError}, err)
}

// TestNode_InitResources_SecondProviderInitFails tests when one of the resource provider fails to initialize
func TestNode_InitResources_SecondProviderInitFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)

	expectFastPathMiss(&mock)

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

// TestNode_tryHydrateFromCheckpoint_ObservedTrunkMismatch tests the identity check
// against the observed trunk. status.trunkInterface is the observed-resource layer,
// so if it is absent or has moved on while the controller-private checkpoint stayed
// behind, the checkpoint may describe a trunk that is no longer this node's.
// Serving allocations against it would be unsafe, so the node takes the EC2 path.
func TestNode_tryHydrateFromCheckpoint_ObservedTrunkMismatch(t *testing.T) {
	for name, mutate := range map[string]func(*rcv1alpha1.CNINode){
		"observed trunk absent": func(n *rcv1alpha1.CNINode) {
			n.Status.TrunkInterface = nil
		},
		"observed trunk id moved on": func(n *rcv1alpha1.CNINode) {
			n.Status.TrunkInterface.ID = "eni-some-other-trunk"
		},
		"observed trunk subnet moved on": func(n *rcv1alpha1.CNINode) {
			n.Status.TrunkInterface.SubnetID = "subnet-other"
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewMock(ctrl, 0)
			instID := "i-abc"
			cniNode := validCheckpointCNINode(instID, nitroInstanceType)
			mutate(cniNode)

			mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
			mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
			mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
			expectNoNodeLabel(&mock)

			assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
		})
	}
}

// TestNode_tryHydrateFromCheckpoint_ManagedByOtherController tests that a CNINode
// owned by another controller is never hydrated from. Exactly one controller owns
// an object's status, so a checkpoint found on someone else's CNINode is not ours
// to trust.
func TestNode_tryHydrateFromCheckpoint_ManagedByOtherController(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validCheckpointCNINode(instID, nitroInstanceType)
	cniNode.Spec.ManagedBy = rcv1alpha1.ManagedByEKSAutoMode

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)

	assert.False(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}

// TestNode_tryHydrateFromCheckpoint_EmptyManagedByIsOurs tests the backward
// compatible case: an empty managedBy means the vpc-resource-controller owns the
// object, so objects created before the field existed still hydrate.
func TestNode_tryHydrateFromCheckpoint_EmptyManagedByIsOurs(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 0)
	instID := "i-abc"
	cniNode := validCheckpointCNINode(instID, nitroInstanceType)
	cniNode.Spec.ManagedBy = ""

	mock.MockInstance.EXPECT().Name().Return(nodeName).AnyTimes()
	mock.MockInstance.EXPECT().InstanceID().Return(instID).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(cniNode, nil)
	expectNoNodeLabel(&mock)
	mock.MockInstance.EXPECT().LoadFromCheckpoint(*cniNode.Status.ReinitCheckpoint)

	assert.True(t, mock.NodeWithMock.tryHydrateFromCheckpoint())
}
