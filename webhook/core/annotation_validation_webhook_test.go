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
	v1 "k8s.io/api/authentication/v1"
	"net/http"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/stretchr/testify/assert"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

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

	podWithAnnotation := basePod.DeepCopy()
	podWithAnnotation.Annotations[config.ResourceNamePodENI] = "annotation-value"
	podWithAnnotationRaw, err := json.Marshal(podWithAnnotation)
	assert.NoError(t, err)

	podWithDifferentAnnotation := basePod.DeepCopy()
	podWithDifferentAnnotation.Annotations[config.ResourceNamePodENI] = "annotation-value-2"
	podWithDifferentAnnotationRaw, err := json.Marshal(podWithDifferentAnnotation)
	assert.NoError(t, err)

	test := []struct {
		name string
		req  admission.Request
		want admission.Response
	}{
		{
			name: "[create] when no annotation, approve request ",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					Operation: v1beta1.Create,
					Object: runtime.RawExtension{
						Raw:    podWithoutAnnotationRaw,
						Object: podWithoutAnnotation,
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
		},
		{
			name: "[create] when there's annotation, reject request",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					Operation: v1beta1.Create,
					Object: runtime.RawExtension{
						Raw:    podWithAnnotationRaw,
						Object: podWithAnnotation,
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
		},
		{
			name: "[update] annotation created by old vpc-resource-controller SA, allow request",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					UserInfo:  v1.UserInfo{Username: validUserInfo},
					Operation: v1beta1.Update,
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
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
		},
		{
			name: "[update] annotation created by new vpc-resource-controller SA, allow request",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					UserInfo:  v1.UserInfo{Username: newValidUserInfo},
					Operation: v1beta1.Update,
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
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
					Allowed: true,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
		},
		{
			name: "[update] annotation created by unauthorized user, deny request",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
					Operation: v1beta1.Update,
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
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
		},
		{
			name: "[update] annotation updated by unauthorized user, deny request",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
					Operation: v1beta1.Update,
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
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
		},
		{
			name: "[update] annotation deleted by unauthorized user, deny request",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
					Operation: v1beta1.Update,
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
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusForbidden,
					},
				},
			},
		},
		{
			name: "[delete] delete, allow request",
			req: admission.Request{
				AdmissionRequest: v1beta1.AdmissionRequest{
					UserInfo:  v1.UserInfo{Username: "some unauthorized user"},
					Operation: v1beta1.Delete,
					OldObject: runtime.RawExtension{
						Raw:    podWithAnnotationRaw,
						Object: podWithAnnotation,
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: v1beta1.AdmissionResponse{
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
			ctx := context.Background()
			h := &AnnotationValidator{
				decoder: decoder,
				Log:     zap.New(),
			}
			got := h.Handle(ctx, tt.req)
			assert.Equal(t, tt.want.Allowed, got.Allowed)
			assert.Equal(t, tt.want.Result.Status, got.Result.Status)
		})
	}
}
