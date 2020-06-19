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
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
)

// getMockK8sWrapper returns the mock wrapper interface
func getMockK8sWrapper() K8sWrapper {
	scheme := runtime.NewScheme()
	v1.AddToScheme(scheme)
	client := fake.NewFakeClientWithScheme(scheme, mockPod)
	return NewK8sWrapper(client)
}

// TestApi_AnnotatePod tests that annotate pod doesn't throw error on adding a new annotation to pod
func TestK8sWrapper_AnnotatePod(t *testing.T) {
	wrapper := getMockK8sWrapper()

	testPod := mockPod.DeepCopy()

	err := wrapper.AnnotatePod(testPod, newAnnotation, newAnnotationValue)
	assert.NoError(t, err)
}

// TestK8sWrapper_GetPod gets the pod object form the wrapper api
func TestK8sWrapper_GetPod(t *testing.T) {
	wrapper := getMockK8sWrapper()

	testPod := mockPod.DeepCopy()

	pod, err := wrapper.GetPod(podNamespace, podName)
	assert.NoError(t, err)
	assert.Equal(t, testPod.ObjectMeta, pod.ObjectMeta)
}

// TestK8sWrapper_GetPod_NotExists tests that error is returned when the pod doesn't exist
func TestK8sWrapper_GetPod_NotExists(t *testing.T) {
	wrapper := getMockK8sWrapper()

	_, err := wrapper.GetPod(podNamespace, "not-exist")
	assert.NotNil(t, err)
}

// TestNewK8sWrapper_Annotate_And_Get_Pod annotates a pod and gets it from wrapper to verify annotation exists
func TestNewK8sWrapper_Annotate_And_Get_Pod(t *testing.T) {
	wrapper := getMockK8sWrapper()

	testPod := mockPod.DeepCopy()

	err := wrapper.AnnotatePod(testPod, newAnnotation, newAnnotationValue)
	assert.NoError(t, err)

	pod, err := wrapper.GetPod(podNamespace, podName)
	assert.NoError(t, err)
	assert.Equal(t, newAnnotationValue, pod.Annotations[newAnnotation])
}
