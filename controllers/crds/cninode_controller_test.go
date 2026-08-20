package crds

import (
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_cleanup "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api/cleanup"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	ec2API "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api/cleanup"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

func NewCNINodeMock(ctrl *gomock.Controller, mockObjects ...client.Object) *CNINodeMock {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	client := fakeClient.NewClientBuilder().WithScheme(scheme).WithObjects(mockObjects...).Build()
	return &CNINodeMock{
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
		mockEC2API           *mock_api.MockEC2APIHelper
		mockCNINode          *CNINodeMock
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
		{
			name: "verify cleaner was not called if node id is not present given that node is being finalized",
			args: args{
				mockNode: nil,
				mockCNINode: &v1alpha1.CNINode{
					ObjectMeta: metav1.ObjectMeta{
						Name: mockName,
						Labels: map[string]string{
							config.NodeLabelOS: config.OSLinux,
						},
						Finalizers:        []string{config.NodeTerminationFinalizer},
						DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
					},
				},
			},
			prepare: func(f *fields) {
				f.mockCNINode.Reconciler.newResourceCleaner = func(nodeID string, eC2Wrapper ec2API.EC2Wrapper, vpcID string) cleanup.ResourceCleaner {
					return f.mockResourceCleaner
				}
				f.mockResourceCleaner.EXPECT().DeleteLeakedResources().Times(0)

				f.mockFinalizerManager.EXPECT().
					RemoveFinalizers(gomock.Any(), gomock.Any(), config.NodeTerminationFinalizer).
					Return(nil)
			},
			asserts: func(res reconcile.Result, err error, cniNode *v1alpha1.CNINode) {
				assert.NoError(t, err)
				assert.Equal(t, res, reconcile.Result{})
			},
		},
		{
			name: "verify cleaner was called if node id is not empty when node is being finalized",
			args: args{
				mockNode: nil,
				mockCNINode: &v1alpha1.CNINode{
					ObjectMeta: metav1.ObjectMeta{
						Name: mockName,
						Labels: map[string]string{
							config.NodeLabelOS: config.OSLinux,
						},
						Finalizers:        []string{config.NodeTerminationFinalizer},
						DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
					},
					Spec: v1alpha1.CNINodeSpec{
						Tags: map[string]string{
							config.NetworkInterfaceNodeIDKey: "i-1234567890",
						},
					},
				},
			},
			prepare: func(f *fields) {
				f.mockCNINode.Reconciler.newResourceCleaner = func(nodeID string, eC2Wrapper ec2API.EC2Wrapper, vpcID string) cleanup.ResourceCleaner {
					assert.Equal(t, "i-1234567890", nodeID)
					return f.mockResourceCleaner
				}
				f.mockResourceCleaner.EXPECT().DeleteLeakedResources().Times(1).Return(nil)
				f.mockFinalizerManager.EXPECT().
					RemoveFinalizers(gomock.Any(), gomock.Any(), config.NodeTerminationFinalizer).
					Return(nil)

			},
			asserts: func(res reconcile.Result, err error, cniNode *v1alpha1.CNINode) {
				assert.NoError(t, err)
				assert.Equal(t, res, reconcile.Result{})
			},
		},
		{
			name: "verify CNINode managed by another controller is skipped entirely",
			args: args{
				mockNode: mockNodeWithLabel,
				mockCNINode: &v1alpha1.CNINode{
					ObjectMeta: metav1.ObjectMeta{
						Name: mockName,
					},
					Spec: v1alpha1.CNINodeSpec{
						ManagedBy: v1alpha1.ManagedByEKSAutoMode,
					},
				},
			},
			prepare: nil,
			asserts: func(res reconcile.Result, err error, cniNode *v1alpha1.CNINode) {
				assert.NoError(t, err)
				assert.Equal(t, res, reconcile.Result{})
				// no tags, labels, or finalizer added by this controller
				assert.Empty(t, cniNode.Labels)
				assert.Empty(t, cniNode.Spec.Tags)
				assert.NotContains(t, cniNode.Finalizers, config.NodeTerminationFinalizer)
			},
		},
		{
			name: "verify empty managedBy is treated as vpc-resource-controller (backward compatibility)",
			args: args{
				mockNode: mockNodeWithLabel,
				mockCNINode: &v1alpha1.CNINode{
					ObjectMeta: metav1.ObjectMeta{
						Name: mockName,
						Labels: map[string]string{
							config.NodeLabelOS: "linux",
						},
					},
					Spec: v1alpha1.CNINodeSpec{
						Tags: map[string]string{
							config.VPCCNIClusterNameKey: mockClusterName,
						},
					},
				},
			},
			// This branch adds finalizers through the finalizer manager (master uses
			// controllerutil.AddFinalizer + patch); expecting the call proves the object
			// was reconciled as self-owned rather than skipped.
			prepare: func(f *fields) {
				f.mockFinalizerManager.EXPECT().
					AddFinalizers(gomock.Any(), gomock.Any(), config.NodeTerminationFinalizer).
					Return(nil)
			},
			asserts: func(res reconcile.Result, err error, cniNode *v1alpha1.CNINode) {
				assert.NoError(t, err)
				assert.Equal(t, res, reconcile.Result{})
			},
		},
		{
			name: "verify finalizer is added when labels and tags are present",
			args: args{
				mockNode: mockNodeWithLabel,
				mockCNINode: &v1alpha1.CNINode{
					ObjectMeta: metav1.ObjectMeta{
						Name: mockName,
						Labels: map[string]string{
							config.NodeLabelOS: "linux",
						},
					},
					Spec: v1alpha1.CNINodeSpec{
						Tags: map[string]string{
							config.VPCCNIClusterNameKey: mockClusterName,
						},
					},
				},
			},
			prepare: func(f *fields) {
				f.mockFinalizerManager.EXPECT().
					AddFinalizers(gomock.Any(), gomock.Any(), config.NodeTerminationFinalizer).
					Return(nil)
			},
			asserts: func(res reconcile.Result, err error, cniNode *v1alpha1.CNINode) {
				assert.NoError(t, err)
				assert.Equal(t, res, reconcile.Result{})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			objs := []client.Object{tt.args.mockCNINode}
			if tt.args.mockNode != nil {
				objs = append(objs, tt.args.mockNode)
			}
			mock := NewCNINodeMock(ctrl, objs...)
			f := fields{
				mockResourceCleaner:  mock_cleanup.NewMockResourceCleaner(ctrl),
				mockK8sApi:           mock_k8s.NewMockK8sWrapper(ctrl),
				mockFinalizerManager: mock_k8s.NewMockFinalizerManager(ctrl),
				mockEC2API:           mock_api.NewMockEC2APIHelper(ctrl),
				mockCNINode:          mock,
			}
			mock.Reconciler.finalizerManager = f.mockFinalizerManager
			mock.Reconciler.k8sAPI = f.mockK8sApi
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

// TestCNINodeReconcile_InstanceIDCollision proves the "bind" side of M6 (design-cn.md §2.7,
// §4.2 E7; issue #515): every CNINode reconcile with a live node compares the CNINode's
// checkpointed instance ID against the node's live instance ID (parsed from spec.providerID),
// increments cninode_instance_id_collision_total{source="bind"} on a mismatch, leaves it
// untouched on a match, and skips silently (no error, no metric change) whenever either side of
// the comparison is unavailable.
func TestCNINodeReconcile_InstanceIDCollision(t *testing.T) {
	const recordedInstanceID = "i-0aaaaaaaaaaaaaaaa"
	const liveInstanceID = "i-0bbbbbbbbbbbbbbbb"

	nodeWithProviderID := func(providerID string) *corev1.Node {
		return &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: mockName,
				Labels: map[string]string{
					config.NodeLabelOS: "linux",
				},
			},
			Spec: corev1.NodeSpec{
				ProviderID: providerID,
			},
		}
	}

	cniNodeWithCheckpoint := func(instanceID string) *v1alpha1.CNINode {
		cniNode := &v1alpha1.CNINode{
			ObjectMeta: metav1.ObjectMeta{
				Name: mockName,
				Labels: map[string]string{
					config.NodeLabelOS: "linux",
				},
			},
			Spec: v1alpha1.CNINodeSpec{
				Tags: map[string]string{
					config.VPCCNIClusterNameKey: mockClusterName,
				},
			},
		}
		if instanceID != "" {
			cniNode.Status = v1alpha1.CNINodeStatus{
				ReinitCheckpoint: &v1alpha1.ReinitCheckpoint{
					SnapshotVersion: v1alpha1.CNINodeStatusSnapshotVersion,
					Instance:        v1alpha1.InstanceStatus{InstanceID: instanceID},
				},
			}
		}
		return cniNode
	}

	tests := []struct {
		name      string
		node      *corev1.Node
		cniNode   *v1alpha1.CNINode
		wantDelta float64
	}{
		{
			name:      "mismatched instance ID increments the bind collision counter",
			node:      nodeWithProviderID("aws:///us-west-2a/" + liveInstanceID),
			cniNode:   cniNodeWithCheckpoint(recordedInstanceID),
			wantDelta: 1,
		},
		{
			name:      "matching instance ID does not increment the counter",
			node:      nodeWithProviderID("aws:///us-west-2a/" + recordedInstanceID),
			cniNode:   cniNodeWithCheckpoint(recordedInstanceID),
			wantDelta: 0,
		},
		{
			name:      "missing providerID skips the check",
			node:      nodeWithProviderID(""),
			cniNode:   cniNodeWithCheckpoint(recordedInstanceID),
			wantDelta: 0,
		},
		{
			name:      "unparseable providerID skips the check",
			node:      nodeWithProviderID("not-a-providerid"),
			cniNode:   cniNodeWithCheckpoint(recordedInstanceID),
			wantDelta: 0,
		},
		{
			name:      "no checkpoint yet skips the check",
			node:      nodeWithProviderID("aws:///us-west-2a/" + liveInstanceID),
			cniNode:   cniNodeWithCheckpoint(""),
			wantDelta: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mock := NewCNINodeMock(ctrl, tt.cniNode, tt.node)
			mockFinalizerManager := mock_k8s.NewMockFinalizerManager(ctrl)
			mockFinalizerManager.EXPECT().
				AddFinalizers(gomock.Any(), gomock.Any(), config.NodeTerminationFinalizer).
				Return(nil)
			mock.Reconciler.finalizerManager = mockFinalizerManager
			mock.Reconciler.k8sAPI = mock_k8s.NewMockK8sWrapper(ctrl)

			before := testutil.ToFloat64(ec2.InstanceIDCollisionCount.WithLabelValues(ec2.CollisionSourceBind))

			res, err := mock.Reconciler.Reconcile(context.Background(), reconcileRequest)
			assert.NoError(t, err)
			assert.Equal(t, reconcile.Result{}, res)

			after := testutil.ToFloat64(ec2.InstanceIDCollisionCount.WithLabelValues(ec2.CollisionSourceBind))
			assert.Equal(t, tt.wantDelta, after-before)
		})
	}
}
