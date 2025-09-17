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

package manager

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	rcV1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_node "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	mock_worker "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	instanceID      = "i-01234567890abcdef"
	providerId      = "aws:///us-west-2c/" + instanceID
	eniConfigName   = "eni-config-name"
	subnetID        = "subnet-id"
	nodeName        = "ip-192-168-55-73.us-west-2.compute.internal"
	securityGroupId = "sg-1"
	mockClusterName = "cluster-name"

	eniConfig = &v1alpha1.ENIConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: eniConfigName,
		},
		Spec: v1alpha1.ENIConfigSpec{
			SecurityGroups: []string{securityGroupId},
			Subnet:         subnetID,
		},
	}

	eniConfig_empty_sg = &v1alpha1.ENIConfig{
		Spec: v1alpha1.ENIConfigSpec{
			SecurityGroups: []string{},
			Subnet:         subnetID,
		},
	}

	v1Node = &v1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: map[string]string{config.NodeLabelOS: config.OSLinux, config.HasTrunkAttachedLabel: "true"},
		},
		Spec: v1.NodeSpec{
			ProviderID: providerId,
		},
		Status: v1.NodeStatus{
			Capacity: map[v1.ResourceName]resource.Quantity{
				config.ResourceNamePodENI: *resource.NewQuantity(1, resource.DecimalExponent),
			},
		},
	}
	nodeList = &v1.NodeList{
		Items: append([]v1.Node{}, *v1Node),
	}
	mockError = fmt.Errorf("mock error")

	unManagedNode = node.NewUnManagedNode(zap.New(), nodeName, instanceID, config.OSLinux)
	managedNode   = node.NewManagedNode(zap.New(), nodeName, instanceID, config.OSLinux, nil, nil)

	healthzHandler = healthz.NewHealthzHandler(5)
)

type AsyncJobMatcher struct {
	expected AsyncOperationJob
}

func NewAsyncOperationMatcher(expected AsyncOperationJob) *AsyncJobMatcher {
	return &AsyncJobMatcher{expected: expected}
}

func (m *AsyncJobMatcher) Matches(actual interface{}) bool {
	actualJob := actual.(AsyncOperationJob)
	return actualJob.op == m.expected.op &&
		actualJob.nodeName == m.expected.nodeName &&
		actualJob.node.IsManaged() == m.expected.node.IsManaged() &&
		actualJob.nodeInstanceID == m.expected.nodeInstanceID
}

func (m *AsyncJobMatcher) String() string {
	return "verify AsyncOperationJob match"
}

func AreNodesEqual(expected node.Node, actual node.Node) bool {
	return expected.IsManaged() == actual.IsManaged() &&
		expected.IsReady() == actual.IsReady() && expected.GetNodeInstanceID() == actual.GetNodeInstanceID()
}

type Mock struct {
	Manager             manager
	MockK8sAPI          *mock_k8s.MockK8sWrapper
	MockEC2API          *mock_api.MockEC2APIHelper
	MockWorker          *mock_worker.MockWorker
	MockNode            *mock_node.MockNode
	MockResourceManager *mock_resource.MockResourceManager
	MockConditions      *mock_condition.MockConditions
}

func NewMock(ctrl *gomock.Controller, existingDataStore map[string]node.Node) Mock {
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockEC2APIHelper := mock_api.NewMockEC2APIHelper(ctrl)
	mockAsyncWorker := mock_worker.NewMockWorker(ctrl)
	mockResourceManager := mock_resource.NewMockResourceManager(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)

	return Mock{
		Manager: manager{
			dataStore: existingDataStore,
			Log:       zap.New(),
			wrapper: api.Wrapper{
				K8sAPI: mockK8sWrapper,
				EC2API: mockEC2APIHelper,
			},
			worker:           mockAsyncWorker,
			resourceManager:  mockResourceManager,
			conditions:       mockConditions,
			clusterName:      mockClusterName,
			instanceIDToFQDN: make(map[string]string),
			fqdnToInstanceID: make(map[string]string),
		},
		MockK8sAPI:          mockK8sWrapper,
		MockEC2API:          mockEC2APIHelper,
		MockWorker:          mockAsyncWorker,
		MockNode:            mockNode,
		MockResourceManager: mockResourceManager,
		MockConditions:      mockConditions,
	}
}

// Test_GetNewManager tests new node manager is created without error
func Test_GetNewManager(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	mock.MockWorker.EXPECT().StartWorkerPool(gomock.Any()).Return(nil)
	manager, err := NewNodeManager(zap.New(), nil, api.Wrapper{}, mock.MockWorker, mock.MockConditions, mockClusterName, "v1.3.1", healthzHandler)

	assert.NotNil(t, manager)
	assert.NoError(t, err)
}

// Test_GetNewManager tests new node manager is created with error
func Test_GetNewManager_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	mock.MockWorker.EXPECT().StartWorkerPool(gomock.Any()).Return(mockError)
	manager, err := NewNodeManager(zap.New(), nil, api.Wrapper{}, mock.MockWorker, mock.MockConditions, mockClusterName, "v1.3.1", healthzHandler)

	assert.NotNil(t, manager)
	assert.Error(t, err, mockError)
}

// Test_addOrUpdateNode_new_node tests if a node that doesn't exist in managed list is added and a request
// to perform init resource is returned.
func Test_AddNode_CNINode_Existing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	expectedJob := AsyncOperationJob{
		op:             Init,
		nodeName:       nodeName,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil).Times(1)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(expectedJob)))
	mock.MockK8sAPI.EXPECT().CreateCNINode(v1Node, mockClusterName).Return(nil).Times(0)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{}, nil).Times(2)

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_AddNode_CNINode_Not_Existing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	expectedJob := AsyncOperationJob{
		op:             Init,
		nodeName:       nodeName,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil).Times(1)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(expectedJob)))
	mock.MockK8sAPI.EXPECT().CreateCNINode(v1Node, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(
		&rcV1alpha1.CNINode{}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test")).
		Times(2)

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_AddNode_UnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	nodeWithoutLabel := v1Node.DeepCopy()
	nodeWithoutLabel.Labels = map[string]string{}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithoutLabel, nil).Times(1)
	mock.MockK8sAPI.EXPECT().CreateCNINode(nodeWithoutLabel, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithoutLabel.Name}).Return(
		&rcV1alpha1.CNINode{}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test")).
		Times(1) // unmanaged node won't check custom networking subnets and call GetCNINode only once

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], unManagedNode))
}

func Test_AddNode_AlreadyAdded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{instanceID: unManagedNode})

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], unManagedNode))
}

func Test_AddNode_CustomNetworking_CNINode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		op:             Init,
		nodeName:       nodeName,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	nodeWithENIConfig := v1Node.DeepCopy()

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil).Times(1)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))
	mock.MockK8sAPI.EXPECT().CreateCNINode(nodeWithENIConfig, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithENIConfig.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking, Value: eniConfigName}},
		},
	}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test"))
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithENIConfig.Name}).Return(
		&rcV1alpha1.CNINode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeWithENIConfig.Name},
			Spec: rcV1alpha1.CNINodeSpec{
				Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking, Value: eniConfigName}},
			},
		}, nil,
	).Times(2)
	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_AddNode_CustomNetworking_CNINode_No_EniConfigName(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		op:       Init,
		nodeName: nodeName,
		node:     managedNode,
	}

	nodeWithENIConfig := v1Node.DeepCopy()

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil)
	wantedError := fmt.Errorf("couldn't find custom networking eniconfig name for node %s, error: %w", nodeName, utils.ErrNotFound)
	msg := wantedError.Error()
	mock.MockK8sAPI.EXPECT().BroadcastEvent(nodeWithENIConfig, utils.EniConfigNameNotFoundReason, msg, v1.EventTypeWarning).Times(1)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil).Times(0)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job))).Times(0)
	mock.MockK8sAPI.EXPECT().CreateCNINode(nodeWithENIConfig, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithENIConfig.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking}},
		},
	}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test"))
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithENIConfig.Name}).Return(
		&rcV1alpha1.CNINode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeWithENIConfig.Name},
			Spec: rcV1alpha1.CNINodeSpec{
				Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking}},
			},
		}, nil,
	).Times(2)
	err := mock.Manager.AddNode(nodeName)
	assert.Error(t, err)
	assert.True(t, errors.Is(err, utils.ErrNotFound))
}

func Test_AddNode_CustomNetworking_NodeLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		op:             Init,
		nodeName:       nodeName,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil).Times(1)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))
	mock.MockK8sAPI.EXPECT().CreateCNINode(nodeWithENIConfig, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithENIConfig.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking}},
		},
	}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test")).Times(1)

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

// Test adding node when custom networking is enabled but incorrect ENIConfig is defined; it should succeed
// TODO: combine with other Test_AddNode_CustomNetworking tests
func Test_AddNode_CustomNetworking_Incorrect_ENIConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		op:             Init,
		nodeName:       nodeName,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig_empty_sg, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))
	mock.MockK8sAPI.EXPECT().CreateCNINode(nodeWithENIConfig, mockClusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithENIConfig.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking}},
		},
	}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test"))

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))

}

func Test_AddNode_CustomNetworking_NoENIConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil)
	mock.MockK8sAPI.EXPECT().CreateCNINode(nodeWithENIConfig, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(nil, mockError)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithENIConfig.Name}).Return(&rcV1alpha1.CNINode{}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test"))

	err := mock.Manager.AddNode(nodeName)
	assert.NotContains(t, mock.Manager.dataStore, nodeName)
	assert.Error(t, err, mockError)
}

func Test_UpdateNode_Managed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{instanceID: managedNode})

	job := AsyncOperationJob{
		op:             Update,
		nodeName:       nodeName,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{},
		},
	}, nil).Times(1)

	err := mock.Manager.UpdateNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_UpdateNode_UnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{instanceID: unManagedNode})

	k8sNode := v1Node.DeepCopy()
	k8sNode.Labels = map[string]string{}

	mock.MockK8sAPI.EXPECT().GetNode(v1Node.Name).Return(k8sNode, nil)

	err := mock.Manager.UpdateNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], unManagedNode))
}

func Test_UpdateNode_ManagedToUnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{instanceID: managedNode})

	job := AsyncOperationJob{
		op:             Delete,
		nodeName:       nodeName,
		node:           managedNode, // should pass the older cached value, instead of new node
		nodeInstanceID: instanceID,
	}

	updatedNode := v1Node.DeepCopy()
	updatedNode.Labels = map[string]string{}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(updatedNode, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))

	err := mock.Manager.UpdateNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], unManagedNode))
}

func Test_UpdateNode_UnManagedToManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{instanceID: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:             Init,
		nodeName:       v1Node.Name,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}
	mock.MockK8sAPI.EXPECT().GetNode(v1Node.Name).Return(v1Node, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{},
		},
	}, nil).Times(1)

	err := mock.Manager.UpdateNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_UpdateNode_UnManagedToManaged_WithENIConfig_NodeLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{instanceID: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:             Init,
		nodeName:       v1Node.Name,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mock.MockK8sAPI.EXPECT().GetNode(v1Node.Name).Return(nodeWithENIConfig, nil)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))

	err := mock.Manager.UpdateNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_UpdateNode_UnManagedToManaged_WithENIConfig_CNINode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{instanceID: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:             Init,
		nodeName:       v1Node.Name,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	nodeWithENIConfig := v1Node.DeepCopy()

	mock.MockK8sAPI.EXPECT().GetNode(v1Node.Name).Return(nodeWithENIConfig, nil)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking, Value: eniConfigName}},
		},
	}, nil).Times(2)

	err := mock.Manager.UpdateNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_DeleteNode_Managed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithManagedNode := map[string]node.Node{instanceID: managedNode}

	mock := NewMock(ctrl, dataStoreWithManagedNode)
	mock.Manager.fqdnToInstanceID[v1Node.Name] = instanceID
	mock.Manager.instanceIDToFQDN[instanceID] = v1Node.Name
	job := AsyncOperationJob{
		op:             Delete,
		nodeName:       v1Node.Name,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}

	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))

	err := mock.Manager.DeleteNode(v1Node.Name)
	assert.NoError(t, err)
	assert.NotContains(t, mock.Manager.dataStore, instanceID)
}

func Test_DeleteNode_UnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{instanceID: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	err := mock.Manager.DeleteNode(v1Node.Name)
	assert.NoError(t, err)
	assert.NotContains(t, mock.Manager.dataStore, nodeName)
}

func Test_DeleteNode_AlreadyDeleted(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	err := mock.Manager.DeleteNode(v1Node.Name)
	assert.NoError(t, err)
}

func Test_performAsyncOperation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{instanceID: managedNode})

	job := AsyncOperationJob{
		node:           mock.MockNode,
		nodeName:       nodeName,
		nodeInstanceID: instanceID,
	}

	job.op = Init

	mock.MockK8sAPI.EXPECT().AddLabelToManageNode(v1Node, config.HasTrunkAttachedLabel, config.BooleanTrue).Return(true, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(v1Node, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", mock.Manager.controllerVersion), v1.EventTypeNormal).Times(1)
	mock.MockNode.EXPECT().InitResources(mock.MockResourceManager).Return(nil)
	mock.MockNode.EXPECT().UpdateResources(mock.MockResourceManager).Return(nil)
	_, err := mock.Manager.performAsyncOperation(job)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.NoError(t, err)

	job.op = Update
	mock.MockNode.EXPECT().UpdateResources(mock.MockResourceManager).Return(nil)
	_, err = mock.Manager.performAsyncOperation(job)
	assert.NoError(t, err)

	job.op = Delete
	mock.MockNode.EXPECT().DeleteResources(mock.MockResourceManager).Return(nil)
	_, err = mock.Manager.performAsyncOperation(job)
	assert.NoError(t, err)

	job.op = ""
	_, err = mock.Manager.performAsyncOperation(job)
	assert.NoError(t, err)
}

func Test_performAsyncOperation_fail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{instanceID: managedNode})

	job := AsyncOperationJob{
		node:           mock.MockNode,
		nodeName:       nodeName,
		op:             Init,
		nodeInstanceID: instanceID,
	}

	mock.MockNode.EXPECT().InitResources(mock.MockResourceManager).Return(&node.ErrInitResources{})
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(v1Node, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", mock.Manager.controllerVersion), v1.EventTypeNormal).Times(1)

	_, err := mock.Manager.performAsyncOperation(job)
	assert.NotContains(t, mock.Manager.dataStore, instanceID) // It should be cleared from cache
	assert.NoError(t, err)
}

func Test_performAsyncOperation_fail_pausingHealthCheck(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{instanceID: managedNode})

	job := AsyncOperationJob{
		node:           mock.MockNode,
		nodeName:       nodeName,
		op:             Init,
		nodeInstanceID: instanceID,
	}

	mock.MockNode.EXPECT().InitResources(mock.MockResourceManager).Return(&node.ErrInitResources{
		Err: errors.New("RequestLimitExceeded: Request limit exceeded.\n\tstatus code: 503, request id: 123-123-123-123-123"),
	}).Times(2)
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil).Times(2)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(v1Node, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", mock.Manager.controllerVersion), v1.EventTypeNormal).Times(2)

	_, err := mock.Manager.performAsyncOperation(job)
	time.Sleep(time.Millisecond * 100)
	assert.True(t, mock.Manager.SkipHealthCheck())
	assert.NotContains(t, mock.Manager.dataStore, instanceID) // It should be cleared from cache
	assert.NoError(t, err)

	time.Sleep(time.Second * 2)
	_, err = mock.Manager.performAsyncOperation(job)
	assert.NoError(t, err)
	time.Sleep(time.Millisecond * 100)
	assert.True(t, mock.Manager.SkipHealthCheck())
	assert.True(t, time.Since(mock.Manager.stopHealthCheckAt) > time.Second*2 && time.Since(mock.Manager.stopHealthCheckAt) < time.Second*3)
}

// Test_isPodENICapacitySet test if the pod-eni capacity then true is returned
func Test_isPodENICapacitySet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})
	isSet, err := mock.Manager.canAttachTrunk(v1Node)
	assert.NoError(t, err)
	assert.True(t, isSet)
}

func Test_isPodENICapacitySet_CNINode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})
	emptyNode := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: "test"}).Return(
		&rcV1alpha1.CNINode{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: rcV1alpha1.CNINodeSpec{
				Features: []rcV1alpha1.Feature{
					{Name: rcV1alpha1.SecurityGroupsForPods},
				},
			},
		},
		nil).Times(1)
	isSet, err := mock.Manager.canAttachTrunk(emptyNode)
	assert.NoError(t, err)
	assert.True(t, isSet)
}

// Test_isPodENICapacitySet_Neg tests if the pod-eni capacity is not set then false is returned
func Test_isPodENICapacitySet_Neg(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})
	v1NodeCopy := v1Node.DeepCopy()
	delete(v1NodeCopy.Labels, config.HasTrunkAttachedLabel)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{},
		},
	}, nil).Times(1)
	isSet, err := mock.Manager.canAttachTrunk(v1NodeCopy)
	assert.NoError(t, err)
	assert.False(t, isSet)
}

// Test_isWindowsNode tests if the os label is set to windows then true is returned
func Test_isWindowsNode(t *testing.T) {
	v1NodeCopy := v1Node.DeepCopy()
	v1NodeCopy.Labels[config.NodeLabelOS] = config.OSWindows
	isSet := isWindowsNode(v1NodeCopy)
	assert.True(t, isSet)
}

// Test_isWindowsNode_BetaLabelSet tests if the beta os label is set then true is returned
func Test_isWindowsNode_BetaLabelSet(t *testing.T) {
	v1NodeCopy := v1Node.DeepCopy()
	delete(v1NodeCopy.Labels, config.NodeLabelOS)
	v1NodeCopy.Labels[config.NodeLabelOSBeta] = config.OSWindows

	isSet := isWindowsNode(v1NodeCopy)
	assert.True(t, isSet)
}

// Test_isWindowsNode_Linux tests if the node is OS linux then the function returns false
func Test_isWindowsNode_Linux(t *testing.T) {
	isSet := isWindowsNode(v1Node)
	assert.False(t, isSet)
}

// Test_getNodeInstanceID test if the correct node id is retrieved from the provider id
func Test_getNodeInstanceID(t *testing.T) {
	id := GetNodeInstanceID(v1Node)
	assert.Equal(t, instanceID, id)
}

// Test_getNodeOS tests that is OS label is set then the correct os is returned
func Test_getNodeOS(t *testing.T) {
	os := GetNodeOS(v1Node)
	assert.Equal(t, config.OSLinux, os)
}

// Test_isSelectedForManagement tests if the either the capacity or the label is set true is returned
func Test_isSelectedForManagement_WindowsIPAMEnabled_False(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	isSelected, err := mock.Manager.isSelectedForManagement(v1Node)
	assert.NoError(t, err)
	assert.True(t, isSelected)
}

func Test_isSelectedForManagement_WindowsIPAMEnabled_True(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	windowsNode := v1Node.DeepCopy()
	windowsNode.Labels = map[string]string{config.NodeLabelOS: config.OSWindows}
	mock := NewMock(ctrl, map[string]node.Node{})
	mock.MockConditions.EXPECT().IsWindowsIPAMEnabled().Return(true)

	isSelected, err := mock.Manager.isSelectedForManagement(windowsNode)
	assert.NoError(t, err)
	assert.True(t, isSelected)
}

func Test_UpdateNode_Windows_UnManagedToManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	windowsNode := v1Node.DeepCopy()
	windowsNode.Labels = map[string]string{config.NodeLabelOS: config.OSWindows}
	dataStoreWithUnManagedNode := map[string]node.Node{instanceID: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:             Init,
		nodeName:       windowsNode.Name,
		node:           managedNode,
		nodeInstanceID: instanceID,
	}
	mock.MockK8sAPI.EXPECT().GetNode(windowsNode.Name).Return(windowsNode, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))
	mock.MockConditions.EXPECT().IsWindowsIPAMEnabled().Return(true)
	// Windows node will also have a CNINode but Windows CNI will not update for features
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
		Spec: rcV1alpha1.CNINodeSpec{
			Features: []rcV1alpha1.Feature{},
		},
	}, nil).Times(1)

	err := mock.Manager.UpdateNode(windowsNode.Name)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, instanceID)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[instanceID], managedNode))
}

func Test_Node_HasInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	assert.True(t, managedNode.HasInstance(), "managed node should have instance")
	assert.True(t, unManagedNode.HasInstance(), "unmanaged node should have instance")
}

func Test_GetEniConfigName(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	testCases := []struct {
		desc     string
		value    string
		cniNode  rcV1alpha1.CNINode
		notFound bool
	}{
		{
			desc: "custom networking feature has been added",
			cniNode: rcV1alpha1.CNINode{
				ObjectMeta: metav1.ObjectMeta{
					Name: v1Node.Name,
				},
				Spec: rcV1alpha1.CNINodeSpec{
					Features: []rcV1alpha1.Feature{{Name: rcV1alpha1.CustomNetworking, Value: "default"}},
				},
			},
			value:    "default",
			notFound: false,
		},
		{
			desc: "no feature has been added",
			cniNode: rcV1alpha1.CNINode{
				ObjectMeta: metav1.ObjectMeta{
					Name: v1Node.Name,
				},
				Spec: rcV1alpha1.CNINodeSpec{
					Features: []rcV1alpha1.Feature{},
				},
			},
			value:    "",
			notFound: true,
		},
		{
			desc: "SGP feature has been added",
			cniNode: rcV1alpha1.CNINode{
				ObjectMeta: metav1.ObjectMeta{
					Name: v1Node.Name,
				},
				Spec: rcV1alpha1.CNINodeSpec{
					Features: []rcV1alpha1.Feature{
						{Name: rcV1alpha1.SecurityGroupsForPods, Value: ""},
					},
				},
			},
			value:    "",
			notFound: true,
		},
	}
	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&tC.cniNode, nil).Times(1)
			name, err := mock.Manager.GetEniConfigName(v1Node)
			assert.Equal(t, tC.notFound, errors.Is(err, utils.ErrNotFound))
			assert.Equal(t, tC.value, name)
		})
	}
}

func Test_TrunkEnabledInCNINode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{v1Node.Name: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	testCases := []struct {
		features []rcV1alpha1.Feature
		managed  bool
		msg      string
	}{
		{
			features: []rcV1alpha1.Feature{},
			managed:  false,
			msg:      "no feature is added and node is not managed",
		},
		{
			features: []rcV1alpha1.Feature{
				{
					Name:  rcV1alpha1.SecurityGroupsForPods,
					Value: "",
				},
			},
			managed: true,
			msg:     "no SGP feature is added and node is not managed",
		},
		{
			features: []rcV1alpha1.Feature{
				{
					Name:  rcV1alpha1.CustomNetworking,
					Value: "default",
				},
			},
			managed: false,
			msg:     "SGP feature is added and node is managed",
		},
	}

	for _, test := range testCases {
		t.Run(test.msg, func(t *testing.T) {
			mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
				Spec: rcV1alpha1.CNINodeSpec{
					Features: test.features,
				},
			}, nil).Times(1)
			managed, err := mock.Manager.trunkEnabledInCNINode(v1Node)
			assert.NoError(t, err)
			assert.Equal(t, test.managed, managed)
		})
	}
}
