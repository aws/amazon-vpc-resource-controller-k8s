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

package k8s

import (
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	podName      = "name"
	podNamespace = "namespace"

	oldAnnotation      = "old-annotation-key"
	oldAnnotationValue = "old-annotation-val"

	newAnnotation      = "new-annotation"
	newAnnotationValue = "new-annotation-val"

	mockPod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:        podName,
			Namespace:   podNamespace,
			Annotations: map[string]string{oldAnnotation: oldAnnotation},
		},
		Spec:   v1.PodSpec{},
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
func getMockK8sWrapperWithClient() (K8sWrapper, client.Client) {
	scheme := runtime.NewScheme()
	v1.AddToScheme(scheme)
	fakeClient := fakeClient.NewFakeClientWithScheme(scheme, mockPod, mockNode)
	return NewK8sWrapper(fakeClient), fakeClient
}

// TestApi_AnnotatePod tests that annotate pod doesn't throw error on adding a new annotation to pod
func TestK8sWrapper_AnnotatePod(t *testing.T) {
	wrapper, _ := getMockK8sWrapperWithClient()

	testPod := mockPod.DeepCopy()

	err := wrapper.AnnotatePod(testPod, newAnnotation, newAnnotationValue)
	assert.NoError(t, err)
}

// TestK8sWrapper_GetPod gets the pod object form the wrapper api
func TestK8sWrapper_GetPod(t *testing.T) {
	wrapper, _ := getMockK8sWrapperWithClient()

	testPod := mockPod.DeepCopy()

	pod, err := wrapper.GetPod(podNamespace, podName)
	assert.NoError(t, err)
	assert.Equal(t, testPod.ObjectMeta, pod.ObjectMeta)
}

// TestK8sWrapper_GetPod_NotExists tests that error is returned when the pod doesn't exist
func TestK8sWrapper_GetPod_NotExists(t *testing.T) {
	wrapper, _ := getMockK8sWrapperWithClient()

	_, err := wrapper.GetPod(podNamespace, "not-exist")
	assert.NotNil(t, err)
}

// TestNewK8sWrapper_Annotate_And_Get_Pod annotates a pod and gets it from wrapper to verify annotation exists
func TestNewK8sWrapper_Annotate_And_Get_Pod(t *testing.T) {
	wrapper, _ := getMockK8sWrapperWithClient()

	testPod := mockPod.DeepCopy()

	err := wrapper.AnnotatePod(testPod, newAnnotation, newAnnotationValue)
	assert.NoError(t, err)

	pod, err := wrapper.GetPod(podNamespace, podName)
	assert.NoError(t, err)
	assert.Equal(t, newAnnotationValue, pod.Annotations[newAnnotation])
}

// TestK8sWrapper_AdvertiseCapacity tests that the capacity is advertised to the k8s node
func TestK8sWrapper_AdvertiseCapacity(t *testing.T) {
	wrapper, client := getMockK8sWrapperWithClient()

	// Make node copy and ensure that the advertised capacity is 0
	testNode := mockNode.DeepCopy()
	quantity := testNode.Status.Capacity[v1.ResourceName(mockResourceName)]
	assert.True(t, quantity.IsZero())

	// Advertise capacity
	capacityToAdvertise := 10
	err := wrapper.AdvertiseCapacity(nodeName, mockResourceName, capacityToAdvertise)
	assert.NoError(t, err)

	// Get the node from the client and verify the capacity is set
	node := &v1.Node{}
	err = client.Get(context.Background(), types.NamespacedName{Name: nodeName}, node)

	// Verify no error and the capacity is set to the desired capacity
	assert.NoError(t, err)
	newQuantity := node.Status.Capacity[v1.ResourceName(mockResourceName)]
	assert.Equal(t, int64(capacityToAdvertise), newQuantity.Value())
}

// TestK8sWrapper_AdvertiseCapacity_Err tests that error is thrown when an error is encountered on the advertise resource
func TestK8sWrapper_AdvertiseCapacity_Err(t *testing.T) {
	wrapper, _ := getMockK8sWrapperWithClient()

	deletedNodeName := "deleted-node"
	err := wrapper.AdvertiseCapacity(deletedNodeName, mockResourceName, 10)
	assert.NotNil(t, err)
}
