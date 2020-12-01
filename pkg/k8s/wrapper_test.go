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

	mock_controller "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/controllers/custom"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakeClientSet "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	podName      = "name"
	podNamespace = "namespace"

	oldAnnotation = "old-annotation-key"

	newAnnotation      = "new-annotation"
	newAnnotationValue = "new-annotation-val"

	mockPod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   podNamespace,
			Annotations: map[string]string{oldAnnotation: oldAnnotation},
		},
		Spec:   v1.PodSpec{NodeName: mockNode.Name},
		Status: v1.PodStatus{},
	}

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
)

// getMockK8sWrapper returns the mock wrapper interface
func getMockK8sWrapperWithClient(ctrl *gomock.Controller) (K8sWrapper, client.Client,
	*mock_controller.MockController) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)

	client := fakeClient.NewFakeClientWithScheme(scheme, mockPod, mockNode)
	clientSet := fakeClientSet.NewSimpleClientset(mockPod, mockNode)
	mockController := mock_controller.NewMockController(ctrl)

	return NewK8sWrapper(client, clientSet.CoreV1(), mockController), client, mockController
}

func getFakeDataStore() cache.Store {
	indexer := map[string]cache.IndexFunc{}
	indexer[NodeNameSpec] = func(obj interface{}) (strings []string, err error) {
		return []string{obj.(*v1.Pod).Spec.NodeName}, nil
	}
	store := cache.NewIndexer(func(obj interface{}) (s string, err error) {
		pod := obj.(*v1.Pod)
		return types.NamespacedName{
			Namespace: pod.Namespace,
			Name:      pod.Name,
		}.String(), nil
	}, indexer)
	store.Add(mockPod)
	return store
}

// TestK8sWrapper_AnnotatePod tests that annotate pod doesn't throw error on adding a new annotation to pod
func TestK8sWrapper_AnnotatePod(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, k8sClient, mockController := getMockK8sWrapperWithClient(ctrl)

	mockController.EXPECT().GetDataStore().Return(getFakeDataStore())

	err := wrapper.AnnotatePod(podNamespace, podName, newAnnotation, newAnnotationValue)
	assert.NoError(t, err)

	// Validate the pod got the annotation
	updatedPod := &v1.Pod{}
	err = k8sClient.Get(context.Background(), types.NamespacedName{
		Namespace: podNamespace,
		Name:      podName,
	}, updatedPod)

	assert.NoError(t, err)
	assert.Equal(t, newAnnotationValue, updatedPod.Annotations[newAnnotation])
}

// TestK8sWrapper_AnnotatePod_PodNotExists tests that annotate pod fails if the pod doesn't exist
func TestK8sWrapper_AnnotatePod_PodNotExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, mockController := getMockK8sWrapperWithClient(ctrl)

	mockController.EXPECT().GetDataStore().Return(getFakeDataStore())
	err := wrapper.AnnotatePod(podNamespace, "non-existent-pod", newAnnotation, newAnnotationValue)
	assert.NotNil(t, err)
}

// TestK8sWrapper_GetPod gets the pod object form the wrapper api
func TestK8sWrapper_GetPod(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, mockController := getMockK8sWrapperWithClient(ctrl)
	testPod := mockPod.DeepCopy()

	mockController.EXPECT().GetDataStore().Return(getFakeDataStore())

	pod, err := wrapper.GetPod(podNamespace, podName)
	assert.NoError(t, err)
	assert.Equal(t, testPod.ObjectMeta, pod.ObjectMeta)
}

// TestK8sWrapper_GetPod_NotExists tests that error is returned when the pod doesn't exist
func TestK8sWrapper_GetPod_NotExists(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, mockController := getMockK8sWrapperWithClient(ctrl)

	mockController.EXPECT().GetDataStore().Return(getFakeDataStore())

	_, err := wrapper.GetPod(podNamespace, "not-exist")
	assert.NotNil(t, err)
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

	capacity, _ := mockNode.Status.Capacity[v1.ResourceName(existingResource)]
	assert.NoError(t, err)
	assert.Equal(t, existingResourceQuantity, capacity.Value())
}

// TestK8sWrapper_GetPodFromAPIServer tests if the pod is returned if it's stored with API server
func TestK8sWrapper_GetPodFromAPIServer(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, mockController := getMockK8sWrapperWithClient(ctrl)

	mockController.EXPECT().GetDataStore().Return(getFakeDataStore())

	pod, err := wrapper.GetPodFromAPIServer(podNamespace, podName)

	assert.NoError(t, err)
	assert.Equal(t, mockPod, pod)
}

// TestK8sWrapper_GetPodFromAPIServer_NoError tests that error is returned when the pod doesn't exist
func TestK8sWrapper_GetPodFromAPIServer_NoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, mockController := getMockK8sWrapperWithClient(ctrl)

	mockController.EXPECT().GetDataStore().Return(getFakeDataStore())

	_, err := wrapper.GetPodFromAPIServer(podNamespace, podName+"not-exists")

	assert.NotNil(t, err)
}

// TestK8sWrapper_ListPods tests that list pod returns the list of pods with the given node name in their node spec
// https://github.com/kubernetes/client-go/issues/326
func TestK8sWrapper_ListPods(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, mockController := getMockK8sWrapperWithClient(ctrl)

	mockController.EXPECT().GetDataStore().Return(getFakeDataStore())

	podList, err := wrapper.ListPods(nodeName)

	assert.NoError(t, err)
	assert.Equal(t, 1, len(podList.Items))
	assert.Equal(t, mockPod.UID, podList.Items[0].UID)
}
