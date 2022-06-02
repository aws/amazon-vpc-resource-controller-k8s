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

type MockNodeUpdateWebhook struct {
	MockCondition *mock_condition.MockConditions
}

func TestNodeUpdateWebhook_Handle(t *testing.T) {
	schema := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(schema)
	assert.NoError(t, err)

	decoder, _ := admission.NewDecoder(schema)

	baseNode := &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Node",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			Annotations: map[string]string{
				"existing-key": "existing-val",
			},
			Labels: map[string]string{
				"existing-label": "existing-val",
			},
		},
		Spec: corev1.NodeSpec{
			Unschedulable: false,
		},
		Status: corev1.NodeStatus{
			Phase: "Ready",
		},
	}

	oldNode := baseNode.DeepCopy()
	oldNodeRaw, _ := json.Marshal(oldNode)

	nodeWithHasTrunkAttachedLabel := baseNode.DeepCopy()
	nodeWithHasTrunkAttachedLabel.Labels[config.HasTrunkAttachedLabel] = "true"
	nodeWithHasTrunkAttachedLabelRaw, _ := json.Marshal(nodeWithHasTrunkAttachedLabel)

	nodeWithENIConfigLabel := baseNode.DeepCopy()
	nodeWithENIConfigLabel.Labels[config.CustomNetworkingLabel] = "us-west-2a"
	nodeWithENIConfigLabelRaw, _ := json.Marshal(nodeWithENIConfigLabel)

	nodeWithUnknownLabel := baseNode.DeepCopy()
	nodeWithUnknownLabel.Labels["some-label"] = "some-val"
	nodeWithUnknownLabelRaw, _ := json.Marshal(nodeWithUnknownLabel)

	nodeWithUpdatedStatus := baseNode.DeepCopy()
	nodeWithUpdatedStatus.Status.Phase = "NotReady"
	nodeWithUpdatedStatusRaw, _ := json.Marshal(nodeWithUpdatedStatus)

	nodeWithUpdatedSpec := baseNode.DeepCopy()
	nodeWithUpdatedSpec.Spec.ProviderID = "some-val"
	nodeWithUpdatedSpecRaw, _ := json.Marshal(nodeWithUpdatedSpec)

	nodeWithTaints := baseNode.DeepCopy()
	nodeWithTaints.Spec.Taints = []corev1.Taint{
		{
			Key:   "effect",
			Value: "NoSchedule",
		},
	}
	//json.Marshal(fargatePodWithDifferentAnnotation)

	test := []struct {
		name           string
		req            []admission.Request // Club tests with similar response & diff request in 1 test
		want           admission.Response
		mockInvocation func(mock MockNodeUpdateWebhook)
	}{
		{
			name: "[sgp] allow request when username is not aws-node",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: "system:serviceaccount:sa:sa-name",
						},
						Object: runtime.RawExtension{
							Raw:    oldNodeRaw,
							Object: oldNode,
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
			mockInvocation: func(mock MockNodeUpdateWebhook) {
			},
		},
		{
			name: "[sgp] allow request when expected label/annotations are modified",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
						},
						Object: runtime.RawExtension{
							Raw:    nodeWithHasTrunkAttachedLabelRaw,
							Object: nodeWithHasTrunkAttachedLabel,
						},
						OldObject: runtime.RawExtension{
							Raw:    oldNodeRaw,
							Object: oldNode,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
						},
						Object: runtime.RawExtension{
							Raw:    nodeWithENIConfigLabelRaw,
							Object: nodeWithENIConfigLabel,
						},
						OldObject: runtime.RawExtension{
							Raw:    oldNodeRaw,
							Object: oldNode,
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
		{
			name: "[sgp] deny request if any non expected field is modified",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
						},
						Object: runtime.RawExtension{
							Raw:    nodeWithUnknownLabelRaw,
							Object: nodeWithUnknownLabel,
						},
						OldObject: runtime.RawExtension{
							Raw:    oldNodeRaw,
							Object: oldNode,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
						},
						Object: runtime.RawExtension{
							Raw:    nodeWithUpdatedStatusRaw,
							Object: nodeWithUpdatedStatus,
						},
						OldObject: runtime.RawExtension{
							Raw:    oldNodeRaw,
							Object: oldNode,
						},
					},
				},
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
						},
						Object: runtime.RawExtension{
							Raw:    nodeWithUpdatedSpecRaw,
							Object: nodeWithUpdatedSpec,
						},
						OldObject: runtime.RawExtension{
							Raw:    oldNodeRaw,
							Object: oldNode,
						},
					},
				},
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
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
				mock := MockNodeUpdateWebhook{
					MockCondition: mock_condition.NewMockConditions(ctrl),
				}

				h := &NodeUpdateWebhook{
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
