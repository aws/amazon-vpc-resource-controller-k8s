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

package k8s

import (
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/controllers/custom"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	appV1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakeClientSet "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	nodeName         = "node-name"
	mockResourceName = config.ResourceNamePodENI

	existingResource         = "extended-resource"
	existingResourceQuantity = int64(5)
	mockNode                 = &v1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
		Spec: v1.NodeSpec{},
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourceName(existingResource): *resource.NewQuantity(existingResourceQuantity, resource.DecimalExponent),
			},
		},
	}
	mockDeployment = &appV1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.OldVPCControllerDeploymentName,
			Namespace: config.OldVPCControllerDeploymentNS,
		},
	}
)

// getMockK8sWrapper returns the mock wrapper interface
func getMockK8sWrapperWithClient(ctrl *gomock.Controller) (K8sWrapper, client.Client,
	*mock_custom.MockController) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = appV1.AddToScheme(scheme)

	client := fakeClient.NewFakeClientWithScheme(scheme, mockNode, mockDeployment)
	clientSet := fakeClientSet.NewSimpleClientset(mockNode)
	mockController := mock_custom.NewMockController(ctrl)

	return NewK8sWrapper(client, clientSet.CoreV1(), context.Background()), client, mockController
}

// TestK8sWrapper_AdvertiseCapacity tests that the capacity is advertised to the k8s node
func TestK8sWrapper_AdvertiseCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, k8sClient, _ := getMockK8sWrapperWithClient(ctrl)

	// Make node copy and ensure that the advertised capacity is 0
	testNode := mockNode.DeepCopy()
	quantity := testNode.Status.Capacity[v1.ResourceName(mockResourceName)]
	assert.True(t, quantity.IsZero())

	// Advertise capacity
	capacityToAdvertise := 10
	err := wrapper.AdvertiseCapacityIfNotSet(nodeName, mockResourceName, capacityToAdvertise)
	assert.NoError(t, err)

	// Get the node from the client and verify the capacity is set
	node := &v1.Node{}
	err = k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, node)

	// Verify no error and the capacity is set to the desired capacity
	assert.NoError(t, err)
	newQuantity := node.Status.Capacity[v1.ResourceName(mockResourceName)]
	assert.Equal(t, int64(capacityToAdvertise), newQuantity.Value())
}

// TestK8sWrapper_AdvertiseCapacity_Err tests that error is thrown when an error is encountered on the advertise resource
func TestK8sWrapper_AdvertiseCapacity_Err(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl)

	deletedNodeName := "deleted-node"
	err := wrapper.AdvertiseCapacityIfNotSet(deletedNodeName, mockResourceName, 10)
	assert.NotNil(t, err)
}

// TestK8sWrapper_AdvertiseCapacity_AlreadySet tests that if capacity of node is already set no error is thrown.
func TestK8sWrapper_AdvertiseCapacity_AlreadySet(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl)
	err := wrapper.AdvertiseCapacityIfNotSet(nodeName, existingResource, 5)

	capacity := mockNode.Status.Capacity[v1.ResourceName(existingResource)]
	assert.NoError(t, err)
	assert.Equal(t, existingResourceQuantity, capacity.Value())
}

func TestK8sWrapper_GetDeployment(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl)

	deployment, err := wrapper.GetDeployment(config.OldVPCControllerDeploymentNS,
		config.OldVPCControllerDeploymentName)
	assert.NoError(t, err)
	assert.Equal(t, deployment.ObjectMeta, mockDeployment.ObjectMeta)
}

func TestK8sWrapper_GetDeployment_Err(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl)

	_, err := wrapper.GetDeployment("default",
		config.OldVPCControllerDeploymentName)
	assert.Error(t, err)
}
