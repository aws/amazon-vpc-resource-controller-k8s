package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeSchema "k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type MockNodeUpdateWebhook struct {
	MockCondition *mock_condition.MockConditions
	MockClient    *mock_k8s.MockK8sWrapper
}

var (
	baseNode = &corev1.Node{
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

	oldNode       = baseNode.DeepCopy()
	oldNodeRaw, _ = json.Marshal(oldNode)

	awsPodName                    = "aws-node-testing"
	nodeName                      = "testing-node"
	nodeWithHasTrunkAttachedLabel = baseNode.DeepCopy()
	nodeWithENIConfigLabel        = baseNode.DeepCopy()
	nodeWithUnknownLabel          = baseNode.DeepCopy()
	nodeWithUpdatedStatus         = baseNode.DeepCopy()
	nodeWithUpdatedSpec           = baseNode.DeepCopy()
	nodeWithTaints                = baseNode.DeepCopy()
)

func TestNodeUpdateWebhook_Handle(t *testing.T) {
	schema := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(schema)
	assert.NoError(t, err)

	decoder, _ := admission.NewDecoder(schema)

	nodeWithHasTrunkAttachedLabel.Labels[config.HasTrunkAttachedLabel] = "true"
	nodeWithHasTrunkAttachedLabelRaw, _ := json.Marshal(nodeWithHasTrunkAttachedLabel)

	nodeWithENIConfigLabel.Labels[config.CustomNetworkingLabel] = "us-west-2a"
	nodeWithENIConfigLabelRaw, _ := json.Marshal(nodeWithENIConfigLabel)

	nodeWithUnknownLabel.Labels["some-label"] = "some-val"
	nodeWithUnknownLabelRaw, _ := json.Marshal(nodeWithUnknownLabel)

	nodeWithUpdatedStatus.Status.Phase = "NotReady"
	nodeWithUpdatedStatusRaw, _ := json.Marshal(nodeWithUpdatedStatus)

	nodeWithUpdatedSpec.Spec.ProviderID = "some-val"
	nodeWithUpdatedSpecRaw, _ := json.Marshal(nodeWithUpdatedSpec)

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
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: "system:serviceaccount:sa:sa-name",
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
		{
			name: "[sgp] deny request if node doesn't match aws-node's nodeName",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Name:      nodeName + "-hack",
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
					MockClient:    mock_k8s.NewMockK8sWrapper(ctrl),
				}

				h := &NodeUpdateWebhook{
					decoder:   decoder,
					Log:       zap.New(),
					Condition: mock.MockCondition,
					client:    mock.MockClient,
				}

				if tt.mockInvocation != nil {
					tt.mockInvocation(mock)
				}

				testPod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      awsPodName,
						Namespace: config.KubeSystemNamespace,
					},
					Spec: corev1.PodSpec{
						NodeName: nodeName,
					},
				}
				mock.MockClient.EXPECT().GetPod(awsPodName, config.KubeSystemNamespace).Return(testPod, nil).AnyTimes()

				got := h.Handle(ctx, req)
				assert.Equal(t, tt.want.Allowed, got.Allowed)
				assert.Equal(t, tt.want.Result.Status, got.Result.Status)
			}
		})
	}
}

func TestNodeUpdateWebhook_Handle_Errors(t *testing.T) {
	schema := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(schema)
	assert.NoError(t, err)

	decoder, _ := admission.NewDecoder(schema)

	nodeWithHasTrunkAttachedLabel.Labels[config.HasTrunkAttachedLabel] = "true"
	nodeWithHasTrunkAttachedLabelRaw, _ := json.Marshal(nodeWithHasTrunkAttachedLabel)

	//json.Marshal(fargatePodWithDifferentAnnotation)

	test := []struct {
		name           string
		req            []admission.Request // Club tests with similar response & diff request in 1 test
		want           admission.Response
		retriable      bool
		mockInvocation func(mock MockNodeUpdateWebhook)
		err            *apierrors.StatusError
	}{
		{
			name: "[sgp] retriable internal error should be retried five times",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			retriable: true,
			err:       apierrors.NewInternalError(errors.New("test internal error")),
		},
		{
			name: "[sgp] retriable timeout error should be retried five times",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			retriable: true,
			err: apierrors.NewServerTimeout(runtimeSchema.GroupResource{
				Group:    "v1",
				Resource: "v1",
			}, "GET", 1),
		},
		{
			name: "[sgp] retriable internal error should be retried five times",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			retriable: true,
			err:       apierrors.NewInternalError(errors.New("test internal error")),
		},
		{
			name: "[sgp] retriable TooManyRequests error should be retried five times",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			retriable: true,
			err:       apierrors.NewTooManyRequestsError("too many requests"),
		},
		{
			name: "[sgp] retriable service unavailable error should be retried five times",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			retriable: true,
			err:       apierrors.NewServiceUnavailable("the requested service is not available"),
		},
		{
			name: "[sgp] not retriable error should be retried one time",
			req: []admission.Request{
				{
					AdmissionRequest: admissionv1.AdmissionRequest{
						Name:      nodeName,
						Operation: admissionv1.Update,
						UserInfo: v1.UserInfo{
							Username: awsNodeUsername,
							Extra: map[string]v1.ExtraValue{
								podNameKey: []string{
									awsPodName,
								},
							},
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
			},
			want: admission.Response{
				AdmissionResponse: admissionv1.AdmissionResponse{
					Allowed: false,
					Result: &metav1.Status{
						Code: http.StatusOK,
					},
				},
			},
			retriable: false,
			err: apierrors.NewForbidden(runtimeSchema.GroupResource{
				Group:    "v1",
				Resource: "v1",
			}, "webhook test", errors.New("test invalid webhook request")),
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
					MockClient:    mock_k8s.NewMockK8sWrapper(ctrl),
				}

				h := &NodeUpdateWebhook{
					decoder:   decoder,
					Log:       zap.New(),
					Condition: mock.MockCondition,
					client:    mock.MockClient,
				}

				if tt.mockInvocation != nil {
					tt.mockInvocation(mock)
				}

				testPod := &corev1.Pod{}
				retry := 1
				if tt.retriable {
					retry = 4
				}
				start := time.Now()
				// Retriable error will retry four times with exponential backoff
				// otherwise the function GetPod should be called only once
				mock.MockClient.EXPECT().GetPod(awsPodName, config.KubeSystemNamespace).Return(testPod, tt.err).Times(retry)

				got := h.Handle(ctx, req)
				timeElapsed := time.Since(start).Seconds()
				assert.Equal(t, tt.want.Allowed, got.Allowed)
				assert.Equal(t, tt.want.Result.Status, got.Result.Status)
				assert.True(t, timeElapsed < 5*60, "Webhook retry time has to be less than 5 seconds(vnode timeout)")
			}
		})
	}
}
