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

package pod

import (
	"context"
	"testing"

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
	podName      = "running-pod"
	podNamespace = "namespace"

	podUid = types.UID("00000000-0000-0000-0000-000000000000")

	oldAnnotation = "old-annotation-key"

	newAnnotation      = "new-annotation"
	newAnnotationValue = "new-annotation-val"

	podTemplate = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   podNamespace,
			Annotations: map[string]string{oldAnnotation: oldAnnotation},
			UID:         podUid,
		},
		Spec:   v1.PodSpec{NodeName: mockNode.Name},
		Status: v1.PodStatus{},
	}

	failedPod, completedPod, runningPod *v1.Pod

	nodeName = "node-name"

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
func getMockPodAPIWithClient() (PodClientAPIWrapper, client.Client) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)

	runningPod = podTemplate.DeepCopy()
	runningPod.Name = podName
	runningPod.Status.Phase = v1.PodRunning

	completedPod = podTemplate.DeepCopy()
	completedPod.Name = "completed-pod"
	completedPod.Status.Phase = v1.PodSucceeded

	failedPod = podTemplate.DeepCopy()
	failedPod.Name = "failed-pod"
	failedPod.Status.Phase = v1.PodFailed

	mockObjects := []client.Object{failedPod, completedPod, runningPod}
	mockRuntimeObjects := []runtime.Object{failedPod, completedPod, runningPod}

	client := fakeClient.NewClientBuilder().WithScheme(scheme).WithObjects(mockObjects...).Build()
	clientSet := fakeClientSet.NewSimpleClientset(mockRuntimeObjects...)
	ds := getFakeDataStore()

	return NewPodAPIWrapper(ds, client, clientSet.CoreV1()), client
}

func getFakeDataStore() cache.Indexer {
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
	store.Add(runningPod)
	store.Add(completedPod)
	store.Add(failedPod)
	return store
}

// TestPodAPI_GetPodFromAPIServer tests if the pod is returned if it's stored with API server
func TestPodAPI_GetPodFromAPIServer(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()
	pod, err := podAPI.GetPodFromAPIServer(context.TODO(), podNamespace, podName)

	assert.NoError(t, err)
	assert.Equal(t, runningPod, pod)
}

func TestPodAPI_GetRunningPodsOnNode(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()

	podList, err := podAPI.GetRunningPodsOnNode(nodeName)

	assert.NoError(t, err)
	assert.Equal(t, podList[0], *runningPod)
}

// TestPodPAI_GetPodFromAPIServer_NoError tests that error is returned when the pod doesn't exist
func TestPodPAI_GetPodFromAPIServer_NoError(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()

	_, err := podAPI.GetPodFromAPIServer(context.TODO(), podNamespace, podName+"not-exists")

	assert.NotNil(t, err)
}

// TestPodAPI_ListPods tests that list pod returns the list of pods with the given node name in their node spec
// https://github.com/kubernetes/client-go/issues/326
func TestPodAPI_ListPods(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()

	podList, err := podAPI.ListPods(nodeName)

	assert.NoError(t, err)
	assert.ElementsMatch(t, podList.Items, []v1.Pod{*runningPod, *completedPod, *failedPod})
}

func TestPodAPI_AnnotatePod_UID_Changed(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()

	newUiD := types.UID("00000000-0000-0000-0000-000000000001")

	err := podAPI.AnnotatePod(podNamespace, podName, newUiD, newAnnotation, newAnnotationValue)
	assert.Error(t, err)
}

// TestPodAPI_AnnotatePod tests that annotate pod doesn't throw error on adding a new annotation to pod
func TestPodAPI_AnnotatePod(t *testing.T) {
	podAPI, k8sClient := getMockPodAPIWithClient()

	err := podAPI.AnnotatePod(podNamespace, podName, podUid, newAnnotation, newAnnotationValue)
	assert.NoError(t, err)

	// Validate the pod got the annotation
	updatedPod := &v1.Pod{}
	err = k8sClient.Get(context.TODO(), types.NamespacedName{
		Namespace: podNamespace,
		Name:      podName,
	}, updatedPod)

	assert.NoError(t, err)
	assert.Equal(t, newAnnotationValue, updatedPod.Annotations[newAnnotation])
}

// TestPodAPI_AnnotatePod_PodNotExists tests that annotate pod fails if the pod doesn't exist
func TestPodAPI_AnnotatePod_PodNotExists(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()

	err := podAPI.AnnotatePod(podNamespace, "non-existent-pod", podUid, newAnnotation, newAnnotationValue)
	assert.NotNil(t, err)
}

// TestPodAPI_GetPod gets the pod object form the wrapper api
func TestPodAPI_GetPod(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()
	testPod := runningPod.DeepCopy()
	pod, err := podAPI.GetPod(podNamespace, podName)
	assert.NoError(t, err)
	assert.Equal(t, testPod.ObjectMeta, pod.ObjectMeta)
}

// TestPodAPI_GetPod_NotExists tests that error is returned when the pod doesn't exist
func TestPodAPI_GetPod_NotExists(t *testing.T) {
	podAPI, _ := getMockPodAPIWithClient()

	_, err := podAPI.GetPod(podNamespace, "not-exist")
	assert.NotNil(t, err)
}
