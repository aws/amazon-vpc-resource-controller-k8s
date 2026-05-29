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
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// TestStripDownPod verifies the memory optimization behavior of StripDownPod.
//
// The controller only manages pods that use VPC resources (Security Groups for Pods
// or Windows IPAM). All other pods are irrelevant to the controller's reconciliation
// loop. To reduce memory footprint on large clusters, StripDownPod returns nil for
// pods without VPC resources so the caller can skip caching them.
func TestStripDownPod(t *testing.T) {
	converter := &PodConverter{}

	t.Run("returns nil for pods without VPC resources", func(t *testing.T) {
		// A standard workload pod with no security group association and no
		// VPC resource requests should not be cached by the controller.
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nginx",
				Namespace: "default",
				UID:       types.UID("uid-1"),
				Annotations: map[string]string{
					"kubectl.kubernetes.io/last-applied-configuration": "...",
					"prometheus.io/scrape":                             "true",
				},
			},
			Spec: v1.PodSpec{
				NodeName: "node-1",
				Containers: []v1.Container{
					{
						Name: "nginx",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU:    resource.MustParse("100m"),
								v1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					},
				},
			},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		}

		result := converter.StripDownPod(pod)
		assert.Nil(t, result, "pods without VPC annotations or resource limits must return nil")
	})

	t.Run("returns stripped pod when VPC annotations are present", func(t *testing.T) {
		// When SecurityGroupPolicy matches a pod, the mutating webhook injects
		// VPC resource annotations. These pods must be cached for ENI management.
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "sgp-pod",
				Namespace: "payments",
				UID:       types.UID("uid-2"),
				Annotations: map[string]string{
					config.ResourceNamePodENI:   `[{"eniId":"eni-0abc123"}]`,
					"app.kubernetes.io/version": "v2",
					"prometheus.io/port":        "9090",
				},
			},
			Spec: v1.PodSpec{
				NodeName:           "node-2",
				ServiceAccountName: "payment-sa",
				Containers: []v1.Container{
					{
						Name: "app",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource.MustParse("500m"),
							},
						},
					},
				},
			},
			Status: v1.PodStatus{Phase: v1.PodRunning},
		}

		result := converter.StripDownPod(pod)
		assert.NotNil(t, result)

		// Only VPC-prefixed annotations should be retained
		assert.Equal(t, `[{"eniId":"eni-0abc123"}]`, result.Annotations[config.ResourceNamePodENI])
		assert.NotContains(t, result.Annotations, "app.kubernetes.io/version")
		assert.NotContains(t, result.Annotations, "prometheus.io/port")

		// Identity and scheduling fields must be preserved for reconciliation
		assert.Equal(t, "sgp-pod", result.Name)
		assert.Equal(t, "payments", result.Namespace)
		assert.Equal(t, types.UID("uid-2"), result.UID)
		assert.Equal(t, "node-2", result.Spec.NodeName)
		assert.Equal(t, "payment-sa", result.Spec.ServiceAccountName)
		assert.Equal(t, v1.PodRunning, result.Status.Phase)

		// Non-VPC containers should be stripped entirely
		assert.Empty(t, result.Spec.Containers, "containers without VPC resource requests are not retained")
	})

	t.Run("returns stripped pod when VPC resource limits are present", func(t *testing.T) {
		// The mutating webhook injects vpc.amazonaws.com/pod-eni resource
		// requests into containers when SecurityGroupPolicy matches.
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "branch-eni-pod",
				Namespace: "default",
				UID:       types.UID("uid-3"),
			},
			Spec: v1.PodSpec{
				NodeName: "node-1",
				Containers: []v1.Container{
					{
						Name: "app",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceName(config.ResourceNamePodENI): resource.MustParse("1"),
								v1.ResourceCPU:    resource.MustParse("250m"),
								v1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					},
					{
						Name: "sidecar",
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceCPU: resource.MustParse("50m"),
							},
						},
					},
				},
			},
			Status: v1.PodStatus{Phase: v1.PodPending},
		}

		result := converter.StripDownPod(pod)
		assert.NotNil(t, result)

		// Only containers with VPC resource requests should be retained,
		// and only the VPC resource entries within those containers.
		assert.Len(t, result.Spec.Containers, 1, "only the container with VPC resources is kept")
		assert.Contains(t, result.Spec.Containers[0].Resources.Requests,
			v1.ResourceName(config.ResourceNamePodENI))
		assert.NotContains(t, result.Spec.Containers[0].Resources.Requests,
			v1.ResourceCPU, "non-VPC resource requests are stripped")
	})
}

// TestConvertObject verifies the Converter interface contract. ConvertObject is called
// by the custom controller's Process function for every watch event. Returning (nil, nil)
// signals that the pod should be skipped — the Process loop must handle this gracefully.
func TestConvertObject(t *testing.T) {
	converter := &PodConverter{}

	t.Run("returns nil for non-VPC pods without error", func(t *testing.T) {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "regular", Namespace: "default"},
			Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "app"}}},
		}

		result, err := converter.ConvertObject(pod)
		assert.NoError(t, err)
		assert.Nil(t, result, "non-VPC pods return (nil, nil) to signal skip")
	})

	t.Run("returns converted pod for VPC pods", func(t *testing.T) {
		pod := &v1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "vpc-pod",
				Namespace:   "default",
				Annotations: map[string]string{config.ResourceNamePodENI: "data"},
			},
			Spec: v1.PodSpec{NodeName: "node-1"},
		}

		result, err := converter.ConvertObject(pod)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "vpc-pod", result.(*v1.Pod).Name)
	})

	t.Run("returns error for non-pod objects", func(t *testing.T) {
		result, err := converter.ConvertObject("not-a-pod")
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

// TestConvertList verifies that during List (initial sync and resync), non-VPC pods
// are excluded from the converted list. This prevents the datastore from being
// populated with pods the controller will never reconcile.
func TestConvertList(t *testing.T) {
	converter := &PodConverter{}

	t.Run("filters out non-VPC pods and preserves pagination", func(t *testing.T) {
		podList := &v1.PodList{
			ListMeta: metav1.ListMeta{
				ResourceVersion: "12345",
				Continue:        "continuation-token",
			},
			Items: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "app-1", Namespace: "default"},
					Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "web"}}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "vpc-pod",
						Namespace:   "kube-system",
						Annotations: map[string]string{config.ResourceNamePodENI: "data"},
					},
					Spec: v1.PodSpec{NodeName: "node-1"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "app-2", Namespace: "default"},
					Spec:       v1.PodSpec{Containers: []v1.Container{{Name: "worker"}}},
				},
			},
		}

		result, err := converter.ConvertList(podList)
		assert.NoError(t, err)

		convertedList := result.(*v1.PodList)

		// Pagination metadata must be preserved for the reflector to page correctly
		assert.Equal(t, "12345", convertedList.ResourceVersion)
		assert.Equal(t, "continuation-token", convertedList.Continue)

		// Only the VPC pod should remain in the list
		assert.Len(t, convertedList.Items, 1)
		assert.Equal(t, "vpc-pod", convertedList.Items[0].Name)
	})

	t.Run("returns error for non-PodList input", func(t *testing.T) {
		_, err := converter.ConvertList("not-a-list")
		assert.Error(t, err)
	})
}
