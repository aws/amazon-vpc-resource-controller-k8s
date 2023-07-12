package crds

import (
	"testing"

	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_node "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node"
	mock_manager "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/node/manager"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

var (
	mockNodeName     = "node-name"
	reconcileRequest = reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name: mockNodeName,
		},
	}
	mockNodeObj = &v1.Node{
		ObjectMeta: metaV1.ObjectMeta{
			Name: mockNodeName,
		},
	}
)

type CNINodeMock struct {
	Manager    *mock_manager.MockManager
	MockNode   *mock_node.MockNode
	Controller CNINodeController
	MockK8sAPI *mock_k8s.MockK8sWrapper
	Scheme     runtime.Scheme
}

func NewCNINodeMock(ctrl *gomock.Controller, mockObjects ...runtime.Object) CNINodeMock {
	mockManager := mock_manager.NewMockManager(ctrl)
	mockNode := mock_node.NewMockNode(ctrl)

	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)
	return CNINodeMock{
		Manager:  mockManager,
		MockNode: mockNode,
		Controller: CNINodeController{
			Log:         zap.New(),
			NodeManager: mockManager,
		},
		Scheme: *scheme,
	}
}

func TestSetupWithManager(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	testenv := &envtest.Environment{}
	cfg, err := testenv.Start()
	assert.NoError(t, err)

	mock := NewCNINodeMock(ctrl, mockNodeObj)
	m, err := manager.New(cfg, manager.Options{Scheme: &mock.Scheme})
	assert.NoError(t, err)
	err = mock.Controller.SetupWithManager(m)
	assert.NoError(t, err)
}
