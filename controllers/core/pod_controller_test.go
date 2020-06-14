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

package controllers

import (
	"testing"
	"time"

	mock_handler "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/handler"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"

	"github.com/golang/mock/gomock"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	mockPodName                 = "pod-name"
	mockPodNS                   = "pod-namespace"
	mockResourceName            = "vpc.amazonaws.com/pod-eni"
	mockUnsupportedResourceName = "cpu"

	n = types.NamespacedName{
		Namespace: mockPodNS,
		Name:      mockPodName,
	}
	mockReq = reconcile.Request{NamespacedName: n}

	mockPod = &v1.Pod{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      mockPodName,
			Namespace: mockPodNS,
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceName(mockResourceName): *resource.NewQuantity(1, resource.DecimalSI),
						},
					},
				},
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceName(mockResourceName): *resource.NewQuantity(2, resource.DecimalSI),
						},
					},
				},
				{
					Resources: v1.ResourceRequirements{
						Requests: v1.ResourceList{
							v1.ResourceName(mockUnsupportedResourceName): *resource.NewQuantity(10, resource.DecimalExponent),
						},
					},
				},
			},
		},
		Status: v1.PodStatus{},
	}
)

// getPodReconcilerAndMockHandler returns PodReconciler and mockHandler used to setup the pod reconciler
func getPodReconcilerAndMockHandler(ctrl *gomock.Controller, mockPod *v1.Pod) (PodReconciler, *mock_handler.MockHandler) {
	scheme := runtime.NewScheme()
	v1.AddToScheme(scheme)
	mockHandler := mock_handler.NewMockHandler(ctrl)

	podReconciler := PodReconciler{
		Client:   fake.NewFakeClientWithScheme(scheme, mockPod),
		Log:      zap.New(zap.UseDevMode(true)).WithName("on demand handler"),
		Scheme:   scheme,
		handlers: []handler.Handler{mockHandler},
	}
	return podReconciler, mockHandler
}

// TestPodReconciler_Reconcile_Create test that the resource handler is invoked for supported resource type in case of
// a pod create/update event
func TestPodReconciler_Reconcile_Create(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	reconciler, mockHandler := getPodReconcilerAndMockHandler(ctrl, mockPod)

	gomock.InOrder(
		mockHandler.EXPECT().CanHandle(mockResourceName).Return(true),
		mockHandler.EXPECT().HandleCreate(mockResourceName, int64(3), gomock.Any()).Return(nil),
		mockHandler.EXPECT().CanHandle(mockUnsupportedResourceName).Return(false),
	)

	reconciler.Reconcile(mockReq)
}

// TestPodReconciler_Reconcile_Delete test that the resource handler is invoked for supported resource type in case of
// a pod delete event
func TestPodReconciler_Reconcile_Delete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	pod := mockPod.DeepCopy()
	ti := metav1.NewTime(time.Now())
	pod.DeletionTimestamp = &ti
	reconciler, mockHandler := getPodReconcilerAndMockHandler(ctrl, pod)

	gomock.InOrder(
		mockHandler.EXPECT().CanHandle(mockResourceName).Return(true),
		mockHandler.EXPECT().HandleDelete(mockResourceName, gomock.Any()).Return(nil),
		mockHandler.EXPECT().CanHandle(mockUnsupportedResourceName).Return(false),
	)

	reconciler.Reconcile(mockReq)
}
