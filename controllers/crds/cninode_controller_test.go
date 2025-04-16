package crds

import (
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_cleanup "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/cleanup"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type CNINodeMock struct {
	Reconciler CNINodeReconciler
}

var (
	mockName          = "node-name"
	mockClusterName   = "test-cluster"
	mockNodeWithLabel = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: mockName,
			Labels: map[string]string{
				config.NodeLabelOS: "linux",
			},
		},
	}
	reconcileRequest = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: mockName,
		},
	}
)

func NewCNINodeMock(ctrl *gomock.Controller, mockObjects ...client.Object) CNINodeMock {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	client := fakeClient.NewClientBuilder().WithScheme(scheme).WithObjects(mockObjects...).Build()
	return CNINodeMock{
		Reconciler: CNINodeReconciler{
			Client:      client,
			scheme:      scheme,
			log:         zap.New(),
			clusterName: mockClusterName,
			vpcId:       "vpc-000000000000",
		},
	}
}

func TestCNINodeReconcile(t *testing.T) {
	type args struct {
		mockNode    *corev1.Node
		mockCNINode *v1alpha1.CNINode
	}
	type fields struct {
		mockResourceCleaner  *mock_cleanup.MockResourceCleaner
		mockK8sApi           *mock_k8s.MockK8sWrapper
		mockFinalizerManager *mock_k8s.MockFinalizerManager
	}
	tests := []struct {
		name    string
		args    args
		prepare func(f *fields)
		asserts func(reconcile.Result, error, *v1alpha1.CNINode)
	}{
		{
			name: "verify clusterName tag and labels are added if missing",
			args: args{
				mockNode: mockNodeWithLabel,
				mockCNINode: &v1alpha1.CNINode{
					ObjectMeta: metav1.ObjectMeta{
						Name: mockName,
					},
				},
			},
			prepare: nil,
			asserts: func(res reconcile.Result, err error, cniNode *v1alpha1.CNINode) {
				assert.NoError(t, err)
				assert.Equal(t, res, reconcile.Result{})
				assert.Equal(t, cniNode.Labels, map[string]string{config.NodeLabelOS: "linux"})
				assert.Equal(t, cniNode.Spec.Tags, map[string]string{config.VPCCNIClusterNameKey: mockClusterName})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewCNINodeMock(ctrl, tt.args.mockNode, tt.args.mockCNINode)
			f := fields{
				mockResourceCleaner:  mock_cleanup.NewMockResourceCleaner(ctrl),
				mockK8sApi:           mock_k8s.NewMockK8sWrapper(ctrl),
				mockFinalizerManager: mock_k8s.NewMockFinalizerManager(ctrl),
			}
			if tt.prepare != nil {
				tt.prepare(&f)
			}
			res, err := mock.Reconciler.Reconcile(context.Background(), reconcileRequest)

			cniNode := &v1alpha1.CNINode{}
			getErr := mock.Reconciler.Client.Get(context.Background(), reconcileRequest.NamespacedName, cniNode)
			assert.NoError(t, getErr)

			if tt.asserts != nil {
				tt.asserts(res, err, cniNode)
			}
		})
	}

}
