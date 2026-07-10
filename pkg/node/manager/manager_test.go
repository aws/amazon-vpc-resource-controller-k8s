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
	"sync"
	"sync/atomic"
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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	asyncWorker "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
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
		actualJob.node.IsManaged() == m.expected.node.IsManaged()
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
			worker:          mockAsyncWorker,
			resourceManager: mockResourceManager,
			conditions:      mockConditions,
			clusterName:     mockClusterName,
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
		op:       Init,
		nodeName: nodeName,
		node:     managedNode,
	}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil).Times(2)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(expectedJob)))
	mock.MockK8sAPI.EXPECT().CreateCNINode(v1Node, mockClusterName).Return(nil).Times(0)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{}, nil).Times(2)

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
}

func Test_AddNode_CNINode_Not_Existing(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	expectedJob := AsyncOperationJob{
		op:       Init,
		nodeName: nodeName,
		node:     managedNode,
	}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil).Times(2)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(expectedJob)))
	mock.MockK8sAPI.EXPECT().CreateCNINode(v1Node, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(
		&rcV1alpha1.CNINode{}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test")).
		Times(2)

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
}

func Test_AddNode_UnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	nodeWithoutLabel := v1Node.DeepCopy()
	nodeWithoutLabel.Labels = map[string]string{}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithoutLabel, nil).Times(2)
	mock.MockK8sAPI.EXPECT().CreateCNINode(nodeWithoutLabel, mock.Manager.clusterName).Return(nil).Times(1)
	mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeWithoutLabel.Name}).Return(
		&rcV1alpha1.CNINode{}, apierrors.NewNotFound(schema.GroupResource{Group: "vpcresources.k8s.aws", Resource: "1"}, "test")).
		Times(1) // unmanaged node won't check custom networking subnets and call GetCNINode only once

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], unManagedNode))
}

func Test_AddNode_AlreadyAdded(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{nodeName: unManagedNode})

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)

	err := mock.Manager.AddNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], unManagedNode))
}

func Test_AddNode_CustomNetworking_CNINode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		op:       Init,
		nodeName: nodeName,
		node:     managedNode,
	}

	nodeWithENIConfig := v1Node.DeepCopy()

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil).Times(2)
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
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
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
		op:       Init,
		nodeName: nodeName,
		node:     managedNode,
	}

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil).Times(2)
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
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
}

// Test adding node when custom networking is enabled but incorrect ENIConfig is defined; it should succeed
// TODO: combine with other Test_AddNode_CustomNetworking tests
func Test_AddNode_CustomNetworking_Incorrect_ENIConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		op:       Init,
		nodeName: nodeName,
		node:     managedNode,
	}

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(nodeWithENIConfig, nil).Times(2)
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
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))

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

	mock := NewMock(ctrl, map[string]node.Node{nodeName: managedNode})

	job := AsyncOperationJob{
		op:       Update,
		nodeName: nodeName,
		node:     managedNode,
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
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
}

func Test_UpdateNode_UnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{v1Node.Name: unManagedNode})

	k8sNode := v1Node.DeepCopy()
	k8sNode.Labels = map[string]string{}

	mock.MockK8sAPI.EXPECT().GetNode(v1Node.Name).Return(k8sNode, nil)

	err := mock.Manager.UpdateNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], unManagedNode))
}

func Test_UpdateNode_ManagedToUnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{nodeName: managedNode})

	job := AsyncOperationJob{
		op:       Delete,
		nodeName: nodeName,
		node:     managedNode, // should pass the older cached value, instead of new node
	}

	updatedNode := v1Node.DeepCopy()
	updatedNode.Labels = map[string]string{}

	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(updatedNode, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))

	err := mock.Manager.UpdateNode(nodeName)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], unManagedNode))
}

func Test_UpdateNode_UnManagedToManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{v1Node.Name: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:       Init,
		nodeName: v1Node.Name,
		node:     managedNode,
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
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
}

func Test_UpdateNode_UnManagedToManaged_WithENIConfig_NodeLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{v1Node.Name: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:       Init,
		nodeName: v1Node.Name,
		node:     managedNode,
	}

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mock.MockK8sAPI.EXPECT().GetNode(v1Node.Name).Return(nodeWithENIConfig, nil)
	mock.MockK8sAPI.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil)
	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))

	err := mock.Manager.UpdateNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
}

func Test_UpdateNode_UnManagedToManaged_WithENIConfig_CNINode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{v1Node.Name: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:       Init,
		nodeName: v1Node.Name,
		node:     managedNode,
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
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
}

// Test_SubmittedAt_StampedForInitJobsOnly verifies node_onboarding_latency
// coverage: an UnManagedToManaged transition submits a real Init job and must
// stamp submittedAt (the observe sites are guarded by !submittedAt.IsZero()),
// while non-Init ops (Update/Delete) keep the zero value per
// AsyncOperationJob's documented invariant.
func Test_SubmittedAt_StampedForInitJobsOnly(t *testing.T) {
	t.Run("UnManagedToManaged Init job is stamped", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mock := NewMock(ctrl, map[string]node.Node{v1Node.Name: unManagedNode})

		var captured AsyncOperationJob
		mock.MockK8sAPI.EXPECT().GetNode(v1Node.Name).Return(v1Node, nil)
		mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
			Spec: rcV1alpha1.CNINodeSpec{
				Features: []rcV1alpha1.Feature{},
			},
		}, nil).Times(1)
		mock.MockWorker.EXPECT().SubmitJob(gomock.Any()).Do(func(job interface{}) {
			captured = job.(AsyncOperationJob)
		})

		err := mock.Manager.UpdateNode(v1Node.Name)
		assert.NoError(t, err)
		assert.Equal(t, Init, captured.op)
		assert.False(t, captured.submittedAt.IsZero(), "Init job must stamp submittedAt so node_onboarding_latency is observed")
	})

	t.Run("Update job is not stamped", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mock := NewMock(ctrl, map[string]node.Node{nodeName: managedNode})

		var captured AsyncOperationJob
		mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)
		mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: v1Node.Name}).Return(&rcV1alpha1.CNINode{
			Spec: rcV1alpha1.CNINodeSpec{
				Features: []rcV1alpha1.Feature{},
			},
		}, nil).Times(1)
		mock.MockWorker.EXPECT().SubmitJob(gomock.Any()).Do(func(job interface{}) {
			captured = job.(AsyncOperationJob)
		})

		err := mock.Manager.UpdateNode(nodeName)
		assert.NoError(t, err)
		assert.Equal(t, Update, captured.op)
		assert.True(t, captured.submittedAt.IsZero(), "non-Init Update job must keep submittedAt zero")
	})

	t.Run("Delete job is not stamped", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mock := NewMock(ctrl, map[string]node.Node{v1Node.Name: managedNode})

		var captured AsyncOperationJob
		mock.MockWorker.EXPECT().SubmitJob(gomock.Any()).Do(func(job interface{}) {
			captured = job.(AsyncOperationJob)
		})

		err := mock.Manager.DeleteNode(v1Node.Name)
		assert.NoError(t, err)
		assert.Equal(t, Delete, captured.op)
		assert.True(t, captured.submittedAt.IsZero(), "non-Init Delete job must keep submittedAt zero")
	})
}

func Test_DeleteNode_Managed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithManagedNode := map[string]node.Node{v1Node.Name: managedNode}

	mock := NewMock(ctrl, dataStoreWithManagedNode)

	job := AsyncOperationJob{
		op:       Delete,
		nodeName: v1Node.Name,
		node:     managedNode,
	}

	mock.MockWorker.EXPECT().SubmitJob(gomock.All(NewAsyncOperationMatcher(job)))

	err := mock.Manager.DeleteNode(v1Node.Name)
	assert.NoError(t, err)
	assert.NotContains(t, mock.Manager.dataStore, nodeName)
}

func Test_DeleteNode_UnManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	dataStoreWithUnManagedNode := map[string]node.Node{v1Node.Name: unManagedNode}

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

	mock := NewMock(ctrl, map[string]node.Node{nodeName: managedNode})

	job := AsyncOperationJob{
		node:     mock.MockNode,
		nodeName: nodeName,
	}

	job.op = Init

	mock.MockK8sAPI.EXPECT().AddLabelToManageNode(v1Node, config.HasTrunkAttachedLabel, config.BooleanTrue).Return(true, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(v1Node, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", mock.Manager.controllerVersion), v1.EventTypeNormal).Times(1)
	mock.MockNode.EXPECT().InitResources(mock.MockResourceManager).Return(nil)
	mock.MockNode.EXPECT().UpdateResources(mock.MockResourceManager).Return(nil)
	_, err := mock.Manager.performAsyncOperation(job)
	assert.Contains(t, mock.Manager.dataStore, nodeName)
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

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		node:     mock.MockNode,
		nodeName: nodeName,
		op:       Init,
	}
	mock.Manager.dataStore[nodeName] = mock.MockNode

	mock.MockNode.EXPECT().InitResources(mock.MockResourceManager).Return(&node.ErrInitResources{})
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(v1Node, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", mock.Manager.controllerVersion), v1.EventTypeNormal).Times(1)

	_, err := mock.Manager.performAsyncOperation(job)
	assert.NotContains(t, mock.Manager.dataStore, nodeName) // It should be cleared from cache
	assert.NoError(t, err)
}

func Test_performAsyncOperation_fail_pausingHealthCheck(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	job := AsyncOperationJob{
		node:     mock.MockNode,
		nodeName: nodeName,
		op:       Init,
	}
	mock.Manager.dataStore[nodeName] = mock.MockNode

	mock.MockNode.EXPECT().InitResources(mock.MockResourceManager).Return(&node.ErrInitResources{
		Err: errors.New("RequestLimitExceeded: Request limit exceeded.\n\tstatus code: 503, request id: 123-123-123-123-123"),
	}).Times(2)
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(v1Node, nil).Times(2)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(v1Node, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", mock.Manager.controllerVersion), v1.EventTypeNormal).Times(2)

	_, err := mock.Manager.performAsyncOperation(job)
	time.Sleep(time.Millisecond * 100)
	assert.True(t, mock.Manager.SkipHealthCheck())
	assert.NotContains(t, mock.Manager.dataStore, nodeName) // It should be cleared from cache
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
	dataStoreWithUnManagedNode := map[string]node.Node{windowsNode.Name: unManagedNode}

	mock := NewMock(ctrl, dataStoreWithUnManagedNode)

	job := AsyncOperationJob{
		op:       Init,
		nodeName: windowsNode.Name,
		node:     managedNode,
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
	assert.Contains(t, mock.Manager.dataStore, nodeName)
	assert.True(t, AreNodesEqual(mock.Manager.dataStore[nodeName], managedNode))
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

// --------------------------------------------------------------------------
// Concurrency-focused tests and benchmark for the node manager's
// AddNode/UpdateNode lock behavior. These exercise the narrowed critical
// section: the K8s reads run outside the manager lock, which is taken only for
// the dataStore map mutation using double-checked / re-checked membership.
//
// Run with -race to catch data races, and with -tags deadlock to swap in
// go-deadlock's instrumented RWMutex (see rwmutex_deadlock.go) so lock-order
// inversions are detected at runtime:
//
//	go test -race ./pkg/node/manager/...
//	go test -tags deadlock -race ./pkg/node/manager/...
// --------------------------------------------------------------------------

// Benchmark tuning knobs. These are intentionally fixed (not randomized) so the
// before/after comparison is deterministic.
const (
	// benchNumNodes is how many distinct nodes are added per benchmark op.
	benchNumNodes = 120
	// benchConcurrency matches the default --max-node-reconcile so the benchmark
	// models the real reconciler goroutine fan-in onto the manager lock.
	benchConcurrency = 10
	// benchCallLatency is the simulated per-call latency for each K8s API call
	// that AddNode performs while (currently) holding the manager lock. Keeping
	// this non-zero is what makes the lock-serialization cost observable.
	benchCallLatency = 3 * time.Millisecond
)

// benchK8s is a hand-written fake K8sWrapper that injects a fixed latency into
// the calls AddNode makes. We deliberately avoid gomock here: gomock's Controller
// serializes every mocked call under a single global mutex, which would mask the
// exact manager-lock contention this benchmark is trying to measure.
//
// The embedded k8s.K8sWrapper interface is nil; only the methods AddNode actually
// calls are overridden. Any other method would panic if called (it won't be).
type benchK8s struct {
	k8s.K8sWrapper
	delay time.Duration
}

func (f *benchK8s) GetNode(nodeName string) (*v1.Node, error) {
	time.Sleep(f.delay)
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				config.NodeLabelOS:           config.OSLinux,
				config.HasTrunkAttachedLabel: "true",
			},
		},
		Spec: v1.NodeSpec{ProviderID: providerId},
	}, nil
}

func (f *benchK8s) GetCNINode(types.NamespacedName) (*rcV1alpha1.CNINode, error) {
	time.Sleep(f.delay)
	// Returning an existing (empty) CNINode models the leader-transition case
	// where CNINodes already exist, so CreateCNINode is never called. We
	// therefore do not implement CreateCNINode here (the embedded nil interface
	// satisfies the type), which also keeps this fake decoupled from that
	// method's signature.
	return &rcV1alpha1.CNINode{}, nil
}

// benchWorker is a no-op Worker. SubmitJob must do no real work (and take no
// shared lock) so that it does not become an artificial bottleneck.
type benchWorker struct {
	asyncWorker.Worker
}

func (w *benchWorker) SubmitJob(interface{}) {}

func newBenchManager(delay time.Duration) *manager {
	return &manager{
		Log:       logr.Discard(),
		dataStore: make(map[string]node.Node),
		wrapper: api.Wrapper{
			K8sAPI: &benchK8s{delay: delay},
			EC2API: nil,
		},
		worker:      &benchWorker{},
		clusterName: mockClusterName,
	}
}

// BenchmarkAddNode_Concurrent measures the wall-clock time for benchConcurrency
// goroutines to add benchNumNodes distinct nodes through the manager. It calls
// only the public AddNode, so the same benchmark runs unchanged against both the
// current (whole-function-locked) implementation and the refactored one, making
// it a fair before/after baseline.
//
// Run with:
//
//	go test -bench BenchmarkAddNode_Concurrent -benchtime 1x -count 10 ./pkg/node/manager/...
func BenchmarkAddNode_Concurrent(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m := newBenchManager(benchCallLatency)
		names := make(chan string, benchNumNodes)
		for j := 0; j < benchNumNodes; j++ {
			names <- fmt.Sprintf("bench-node-%d", j)
		}
		close(names)

		var wg sync.WaitGroup
		b.StartTimer()
		for w := 0; w < benchConcurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for name := range names {
					if err := m.AddNode(name); err != nil {
						b.Errorf("AddNode(%s) returned error: %v", name, err)
					}
				}
			}()
		}
		wg.Wait()

		b.StopTimer()
		if len(m.dataStore) != benchNumNodes {
			b.Fatalf("expected %d nodes in dataStore, got %d", benchNumNodes, len(m.dataStore))
		}
		b.StartTimer()
	}
}

// countingWorker is a Worker that atomically counts SubmitJob calls. It takes no
// shared lock so it does not perturb concurrency behavior under the race detector.
type countingWorker struct {
	asyncWorker.Worker
	submitted int32
}

func (w *countingWorker) SubmitJob(interface{}) { atomic.AddInt32(&w.submitted, 1) }

func (w *countingWorker) count() int32 { return atomic.LoadInt32(&w.submitted) }

func newConcurrencyManager(worker asyncWorker.Worker, existing map[string]node.Node) *manager {
	if existing == nil {
		existing = make(map[string]node.Node)
	}
	return &manager{
		Log:       logr.Discard(),
		dataStore: existing,
		wrapper: api.Wrapper{
			K8sAPI: &benchK8s{}, // zero latency; returns a managed (trunk) node
			EC2API: nil,
		},
		worker:      worker,
		clusterName: mockClusterName,
	}
}

// TestAddNode_ConcurrentSameNode_SingleJob asserts that when many goroutines race
// to add the SAME node, the in-lock double-check lets exactly one win: only one
// async job is submitted and the node appears once in the datastore.
//
// Run with -race to also verify there is no concurrent map access.
func TestAddNode_ConcurrentSameNode_SingleJob(t *testing.T) {
	const concurrency = 50
	worker := &countingWorker{}
	m := newConcurrencyManager(worker, nil)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximize contention
			if err := m.AddNode("race-node"); err != nil {
				t.Errorf("AddNode returned error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), worker.count(), "exactly one job should be submitted for a single node")
	assert.Len(t, m.dataStore, 1, "node should appear exactly once in the datastore")
	assert.Contains(t, m.dataStore, "race-node")
}

// TestAddNode_ConcurrentDistinctNodes_AllAdded asserts that concurrently adding
// many distinct nodes results in every node being added exactly once.
func TestAddNode_ConcurrentDistinctNodes_AllAdded(t *testing.T) {
	const (
		numNodes    = 300
		concurrency = 10
	)
	worker := &countingWorker{}
	m := newConcurrencyManager(worker, nil)

	names := make(chan string, numNodes)
	for j := 0; j < numNodes; j++ {
		names <- fmt.Sprintf("node-%d", j)
	}
	close(names)

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range names {
				if err := m.AddNode(name); err != nil {
					t.Errorf("AddNode(%s) returned error: %v", name, err)
				}
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(numNodes), worker.count(), "each distinct node should submit exactly one job")
	assert.Len(t, m.dataStore, numNodes)
}

// TestNode_ConcurrentAddUpdateDelete_NoRace hammers Add/Update/Delete on a small
// set of node names from many goroutines. Its purpose is to exercise the
// compare-and-swap path in UpdateNode and the map mutations under the race
// detector; it asserts the manager does not panic and the datastore stays
// internally consistent.
func TestNode_ConcurrentAddUpdateDelete_NoRace(t *testing.T) {
	const (
		concurrency   = 30
		opsPerRoutine = 100
		distinctNodes = 5
	)
	worker := &countingWorker{}
	m := newConcurrencyManager(worker, nil)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for op := 0; op < opsPerRoutine; op++ {
				name := fmt.Sprintf("node-%d", (seed+op)%distinctNodes)
				switch op % 3 {
				case 0:
					_ = m.AddNode(name)
				case 1:
					_ = m.UpdateNode(name)
				case 2:
					_ = m.DeleteNode(name)
				}
			}
		}(i)
	}
	wg.Wait()

	// No strict count assertion (operations interleave nondeterministically); the
	// real check is the race detector plus the absence of a panic. Sanity-check
	// that the datastore never exceeds the number of distinct node names.
	assert.LessOrEqual(t, len(m.dataStore), distinctNodes)
}

// Test_ConcurrentGetNode_NoRace is a lightweight reader-side race check: many
// concurrent GetNode reads against a pre-populated datastore while writers
// add/delete. Ensures the RLock-only read path in the narrowed lock is race-free.
// The other concurrency tests never exercise GetNode concurrently, so this keeps
// distinct reader-path coverage.
func Test_ConcurrentGetNode_NoRace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{nodeName: managedNode})

	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(v1Node, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(&rcV1alpha1.CNINode{}, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().CreateCNINode(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mock.MockWorker.EXPECT().SubmitJob(gomock.Any()).AnyTimes()

	const readers = 24
	const writers = 4
	var wg sync.WaitGroup
	wg.Add(readers + writers)

	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_, _ = mock.Manager.GetNode(nodeName)
			}
		}()
	}
	for w := 0; w < writers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				if i%2 == 0 {
					_ = mock.Manager.AddNode(nodeName)
				} else {
					_ = mock.Manager.DeleteNode(nodeName)
				}
			}
		}()
	}
	wg.Wait()
}

// gateK8s is a fake K8sWrapper whose GetNode blocks at a barrier until `want`
// calls are simultaneously in-flight, then releases them together. It records the
// maximum number of concurrently in-flight GetNode calls observed.
//
// This is the mechanism that turns the qualitative "serial -> parallel" change
// into a deterministic pass/fail assertion: with a whole-function lock, only one
// goroutine can ever be inside AddNode (hence inside GetNode) at a time, so the
// barrier never fills and the test times out.
type gateK8s struct {
	k8s.K8sWrapper // embedded interface (nil); only GetNode/GetCNINode are real

	want        int
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	arrived     chan struct{} // closed once `want` calls are concurrently in-flight
	release     chan struct{} // closed by the test to let blocked calls proceed
	arriveOnce  sync.Once
}

func (g *gateK8s) GetNode(nodeName string) (*v1.Node, error) {
	g.mu.Lock()
	g.inFlight++
	if g.inFlight > g.maxInFlight {
		g.maxInFlight = g.inFlight
	}
	reached := g.inFlight >= g.want
	g.mu.Unlock()

	if reached {
		g.arriveOnce.Do(func() { close(g.arrived) })
	}
	<-g.release // block until the test releases everyone

	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()

	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				config.NodeLabelOS:           config.OSLinux,
				config.HasTrunkAttachedLabel: "true",
			},
		},
		Spec: v1.NodeSpec{ProviderID: providerId},
	}, nil
}

func (g *gateK8s) GetCNINode(types.NamespacedName) (*rcV1alpha1.CNINode, error) {
	return &rcV1alpha1.CNINode{}, nil
}

// TestAddNode_LockFreePathRunsInParallel asserts that AddNode's K8s work runs
// outside the manager lock: it requires all `concurrency` GetNode calls to be
// in-flight simultaneously. On the previous whole-function-locked implementation
// this is impossible and the test fails via timeout, making it a regression guard
// against re-widening the critical section.
func TestAddNode_LockFreePathRunsInParallel(t *testing.T) {
	const concurrency = 10
	g := &gateK8s{
		want:    concurrency,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
	m := &manager{
		Log:       logr.Discard(),
		dataStore: make(map[string]node.Node),
		wrapper: api.Wrapper{
			K8sAPI: g,
			EC2API: nil,
		},
		worker:      &countingWorker{},
		clusterName: mockClusterName,
	}

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := m.AddNode(fmt.Sprintf("node-%d", i)); err != nil {
				t.Errorf("AddNode returned error: %v", err)
			}
		}(i)
	}

	select {
	case <-g.arrived:
		// All `concurrency` goroutines are concurrently inside the lock-free
		// GetNode call: the critical section is no longer serializing them.
	case <-time.After(5 * time.Second):
		close(g.release) // unblock so the goroutines can finish and not leak
		wg.Wait()
		t.Fatalf("AddNode did not run %d calls concurrently; the lock-free path appears serialized", concurrency)
	}

	close(g.release)
	wg.Wait()

	assert.Equal(t, concurrency, g.maxInFlight, "all goroutines should be inside the lock-free section simultaneously")
	assert.Len(t, m.dataStore, concurrency)
}
