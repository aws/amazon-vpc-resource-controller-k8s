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

package core

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type MockAnnotationWebHook struct {
	MockCondition *mock_condition.MockConditions
}

func TestAnnotationValidator_InjectDecoder(t *testing.T) {
	a := AnnotationValidator{}
	decoder := &admission.Decoder{}
	a.InjectDecoder(decoder)

	assert.Equal(t, decoder, a.decoder)
}

func TestAnnotationValidator_Handle(t *testing.T) {
	schema := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(schema)
	assert.NoError(t, err)

	decoder, _ := admission.NewDecoder(schema)

	basePod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "foo",
			Annotations: map[string]string{
				"existing-key": "existing-val",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "bar",
				},
			},
		},
	}

	podWithoutAnnotation := basePod.DeepCopy()
	podWithoutAnnotationRaw, err := json.Marshal(podWithoutAnnotation)
	assert.NoError(t, err)

	windowsPodWithAnnotation := basePod.DeepCopy()
	windowsPodWithAnnotation.Annotations[config.ResourceNameIPAddress] = "192.168.68.74"
	windowsPodWithAnnotationRaw, err := json.Marshal(windowsPodWithAnnotation)
	assert.NoError(t, err)

	podWithAnnotation := basePod.DeepCopy()
	podWithAnnotation.Annotations[config.ResourceNamePodENI] = "annotation-value"
	podWithAnnotationRaw, err := json.Marshal(podWithAnnotation)
	assert.NoError(t, err)

	podWithDifferentAnnotation := basePod.DeepCopy()
	podWithDifferentAnnotation.Annotations[config.ResourceNamePodENI] = "annotation-value-2"
	podWithDifferentAnnotationRaw, err := json.Marshal(podWithDifferentAnnotation)
	assert.NoError(t, err)

	fargatePodWithoutAnnotation := basePod.DeepCopy()
	fargatePodWithoutAnnotationRaw, err := json.Marshal(fargatePodWithoutAnnotation)
	assert.NoError(t, err)

	fargatePodWithAnnotation := basePod.DeepCopy()
	fargatePodWithAnnotation.Annotations[FargatePodSGAnnotationKey] = "sg-123"
	fargatePodWithAnnotationRaw, err := json.Marshal(fargatePodWithAnnotation)
	assert.NoError(t, err)

	fargatePodWithDifferentAnnotation := basePod.DeepCopy()
	fargatePodWithDifferentAnnotation.Annotations[FargatePodSGAnnotationKey] = "sg-456"
	fargatePodWithDifferentAnnotationRaw, err := json.Marshal(fargatePodWithDifferentAnnotation)
	assert.NoError(t, err)

	test := []struct {
		name           string
		req            []admission.Request // Club tests with similar response & diff request in 1 test
		want           admission.Response
		mockInvocation func(mock MockAnnotationWebHook)
	}{
		{
			name: "[windows] allow all request when feature disabled ",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Create,
						Object: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						UserInfo:  v1.UserInfo{Username: "unauthorized-user"},
						Object: runtime.RawExtension{
							Raw:    podWithoutAnnotationRaw,
							Object: podWithoutAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(false)
			},
		},
		{
			name: "[create] when no annotation, approve request ",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Create,
						Object: runtime.RawExtension{
							Raw:    podWithoutAnnotationRaw,
							Object: podWithoutAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(false)
			},
		},
		{
			name: "[create] when there's annotation, reject request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Create,
						Object: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Create,
						Object: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[update] annotation created by old vpc-resource-controller SA, allow request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: validUserInfo},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithoutAnnotationRaw,
							Object: podWithoutAnnotation,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: validUserInfo},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[update] annotation created by new vpc-resource-controller SA, allow request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: newValidUserInfo},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithoutAnnotationRaw,
							Object: podWithoutAnnotation,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: newValidUserInfo},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[update] annotation created by unauthorized user, deny request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithoutAnnotationRaw,
							Object: podWithoutAnnotation,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[update] annotation updated by unauthorized user, deny request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithDifferentAnnotationRaw,
							Object: podWithDifferentAnnotation,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[update] annotation deleted by unauthorized user, deny request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    podWithoutAnnotationRaw,
							Object: podWithoutAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    windowsPodWithAnnotationRaw,
							Object: windowsPodWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
			mockInvocation: func(mock MockAnnotationWebHook) {
				mock.MockCondition.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[update] fargate pod-sg annotation added during UPDATE event, deny request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    fargatePodWithAnnotationRaw,
							Object: fargatePodWithAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    fargatePodWithoutAnnotationRaw,
							Object: fargatePodWithoutAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
		},
		{
			name: "[update] fargate pod-sg annotation updated during UPDATE event, deny request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    fargatePodWithDifferentAnnotationRaw,
							Object: fargatePodWithDifferentAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    fargatePodWithAnnotationRaw,
							Object: fargatePodWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
		},
		{
			name: "[update] fargate pod-sg annotation deleted during UPDATE event, deny request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						Object: runtime.RawExtension{
							Raw:    fargatePodWithoutAnnotationRaw,
							Object: fargatePodWithoutAnnotation,
						},
						OldObject: runtime.RawExtension{
							Raw:    fargatePodWithAnnotationRaw,
							Object: fargatePodWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
		},
		{
			name: "[delete] delete, allow request",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
						Operation: admissionv1.Delete,
						OldObject: runtime.RawExtension{
							Raw:    podWithAnnotationRaw,
							Object: podWithAnnotation,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
		},
	}

	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			for _, req := range tt.req {
				ctx := context.Background()
				mock := MockAnnotationWebHook{
					MockCondition: mock_condition.NewMockConditions(ctrl),
				}

				h := &AnnotationValidator{
					decoder:   decoder,
					Log:       zap.New(),
					Condition: mock.MockCondition,
				}

				if tt.mockInvocation != nil {
					tt.mockInvocation(mock)
				}

				got := h.Handle(ctx, req)
				assert.Equal(t, tt.want.Allowed, got.Allowed)
				assert.Equal(t, tt.want.Result.Status, got.Result.Status)
			}
		})
	}
}
