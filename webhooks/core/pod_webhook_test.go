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
	"fmt"
	"strings"
	"testing"

	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_utils "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	jsonPatchType                 = admissionv1.PatchTypeJSONPatch
	firstContainerPatchRequestURI = "/spec/containers/0/resources/requests"
	firstContainerPatchLimitURI   = "/spec/containers/0/resources/limits"
	ipResourceJsonPointer         = "/" + jsonPointer(config.ResourceNameIPAddress)
	podENIResourceJsonPointer     = "/" + jsonPointer(config.ResourceNamePodENI)
)

type Mock struct {
	SGPMock       *mock_utils.MockSecurityGroupForPodsAPI
	ConditionMock *mock_condition.MockConditions
}

func TestPodMutationWebHook_Handle(t *testing.T) {
	schema := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(schema)
	assert.NoError(t, err)

	decoder := admission.NewDecoder(schema)

	name := "foo"
	namespace := "default"
	sgList := []string{"sg-1", "sg-2"}
	mockErr := fmt.Errorf("mock erorr")

	basePod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{},
			Annotations: map[string]string{
				"existing-key": "existing-val",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "bar",
					Resources: corev1.ResourceRequirements{
						Limits: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceCPU: resource.MustParse("0.1"),
						},
						Requests: map[corev1.ResourceName]resource.Quantity{
							corev1.ResourceCPU: resource.MustParse("0.2"),
						},
					},
				},
			},
			NodeSelector: map[string]string{},
		},
	}

	// Host Networking Pod
	hostNetworkPod := basePod.DeepCopy()
	hostNetworkPod.Spec.HostNetwork = true
	hostNetworkPodRaw, err := json.Marshal(hostNetworkPod)
	assert.NoError(t, err)

	// Windows Pod with non-beta label
	windowsPod := basePod.DeepCopy()
	windowsPod.Spec.NodeSelector[config.NodeLabelOS] = config.OSWindows
	windowsPodRaw, err := json.Marshal(windowsPod)
	assert.NoError(t, err)

	// Windows Pod with beta label
	windowsPodBeta := basePod.DeepCopy()
	windowsPodBeta.Spec.NodeSelector[config.NodeLabelOSBeta] = config.OSWindows
	windowsBetaPodRaw, err := json.Marshal(windowsPodBeta)
	assert.NoError(t, err)

	// Windows Pod no existing containers request and Limit
	windowsNoLimits := basePod.DeepCopy()
	windowsNoLimits.Spec.NodeSelector[config.NodeLabelOSBeta] = config.OSWindows
	windowsNoLimits.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
	windowsNoLimitsRaw, err := json.Marshal(windowsNoLimits)
	assert.NoError(t, err)

	// Fargate Pod
	fargatePod := basePod.DeepCopy()
	fargatePod.Labels[FargatePodIdentifierLabelKey] = "fargate-profile"
	fargatePodRaw, err := json.Marshal(fargatePod)
	assert.NoError(t, err)

	// Fargate Pod with annotation added by user
	fargatePodWithAnnotation := fargatePod.DeepCopy()
	fargatePodWithAnnotation.Annotations[FargatePodSGAnnotationKey] = strings.Join(sgList, ",")
	fargatePodWithAnnotationRaw, err := json.Marshal(fargatePodWithAnnotation)
	assert.NoError(t, err)

	// Security Group Pod
	sgpPod := basePod.DeepCopy()
	sgpPodRaw, err := json.Marshal(sgpPod)
	assert.NoError(t, err)

	// Security Group Pod with resource limits
	sgpPodWithoutLimits := basePod.DeepCopy()
	sgpPodWithoutLimits.Spec.Containers[0].Resources = corev1.ResourceRequirements{}
	sgpPodWithoutLimitsRaw, err := json.Marshal(sgpPodWithoutLimits)
	assert.NoError(t, err)

	test := []struct {
		name           string
		mockInvocation func(mock Mock)
		req            admission.Request
		want           admission.Response
	}{
		{
			name: "[Linux] Pod matches SG no existing resource limits",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    sgpPodWithoutLimitsRaw,
						Object: sgpPodWithoutLimits,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(sgpPod)).Return(sgList, nil)
			},

			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "add",
						Path:      firstContainerPatchLimitURI,
						Value:     map[string]interface{}{config.ResourceNamePodENI: "1"},
					},
					{
						Operation: "add",
						Path:      firstContainerPatchRequestURI,
						Value:     map[string]interface{}{config.ResourceNamePodENI: "1"},
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
		},
		{
			name: "[Linux] Pod matches SG",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    sgpPodRaw,
						Object: sgpPod,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(sgpPod)).Return(sgList, nil)
			},

			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "add",
						Path:      firstContainerPatchRequestURI + podENIResourceJsonPointer,
						Value:     "1",
					},
					{
						Operation: "add",
						Path:      firstContainerPatchLimitURI + podENIResourceJsonPointer,
						Value:     "1",
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
		},
		{
			name: "[Linux] Pod doesn't match annotation",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    sgpPodRaw,
						Object: sgpPod,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(sgpPod)).Return([]string{}, nil)
			},

			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
				},
			},
		},
		{
			name: "[Linux] SGP returns error",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    sgpPodRaw,
						Object: sgpPod,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(sgpPod)).Return(nil, mockErr)
			},

			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
				},
			},
		},
		{
			name: "[Fargate] not matching any SG",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    fargatePodRaw,
						Object: fargatePod,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(fargatePod)).Return([]string{}, nil)
			},

			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
				},
			},
		},
		{
			name: "[Fargate] user adds SG, must be replaced with new list",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    fargatePodWithAnnotationRaw,
						Object: fargatePodWithAnnotation,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(fargatePod)).Return([]string{"sg1"}, nil)
			},

			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "replace",
						Path:      "/metadata/annotations/" + jsonPointer(FargatePodSGAnnotationKey),
						Value:     "sg1",
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
		},
		{
			name: "[Fargate] user adds SG",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    fargatePodWithAnnotationRaw,
						Object: fargatePodWithAnnotation,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(fargatePod)).Return(nil, nil)
			},

			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "remove",
						Path:      "/metadata/annotations/" + jsonPointer(FargatePodSGAnnotationKey),
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
		},
		{
			name: "[Fargate] matching some SG",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    fargatePodRaw,
						Object: fargatePod,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(fargatePod)).Return(sgList, nil)
			},

			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "add",
						Path:      "/metadata/annotations/" + jsonPointer(FargatePodSGAnnotationKey),
						Value:     strings.Join(sgList, ","),
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
		},
		{
			name: "[Fargate] SGP returns error",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    fargatePodRaw,
						Object: fargatePod,
					},
				},
			},
			mockInvocation: func(mock Mock) {
				mock.SGPMock.EXPECT().GetMatchingSecurityGroupForPods(gomock.AssignableToTypeOf(fargatePod)).Return(nil, mockErr)
			},

			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
				},
			},
		},
		{
			name: "[HostNetwork] should be allowed",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    hostNetworkPodRaw,
						Object: hostNetworkPod,
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
				},
			},
		},
		{
			name: "[Windows] with non-beta label, should be allowed",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    windowsPodRaw,
						Object: windowsPod,
					},
				},
			},
			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "add",
						Path:      firstContainerPatchLimitURI + ipResourceJsonPointer,
						Value:     "1",
					},
					{
						Operation: "add",
						Path:      firstContainerPatchRequestURI + ipResourceJsonPointer,
						Value:     "1",
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
			mockInvocation: func(mock Mock) {
				mock.ConditionMock.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[Windows] with beta label, should be allowed",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    windowsBetaPodRaw,
						Object: windowsPodBeta,
					},
				},
			},
			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "add",
						Path:      firstContainerPatchLimitURI + ipResourceJsonPointer,
						Value:     "1",
					},
					{
						Operation: "add",
						Path:      firstContainerPatchRequestURI + ipResourceJsonPointer,
						Value:     "1",
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
			mockInvocation: func(mock Mock) {
				mock.ConditionMock.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[Windows] with no existing container limits, should be allowed",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    windowsNoLimitsRaw,
						Object: windowsNoLimits,
					},
				},
			},
			want: admission.Response{
				Patches: []jsonpatch.JsonPatchOperation{
					{
						Operation: "add",
						Path:      firstContainerPatchLimitURI,
						Value:     map[string]interface{}{config.ResourceNameIPAddress: "1"},
					},
					{
						Operation: "add",
						Path:      firstContainerPatchRequestURI,
						Value:     map[string]interface{}{config.ResourceNameIPAddress: "1"},
					},
				},
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed:   true,
					PatchType: &jsonPatchType,
				},
			},
			mockInvocation: func(mock Mock) {
				mock.ConditionMock.EXPECT().IsWindowsIPAMEnabled().Return(true)
			},
		},
		{
			name: "[Windows] when feature is disabled, there should be no ops",
			req: admission.Request{
				AdmissionRequest: admissionv1.AdmissionRequest{
					Object: runtime.RawExtension{
						Raw:    windowsNoLimitsRaw,
						Object: windowsNoLimits,
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: true,
				},
			},
			mockInvocation: func(mock Mock) {
				mock.ConditionMock.EXPECT().IsWindowsIPAMEnabled().Return(false)
			},
		},
	}

	for _, tt := range test {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			ctx := context.TODO()
			mock := Mock{
				SGPMock:       mock_utils.NewMockSecurityGroupForPodsAPI(ctrl),
				ConditionMock: mock_condition.NewMockConditions(ctrl),
			}
			h := &PodMutationWebHook{
				decoder:   decoder,
				Log:       zap.New(),
				SGPAPI:    mock.SGPMock,
				Condition: mock.ConditionMock,
			}

			if tt.mockInvocation != nil {
				tt.mockInvocation(mock)
			}

			got := h.Handle(ctx, tt.req)

			assert.Equal(t, tt.want.Allowed, got.Allowed)
			assert.ElementsMatch(t, tt.want.Patches, got.Patches)
			assert.Equal(t, tt.want.PatchType, got.PatchType)
		})
	}
}

// See: https://datatracker.ietf.org/doc/html/rfc6901#section-3
// jsonPointer converts string to Json Pointer
func jsonPointer(str string) string {
	return strings.ReplaceAll(str, "/", "~1")
}
