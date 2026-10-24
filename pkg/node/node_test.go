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

package node

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	mock_provider "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider"
	mock_resource "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	"github.com/go-logr/logr/funcr"
	"github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakeClientSet "k8s.io/client-go/kubernetes/fake"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func writePrometheusMetric(t *testing.T, metric prometheus.Metric) *dto.Metric {
	t.Helper()
	value := &dto.Metric{}
	assert.NoError(t, metric.Write(value))
	return value
}

func TestCheckpointMetricsContract(t *testing.T) {
	restoreSeries := []struct {
		result string
		reason string
	}{
		{"hit", "none"},
		{"fallback", "read_error"},
		{"fallback", "foreign_manager"},
		{"fallback", "missing_state"},
		{"fallback", "missing_field"},
		{"fallback", "instance_id_mismatch"},
		{"fallback", "invalid_state"},
		{"error", "network_update_failed"},
	}
	for _, series := range restoreSeries {
		counter := cniNodeCheckpointRestoreCount.WithLabelValues(series.result, series.reason)
		assert.Contains(t, counter.Desc().String(), `fqName: "cninode_checkpoint_restore_total"`)
		assert.Contains(t, counter.Desc().String(), `variableLabels: {result,reason}`)
		before := writePrometheusMetric(t, counter).GetCounter().GetValue()
		cniNodeCheckpointRestoreCount.WithLabelValues(series.result, series.reason).Inc()
		assert.Equal(t, before+1, writePrometheusMetric(t, counter).GetCounter().GetValue())
	}

	for _, result := range []string{"success", "error"} {
		histogram := nodeInitDuration.WithLabelValues(result)
		metric := histogram.(prometheus.Metric)
		assert.Contains(t, metric.Desc().String(), `fqName: "node_init_duration_seconds"`)
		assert.Contains(t, metric.Desc().String(), `variableLabels: {result}`)
		before := writePrometheusMetric(t, metric).GetHistogram().GetSampleCount()
		histogram.Observe(0)
		assert.Equal(t, before+1, writePrometheusMetric(t, metric).GetHistogram().GetSampleCount())
	}
}

var (
	nodeName   = "node-name"
	instanceID = "i-00000000001"
	linux      = "linux"
	mockError  = fmt.Errorf("mock error")
	mockNode   = v1.Node{
		ObjectMeta: metaV1.ObjectMeta{
			Name: nodeName,
		},
	}
	nitroInstanceType     = "t3.xlarge"
	nonNitroInstanceType  = "c1.medium"
	bareMetalInstanceType = "c5.metal"
)

type Mocks struct {
	MockProviders       map[string]*mock_provider.MockResourceProvider
	ResourceProvider    map[string]provider.ResourceProvider
	MockResourceManager *mock_resource.MockResourceManager
	MockInstance        *mock_ec2.MockEC2Instance
	MockEC2API          *mock_api.MockEC2APIHelper
	MockK8sAPI          *mock_k8s.MockK8sWrapper
	NodeWithMock        node
}

func NewMock(ctrl *gomock.Controller, mockProviderCount int) Mocks {
	mockProviders := map[string]*mock_provider.MockResourceProvider{}
	convertedProvider := map[string]provider.ResourceProvider{}
	for i := 0; i < mockProviderCount; i++ {
		mockProvider := mock_provider.NewMockResourceProvider(ctrl)
		mockProviders[strconv.Itoa(i)] = mockProvider
		convertedProvider[strconv.Itoa(i)] = mockProvider
	}
	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockEC2API := mock_api.NewMockEC2APIHelper(ctrl)
	mockK8sAPI := mock_k8s.NewMockK8sWrapper(ctrl)

	return Mocks{
		MockProviders:       mockProviders,
		ResourceProvider:    convertedProvider,
		MockResourceManager: mock_resource.NewMockResourceManager(ctrl),
		MockEC2API:          mockEC2API,
		MockK8sAPI:          mockK8sAPI,
		MockInstance:        mockInstance,
		NodeWithMock: node{
			log:      zap.New(zap.UseDevMode(true)).WithName("branch provider"),
			instance: mockInstance,
			ec2API:   mockEC2API,
			k8sAPI:   mockK8sAPI,
		},
	}
}

func expectCheckpointRestore(mock *Mocks) {
	mock.MockInstance.EXPECT().Os().Return(config.OSLinux)
}

func expectColdInitialization(mock *Mocks) {
	mock.MockInstance.EXPECT().Os().Return(config.OSWindows)
}

// TestNewManagedNode tests the new node is not nil and node is managed but not ready
func TestNewManagedNode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	managedNode := NewManagedNode(zap.New(), nodeName, instanceID, linux, mock_k8s.NewMockK8sWrapper(ctrl), mock_api.NewMockEC2APIHelper(ctrl))

	assert.NotNil(t, managedNode)
	assert.True(t, managedNode.GetNodeInstanceID() == instanceID)
	assert.True(t, managedNode.IsManaged())
	assert.False(t, managedNode.IsReady())
	assert.Equal(t, config.OSLinux, managedNode.(*node).instance.Os())
}

func TestNewManagedWindowsNodePreservesOS(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	managedNode := NewManagedNode(zap.New(), nodeName, instanceID, "windows",
		mock_k8s.NewMockK8sWrapper(ctrl), mock_api.NewMockEC2APIHelper(ctrl))

	assert.Equal(t, config.OSWindows, managedNode.(*node).instance.Os())
}

// TestNewUnManagedNode tests the new node is not nil and node is not managed
func TestNewUnManagedNode(t *testing.T) {
	node := NewUnManagedNode(zap.New(), nodeName, instanceID, linux)

	assert.NotNil(t, node)
	assert.False(t, node.IsManaged())
	assert.False(t, node.IsReady())
	assert.True(t, node.GetNodeInstanceID() == instanceID)
}

// TestNode_InitResources tests the instance details is loaded and the node is initialized without error
func TestNode_InitResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	expectColdInitialization(&mock)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

func validCheckpointCNINode(checkpointInstanceID string) *rcv1alpha1.CNINode {
	return &rcv1alpha1.CNINode{
		Status: rcv1alpha1.CNINodeStatus{
			NodeNetworkState: &rcv1alpha1.NodeNetworkState{
				InstanceID:                checkpointInstanceID,
				InstanceType:              nitroInstanceType,
				SubnetID:                  "subnet-00000000000000000",
				SubnetCIDRBlock:           "10.0.0.0/24",
				PrimaryNetworkInterfaceID: "eni-00000000000000000",
			},
			TrunkInterface: &rcv1alpha1.TrunkInterface{
				ID: "eni-11111111111111111",
			},
		},
	}
}

// restoreAttempts sums every result and reason series of the restore counter.
func restoreAttempts(t *testing.T) float64 {
	t.Helper()
	metrics := make(chan prometheus.Metric, 16)
	cniNodeCheckpointRestoreCount.Collect(metrics)
	close(metrics)
	total := 0.0
	for metric := range metrics {
		total += writePrometheusMetric(t, metric).GetCounter().GetValue()
	}
	return total
}

// A usable checkpoint restores the instance without EC2; any other CNINode state falls back to EC2, counted by reason.
func TestNode_InitResources_RestoresCheckpointOrFallsBack(t *testing.T) {
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "cninodes"}, nodeName)
	tests := map[string]struct {
		mutate     func(*rcv1alpha1.CNINode)
		getErr     error
		loadErr    error
		updateErr  error
		wantResult string
		wantReason string
	}{
		"restored": {wantResult: "hit", wantReason: "none"},
		"restored with own manager": {
			mutate:     func(c *rcv1alpha1.CNINode) { c.Spec.ManagedBy = rcv1alpha1.ManagedByVPCResourceController },
			wantResult: "hit", wantReason: "none",
		},
		"no CNINode yet":  {getErr: notFound, wantResult: "fallback", wantReason: "missing_state"},
		"read error":      {getErr: mockError, wantResult: "fallback", wantReason: "read_error"},
		"foreign manager": {mutate: func(c *rcv1alpha1.CNINode) { c.Spec.ManagedBy = rcv1alpha1.ManagedByEKSAutoMode }, wantResult: "fallback", wantReason: "foreign_manager"},
		"no state":        {mutate: func(c *rcv1alpha1.CNINode) { *c = rcv1alpha1.CNINode{} }, wantResult: "fallback", wantReason: "missing_state"},
		// The shape a de-initialized node leaves behind: the trunk ID stays, the network state is cleared.
		"cleared network state": {mutate: func(c *rcv1alpha1.CNINode) { c.Status.NodeNetworkState = nil }, wantResult: "fallback", wantReason: "missing_state"},
		"instance mismatch":     {mutate: func(c *rcv1alpha1.CNINode) { c.Status.NodeNetworkState.InstanceID = "i-old" }, wantResult: "fallback", wantReason: "instance_id_mismatch"},
		"missing trunk":         {mutate: func(c *rcv1alpha1.CNINode) { c.Status.TrunkInterface = nil }, wantResult: "fallback", wantReason: "missing_field"},
		"missing instance type": {mutate: func(c *rcv1alpha1.CNINode) { c.Status.NodeNetworkState.InstanceType = "" }, wantResult: "fallback", wantReason: "missing_field"},
		"missing primary ENI": {
			mutate:     func(c *rcv1alpha1.CNINode) { c.Status.NodeNetworkState.PrimaryNetworkInterfaceID = "" },
			wantResult: "fallback", wantReason: "missing_field",
		},
		"invalid state":         {loadErr: mockError, wantResult: "fallback", wantReason: "invalid_state"},
		"network update failed": {updateErr: mockError, wantResult: "error", wantReason: "network_update_failed"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			mock := NewMock(ctrl, 1)
			var logged []string
			mock.NodeWithMock.log = funcr.New(func(_, args string) { logged = append(logged, args) }, funcr.Options{})
			expectCheckpointRestore(&mock)
			cniNode := validCheckpointCNINode(instanceID)
			if test.mutate != nil {
				test.mutate(cniNode)
			}
			if test.getErr != nil {
				cniNode = nil
			}
			mock.MockInstance.EXPECT().Name().Return(nodeName)
			mock.MockK8sAPI.EXPECT().GetCNINode(types.NamespacedName{Name: nodeName}).Return(cniNode, test.getErr)
			if test.getErr == nil && test.wantReason != "foreign_manager" {
				mock.MockInstance.EXPECT().InstanceID().Return(instanceID)
			}
			if test.wantReason == "none" || test.loadErr != nil || test.updateErr != nil {
				mock.MockInstance.EXPECT().LoadFromNodeNetworkState(*cniNode.Status.NodeNetworkState,
					cniNode.Status.TrunkInterface.ID).Return(test.loadErr)
			}
			if test.wantReason == "none" || test.updateErr != nil {
				mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.MockEC2API).Return(test.updateErr)
			}
			if test.wantResult == "fallback" {
				mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
			}
			if test.wantResult != "error" {
				mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)
				mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
				mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)
			}
			counter := cniNodeCheckpointRestoreCount.WithLabelValues(test.wantResult, test.wantReason)
			before, totalBefore := writePrometheusMetric(t, counter).GetCounter().GetValue(), restoreAttempts(t)

			err := mock.NodeWithMock.InitResources(mock.MockResourceManager)

			assert.Equal(t, test.wantResult == "error", err != nil)
			assert.Equal(t, test.wantResult != "error", mock.NodeWithMock.IsReady())
			assert.Equal(t, before+1, writePrometheusMetric(t, counter).GetCounter().GetValue())
			assert.Equal(t, totalBefore+1, restoreAttempts(t), "an attempt was counted under more than one reason")
			if test.getErr == mockError {
				assert.Contains(t, strings.Join(logged, "\n"), mockError.Error())
			}
		})
	}
}

// The Managed -> UnManaged -> Managed toggle through the real wrapper: the clear DeleteNode makes leaves
// a CNINode that the next init cold-inits from.
func TestNode_InitResources_ColdInitsAfterTheCheckpointIsCleared(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	scheme := runtime.NewScheme()
	assert.NoError(t, rcv1alpha1.AddToScheme(scheme))
	cniNode := validCheckpointCNINode(instanceID)
	cniNode.Name = nodeName
	fakeK8sClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&rcv1alpha1.CNINode{}).
		WithRuntimeObjects(cniNode).
		Build()
	k8sAPI := k8s.NewK8sWrapper(fakeK8sClient, fakeK8sClient, fakeClientSet.NewSimpleClientset().CoreV1(), context.Background())
	assert.NoError(t, k8sAPI.ClearCNINodeCheckpoint(nodeName))

	mock := NewMock(ctrl, 1)
	mock.NodeWithMock.k8sAPI = k8sAPI
	expectCheckpointRestore(&mock)
	mock.MockInstance.EXPECT().Name().Return(nodeName)
	mock.MockInstance.EXPECT().InstanceID().Return(instanceID)
	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil)

	assert.NoError(t, mock.NodeWithMock.InitResources(mock.MockResourceManager))
}

func TestNode_InitResources_InstanceNotTrunkSupported(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	expectColdInitialization(&mock)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(false)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NoError(t, err)
	assert.True(t, mock.NodeWithMock.IsReady())
}

func TestNode_InitResources_InstanceNotListed(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	expectColdInitialization(&mock)

	testInstanceType := "dummy.large"
	nodeName = "testInstance"
	node := &v1.Node{
		ObjectMeta: metaV1.ObjectMeta{Name: nodeName, UID: types.UID(nodeName)},
	}

	msg := "The instance type dummy.large is not supported yet by the vpc resource controller"

	mock.MockInstance.EXPECT().Type().Return(testInstanceType).Times(1)
	mock.MockInstance.EXPECT().Name().Return(nodeName).Times(1)
	mock.MockK8sAPI.EXPECT().GetNode(nodeName).Return(node, nil).Times(1)
	mock.MockK8sAPI.EXPECT().BroadcastEvent(node, "Unsupported", msg, v1.EventTypeWarning).Times(1)
	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(fmt.Errorf("unsupported instance type, couldn't find ENI Limit for instance %s, error: %w", testInstanceType, utils.ErrNotFound))

	mock.NodeWithMock.k8sAPI = mock.MockK8sAPI
	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.Error(t, err)
	assert.False(t, mock.NodeWithMock.IsReady())
}

// TestNode_InitResources_LoadInstanceDetails_Error tests that error is propagated when load instance details throws an error
func TestNode_InitResources_LoadInstanceDetails_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	expectColdInitialization(&mock)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(mockError)

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.Error(t, &ErrInitResources{Err: mockError}, err)
}

// TestNode_InitResources_SecondProviderInitFails tests when one of the resource provider fails to initialize
func TestNode_InitResources_SecondProviderInitFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)
	expectColdInitialization(&mock)

	mock.MockInstance.EXPECT().LoadDetails(mock.MockEC2API).Return(nil)
	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	// Second provider throws an error
	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true).AnyTimes()
	mock.MockProviders["0"].EXPECT().InitResource(mock.MockInstance).Return(nil).AnyTimes()

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true).AnyTimes()
	mock.MockProviders["1"].EXPECT().InitResource(mock.MockInstance).Return(mockError).AnyTimes()

	// Expect first provider to be de initialized
	mock.MockProviders["0"].EXPECT().DeInitResource(mock.MockInstance).Return(nil).AnyTimes()

	err := mock.NodeWithMock.InitResources(mock.MockResourceManager)
	assert.NotNil(t, err)
}

// TestNode_DeleteResources tests that delete resources doesn't return an error when all resources are deleted without error
func TestNode_DeleteResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["1"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.DeleteResources(mock.MockResourceManager)
	assert.NoError(t, err)
}

// TestNode_DeleteResources_SomeFail tests that delete returns an error when some of the resources fail to delete
func TestNode_DeleteResources_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 3)

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["1"].EXPECT().DeInitResource(mock.MockInstance).Return(mockError)

	mock.MockProviders["2"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["2"].EXPECT().DeInitResource(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.DeleteResources(mock.MockResourceManager)
	assert.NotNil(t, err)
}

// TestNode_UpdateResources tests that no error is returned when node is updated successfully
func TestNode_UpdateResources(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 2)
	mock.NodeWithMock.ready = true

	mock.MockInstance.EXPECT().UpdateCurrentSubnetAndCidrBlock(mock.MockEC2API).Return(nil)

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(false)

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager)
	assert.NoError(t, err)
}

// TestNode_UpdateResources_SomeFail tests that error is returned if some of the resource fail to advertise the capacity
func TestNode_UpdateResources_SomeFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 3)
	mock.NodeWithMock.ready = true

	mock.MockResourceManager.EXPECT().GetResourceProviders().Return(mock.ResourceProvider)

	mock.MockProviders["0"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["0"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(nil)

	mock.MockProviders["1"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["1"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(mockError)

	mock.MockProviders["2"].EXPECT().IsInstanceSupported(mock.MockInstance).Return(true)
	mock.MockProviders["2"].EXPECT().UpdateResourceCapacity(mock.MockInstance).Return(nil)

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager)
	assert.NotNil(t, err)
}

// TestNode_UpdateResources_NodeNotReady tests that if the node is not ready then update on resources
// is not invoked
func TestNode_UpdateResources_NodeNotReady(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)

	err := mock.NodeWithMock.UpdateResources(mock.MockResourceManager)
	assert.Nil(t, err)
}

// TestNode_IsNitroInstance_Nitro tests that if the node is nitro instance type, it should return true
func TestNode_IsNitroInstance_Nitro(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	mock.MockInstance.EXPECT().Type().Return(nitroInstanceType)

	assert.True(t, mock.NodeWithMock.IsNitroInstance())
}

// TestNode_IsNitroInstance_BareMetal tests that if the node is bare metal, which means it's built on nitro system, it should return true
func TestNode_IsNitroInstance_BareMetal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	mock.MockInstance.EXPECT().Type().Return(bareMetalInstanceType)

	assert.True(t, mock.NodeWithMock.IsNitroInstance())
}

// TestNode_IsNitroInstance_NonNitro tests that if the node is non-nitro instance type, it should return false
func TestNode_IsNitroInstance_NonNitro(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, 1)
	mock.MockInstance.EXPECT().Type().Return(nonNitroInstanceType)

	assert.False(t, mock.NodeWithMock.IsNitroInstance())
}
