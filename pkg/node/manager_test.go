/*


 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package node

import (
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
)

var (
	instanceID     = "i-01234567890abcdef"
	providerId     = "aws:///us-west-2c/" + instanceID
	eniConfigName  = "eni-config-name"
	subnetID       = "subnet-id"
	customSubnetID = "custom-subnet-id"

	subnetCidrBlock = "192.168.0.0/16"
	subnet          = &ec2.Subnet{CidrBlock: &subnetCidrBlock}

	eniConfig = &v1alpha1.ENIConfig{
		Spec: v1alpha1.ENIConfigSpec{
			Subnet: subnetID,
		},
	}

	v1Node = &v1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "ip-192-168-55-73.us-west-2.compute.internal",
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
)

// getMockManager returns the mock manager object
func getMockManager() manager {
	return manager{
		dataStore: make(map[string]Node),
		Log:       zap.New(zap.UseDevMode(true)).WithName("node manager"),
	}
}

// getMockManagerWithK8sWrapper returns the mock manager with mock K8s object
func getMockManagerWithK8sWrapper(ctrl *gomock.Controller) (manager, *mock_k8s.MockK8sWrapper, *mock_api.MockEC2APIHelper) {
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockeEC2APIHelper := mock_api.NewMockEC2APIHelper(ctrl)
	return manager{
		dataStore:    make(map[string]Node),
		Log:          zap.New(zap.UseDevMode(true)).WithName("node manager"),
		k8sWrapper:   mockK8sWrapper,
		ec2APIHelper: mockeEC2APIHelper,
	}, mockK8sWrapper, mockeEC2APIHelper
}

// Test_GetNewManager tests if new node manager is not nil
func Test_GetNewManager(t *testing.T) {
	manager := NewNodeManager(nil, nil, nil, nil)
	assert.NotNil(t, manager)
}

// Test_addOrUpdateNode_new_node tests if a node that doesn't exist in managed list is added and a request
// to perform init resource is returned.
func Test_addOrUpdateNode_new_node(t *testing.T) {
	manager := getMockManager()

	postUnlockOperation, err := manager.addOrUpdateNode(v1Node)
	node, _ := manager.GetNode(v1Node.Name)

	assert.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(node.InitResources).Pointer(), reflect.ValueOf(postUnlockOperation).Pointer())
}

// Test_addOrUpdateNode_new_node_custom_networking tests if node has custom networking label then the eni config is
// loaded from K8s Wrapper
func Test_addOrUpdateNode_new_node_custom_networking(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockK8sWrapper, _ := getMockManagerWithK8sWrapper(ctrl)

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mockK8sWrapper.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil)
	postUnlockOperation, err := manager.addOrUpdateNode(nodeWithENIConfig)
	node, _ := manager.GetNode(v1Node.Name)

	assert.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(node.InitResources).Pointer(), reflect.ValueOf(postUnlockOperation).Pointer())
}

// Test_addOrUpdateNode_new_node_custom_networking_eniConfig_notfound tests that error is returned if the eni config
// set on the node's custom networking label is not found
func Test_addOrUpdateNode_new_node_custom_networking_eniConfig_notfound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockK8sWrapper, _ := getMockManagerWithK8sWrapper(ctrl)

	nodeWithENIConfig := v1Node.DeepCopy()
	nodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName
	nodeWithENIConfig.Labels[eniConfig.Spec.Subnet] = subnetID

	mockK8sWrapper.EXPECT().GetENIConfig(eniConfigName).Return(nil, mockError)
	_, err := manager.addOrUpdateNode(nodeWithENIConfig)
	_, isPresent := manager.GetNode(v1Node.Name)

	assert.Error(t, mockError, err)
	assert.False(t, isPresent)
}

// Test_addOrUpdateNode_new_node_custom_networking tests if node has custom networking label then the eni config is
// loaded from K8s Wrapper
func Test_addOrUpdateNode_existing_node_custom_networking(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	manager, mockK8sWrapper, _ := getMockManagerWithK8sWrapper(ctrl)

	// Add node once
	_, err := manager.addOrUpdateNode(v1Node)
	assert.NoError(t, err)

	// Update the node label key and value pair
	updatedNodeWithENIConfig := v1Node.DeepCopy()
	updatedNodeWithENIConfig.Labels[config.CustomNetworkingLabel] = eniConfigName

	mockK8sWrapper.EXPECT().GetENIConfig(eniConfigName).Return(eniConfig, nil)
	// Update the node, expect to get the eni config
	postUnlockOperation, err := manager.addOrUpdateNode(updatedNodeWithENIConfig)
	node, _ := manager.GetNode(v1Node.Name)

	assert.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(node.UpdateResources).Pointer(), reflect.ValueOf(postUnlockOperation).Pointer())
}

// Test_addOrUpdateNode_existing_node tests if the node already exists, then the operation update resource is returned
func Test_addOrUpdateNode_existing_node(t *testing.T) {
	manager := getMockManager()

	// Add node twice
	_, err := manager.addOrUpdateNode(v1Node)
	assert.NoError(t, err)

	postUnlockOperation, err := manager.addOrUpdateNode(v1Node)
	node, _ := manager.GetNode(v1Node.Name)

	assert.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(node.UpdateResources).Pointer(), reflect.ValueOf(postUnlockOperation).Pointer())
}

// Test_addOrUpdateNode_notSelected verifies if the node is not selected for management it is not added to the
// list of managed node
func Test_addOrUpdateNode_notSelected(t *testing.T) {
	manager := getMockManager()

	v1NodeCopy := v1Node.DeepCopy()
	delete(v1NodeCopy.Labels, config.HasTrunkAttachedLabel)

	postUnlockOperation, err := manager.addOrUpdateNode(v1NodeCopy)
	assert.NoError(t, err)
	assert.Nil(t, postUnlockOperation)

	node, managed := manager.GetNode(v1NodeCopy.Name)
	assert.Nil(t, node)
	assert.False(t, managed)
}

// Test_addOrUpdateNode_statusChanged tests if the status of a managed node changes it is removed from the list
// of managed node and requested to delete the resources
func Test_addOrUpdateNode_statusChanged(t *testing.T) {
	manager := getMockManager()

	// Add node to list of managed node
	_, _ = manager.addOrUpdateNode(v1Node)
	node, managed := manager.GetNode(v1Node.Name)
	assert.True(t, managed)

	// Set the capacity for the node to 0
	v1NodeCopy := v1Node.DeepCopy()
	delete(v1NodeCopy.Labels, config.HasTrunkAttachedLabel)
	_, err := manager.addOrUpdateNode(v1Node)
	assert.NoError(t, err)

	postUnlockOperation, err := manager.addOrUpdateNode(v1NodeCopy)

	assert.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(node.DeleteResources).Pointer(), reflect.ValueOf(postUnlockOperation).Pointer())
}

// Test_deleteNode_notExists tests if the node that is in list of managed node is deleted on calling delteNode
func Test_deleteNode(t *testing.T) {
	manager := getMockManager()

	// Add node to list of managed node
	_, _ = manager.addOrUpdateNode(v1Node)
	node, managed := manager.GetNode(v1Node.Name)
	assert.True(t, managed)

	// Delete node
	postUnlockOperation, err := manager.deleteNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(node.DeleteResources).Pointer(), reflect.ValueOf(postUnlockOperation).Pointer())

	// Verify deleted from managed list
	node, managed = manager.GetNode(v1Node.Name)
	assert.False(t, managed)
	assert.Nil(t, node)
}

// Test_deleteNode_notExists tests that deletion of node that doesn't exist does't throw an error
func Test_deleteNode_notExists(t *testing.T) {
	manager := getMockManager()

	// Delete node
	postUnlockOperation, err := manager.deleteNode(v1Node.Name)
	assert.NoError(t, err)
	assert.Nil(t, postUnlockOperation)
}

// Test_isPodENICapacitySet test if the pod-eni capacity then true is returned
func Test_isPodENICapacitySet(t *testing.T) {

	isSet := canAttachTrunk(v1Node)
	assert.True(t, isSet)
}

// Test_isPodENICapacitySet_Neg tests if the pod-eni capacity is not set then false is returned
func Test_isPodENICapacitySet_Neg(t *testing.T) {
	v1NodeCopy := v1Node.DeepCopy()
	delete(v1NodeCopy.Labels, config.HasTrunkAttachedLabel)
	isSet := canAttachTrunk(v1NodeCopy)
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
	id := getNodeInstanceID(v1Node)
	assert.Equal(t, instanceID, id)
}

// Test_getNodeOS tests that is OS label is set then the correct os is returned
func Test_getNodeOS(t *testing.T) {
	os := getNodeOS(v1Node)
	assert.Equal(t, config.OSLinux, os)
}

// Test_isSelectedForManagement tests if the either the capacity or the label is set true is returned
func Test_isSelectedForManagement(t *testing.T) {
	manager := getMockManager()

	isSelected := manager.isSelectedForManagement(v1Node)
	assert.True(t, isSelected)
}

// Test_performPostUnlockOperation doesn't return an error on executing the postUnlockOperation
func Test_performPostUnlockOperation(t *testing.T) {
	manager := getMockManager()

	postUnlockFunc := func(provider []provider.ResourceProvider, helper api.EC2APIHelper) error {
		return nil
	}

	err := manager.performPostUnlockOperation(v1Node.Name, postUnlockFunc)
	assert.Nil(t, err)
}

// Test_performPostUnlockOperation_intiFails tests that the node in the managed list is removed after the post unlock
// operation fails
func Test_performPostUnlockOperation_intiFails(t *testing.T) {
	manager := getMockManager()
	_, err := manager.addOrUpdateNode(v1Node)
	assert.NoError(t, err)

	_, managed := manager.GetNode(v1Node.Name)
	assert.True(t, managed)

	postUnlockFunc := func(provider []provider.ResourceProvider, helper api.EC2APIHelper) error {
		return ErrInitResources
	}

	err = manager.performPostUnlockOperation(v1Node.Name, postUnlockFunc)
	assert.NotNil(t, err)

	_, managed = manager.GetNode(v1Node.Name)
	assert.False(t, managed)
}
