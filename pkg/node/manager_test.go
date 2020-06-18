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

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/stretchr/testify/assert"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	instanceID = "i-01234567890abcdef"
	providerId = "aws:///us-west-2c/" + instanceID
	v1Node = &v1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:   "ip-192-168-55-73.us-west-2.compute.internal",
			Labels: map[string]string{config.NodeLabelOS: config.OSLinux},
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

// Test_GetNewManager tests if new node manager is not nil
func Test_GetNewManager(t *testing.T) {
	manager := NewNodeManager(nil)
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

// Test_addOrUpdateNode_existing_node tests if the node already exists, then the operation update resource is returned
func Test_addOrUpdateNode_existing_node(t *testing.T) {
	manager := getMockManager()

	// Add node twice
	manager.addOrUpdateNode(v1Node)
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
	v1NodeCopy.Status.Capacity[config.ResourceNamePodENI] = *resource.NewQuantity(0, resource.DecimalExponent)

	postUnlockOperation, err := manager.addOrUpdateNode(v1NodeCopy)
	assert.NoError(t, err)
	assert.Nil(t, postUnlockOperation)

	node, managed := manager.GetNode(v1NodeCopy.Name)
	assert.Nil(t, node)
	assert.False(t, managed)
}

// Test_addOrUpdateNode_statusChanged tests if the status of a managed node changes it is removed from the list
// of managed node and requested to delete the resources
func Test_addOrUpdateNode_statusChanged(t *testing.T)  {
	manager := getMockManager()

	// Add node to list of managed node
	_, _ = manager.addOrUpdateNode(v1Node)
	node, managed := manager.GetNode(v1Node.Name)
	assert.True(t, managed)

	// Set the capacity for the node to 0
	v1NodeCopy := v1Node.DeepCopy()
	v1NodeCopy.Status.Capacity[config.ResourceNamePodENI] = *resource.NewQuantity(0, resource.DecimalExponent)
	manager.addOrUpdateNode(v1Node)

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
	postUnlockOperation, err := manager.deleteNode(v1Node)
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
	postUnlockOperation, err := manager.deleteNode(v1Node)
	assert.NoError(t, err)
	assert.Nil(t, postUnlockOperation)
}

// Test_isPodENICapacitySet test if the pod-eni capacity then true is returned
func Test_isPodENICapacitySet(t *testing.T) {
	isSet := isPodENICapacitySet(v1Node)
	assert.True(t, isSet)
}

// Test_isPodENICapacitySet_Neg tests if the pod-eni capacity is not set then false is returned
func Test_isPodENICapacitySet_Neg(t *testing.T) {
	v1NodeCopy := v1Node.DeepCopy()
	v1NodeCopy.Status.Capacity[config.ResourceNamePodENI] = *resource.NewQuantity(0, resource.DecimalExponent)
	isSet := isPodENICapacitySet(v1NodeCopy)
	assert.False(t, isSet)
}

// Test_isManagedLabelSet tests if the managed label is set then true is returned
func Test_isManagedLabelSet(t *testing.T) {
	v1NodeCopy := v1Node.DeepCopy()
	v1NodeCopy.Labels[config.VPCManagerLabel] = config.VPCManagedBy
	isSet := isManagedLabelSet(v1NodeCopy)
	assert.True(t, isSet)
}

// Test_isManagedLabelSet_neg tests if the managed label is unset then false is returned
func Test_isManagedLabelSet_neg(t *testing.T) {
	isSet := isManagedLabelSet(v1Node)
	assert.False(t, isSet)
}

// Test_getNodeInstanceID test if the correct node id is retrieved from the provider id
func Test_getNodeInstanceID(t *testing.T) {
	id := getNodeInstanceID(v1Node)
	assert.Equal(t, instanceID, id,)
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

	postUnlockFunc := func() error  {
		return nil
	}

	err := manager.performPostUnlockOperation(v1Node.Name, postUnlockFunc)
	assert.Nil(t, err)
}

// Test_performPostUnlockOperation_intiFails tests that the node in the managed list is removed after the post unlock
// operation fails
func Test_performPostUnlockOperation_intiFails(t *testing.T) {
	manager := getMockManager()
	manager.addOrUpdateNode(v1Node)

	_, managed := manager.GetNode(v1Node.Name)
	assert.True(t, managed)

	postUnlockFunc := func() error  {
		return ErrInitResources
	}

	manager.performPostUnlockOperation(v1Node.Name, postUnlockFunc)

	_, managed = manager.GetNode(v1Node.Name)
	assert.False(t, managed)
}
