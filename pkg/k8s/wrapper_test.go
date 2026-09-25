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

package k8s

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	mock_custom "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/controllers/custom"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	appV1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	fakeClientSet "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeClient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var (
	nodeName         = "node-name"
	mockClusterName  = "cluster-name"
	mockResourceName = config.ResourceNamePodENI

	existingResource         = "extended-resource"
	existingResourceQuantity = int64(5)
	mockNode                 = &v1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				config.NodeLabelOS: config.OSLinux,
			},
		},
		Spec: v1.NodeSpec{},
		Status: v1.NodeStatus{
			Capacity: v1.ResourceList{
				v1.ResourceName(existingResource): *resource.NewQuantity(existingResourceQuantity, resource.DecimalExponent),
			},
		},
	}
	mockDeployment = &appV1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.OldVPCControllerDeploymentName,
			Namespace: config.KubeSystemNamespace,
		},
	}

	mockDS = &appV1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.VpcCNIDaemonSetName,
			Namespace: config.KubeSystemNamespace,
		},
	}

	mockCNINode = &v1alpha1.CNINode{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
)

// getMockK8sWrapper returns the mock wrapper interface
func getMockK8sWrapperWithClient(ctrl *gomock.Controller, objs []runtime.Object) (K8sWrapper, client.Client,
	*mock_custom.MockController) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = appV1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	client := fakeClient.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	clientSet := fakeClientSet.NewSimpleClientset(mockNode)
	mockController := mock_custom.NewMockController(ctrl)

	return NewK8sWrapper(client, client, clientSet.CoreV1(), context.Background()), client, mockController
}

// TestK8sWrapper_AdvertiseCapacity tests that the capacity is advertised to the k8s node
func TestK8sWrapper_AdvertiseCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, k8sClient, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockNode, mockDeployment, mockDS})

	// Make node copy and ensure that the advertised capacity is 0
	testNode := mockNode.DeepCopy()
	quantity := testNode.Status.Capacity[v1.ResourceName(mockResourceName)]
	assert.True(t, quantity.IsZero())

	// Advertise capacity
	capacityToAdvertise := 10
	err := wrapper.AdvertiseCapacityIfNotSet(nodeName, mockResourceName, capacityToAdvertise)
	assert.NoError(t, err)

	// Get the node from the client and verify the capacity is set
	node := &v1.Node{}
	err = k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, node)

	// Verify no error and the capacity is set to the desired capacity
	assert.NoError(t, err)
	newQuantity := node.Status.Capacity[v1.ResourceName(mockResourceName)]
	assert.Equal(t, int64(capacityToAdvertise), newQuantity.Value())
}

// TestK8sWrapper_AdvertiseCapacity_Err tests that error is thrown when an error is encountered on the advertise resource
func TestK8sWrapper_AdvertiseCapacity_Err(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockNode, mockDeployment, mockDS})

	deletedNodeName := "deleted-node"
	err := wrapper.AdvertiseCapacityIfNotSet(deletedNodeName, mockResourceName, 10)
	assert.NotNil(t, err)
}

// TestK8sWrapper_AdvertiseCapacity_Node_Nil_Capacity tests when a node is returned with nil capacity map. Referring to issue #144
// https://github.com/aws/amazon-vpc-resource-controller-k8s/issues/144
func TestK8sWrapper_AdvertiseCapacity_Node_Nil_Capacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	mockErrNode := &v1.Node{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
		Spec: v1.NodeSpec{},
		Status: v1.NodeStatus{
			Capacity: nil,
		},
	}
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockErrNode})
	err := wrapper.AdvertiseCapacityIfNotSet(nodeName, mockResourceName, 10)
	assert.NotNil(t, err)
	assert.True(t, errors.IsConflict(err))
}

// TestK8sWrapper_AdvertiseCapacity_AlreadySet tests that if capacity of node is already set no error is thrown.
func TestK8sWrapper_AdvertiseCapacity_AlreadySet(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockNode, mockDeployment, mockDS})
	err := wrapper.AdvertiseCapacityIfNotSet(nodeName, existingResource, 5)

	capacity := mockNode.Status.Capacity[v1.ResourceName(existingResource)]
	assert.NoError(t, err)
	assert.Equal(t, existingResourceQuantity, capacity.Value())
}

func TestNewK8sWrapper_GetDaemonSet(t *testing.T) {
	ctrl := gomock.NewController(t)

	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockNode, mockDeployment, mockDS})
	ds, err := wrapper.GetDaemonSet(config.VpcCNIDaemonSetName, config.KubeSystemNamespace)

	assert.NoError(t, err)
	assert.Equal(t, config.VpcCNIDaemonSetName, ds.Name)
}

func TestNewK8sWrapper_GetDaemonSet_Err(t *testing.T) {
	ctrl := gomock.NewController(t)

	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockNode, mockDeployment, mockDS})
	_, err := wrapper.GetDaemonSet(config.VpcCNIDaemonSetName, "")
	assert.NotNil(t, err)
}

func TestK8sWrapper_GetDeployment(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockNode, mockDeployment, mockDS})

	deployment, err := wrapper.GetDeployment(config.KubeSystemNamespace,
		config.OldVPCControllerDeploymentName)
	assert.NoError(t, err)
	assert.Equal(t, deployment.ObjectMeta, mockDeployment.ObjectMeta)
}

func TestK8sWrapper_GetDeployment_Err(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockNode, mockDeployment, mockDS})

	_, err := wrapper.GetDeployment("default",
		config.OldVPCControllerDeploymentName)
	assert.Error(t, err)
}

func TestK8sWrapper_CreateCNINodeWithExistedObject_NoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockCNINode})

	err := wrapper.CreateCNINode(mockNode, mockClusterName)
	assert.NoError(t, err)
	cniNode, err := wrapper.GetCNINode(types.NamespacedName{Name: mockNode.Name})
	assert.NoError(t, err)
	assert.Equal(t, mockNode.Name, cniNode.Name)
	err = wrapper.CreateCNINode(mockNode, mockClusterName)
	assert.NoError(t, err)
}

func TestK8sWrapper_CreateCNINode_NoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	wrapper, _, _ := getMockK8sWrapperWithClient(ctrl, []runtime.Object{mockCNINode})

	err := wrapper.CreateCNINode(mockNode, mockClusterName)
	assert.NoError(t, err)
	cniNode, err := wrapper.GetCNINode(types.NamespacedName{Name: mockNode.Name})
	assert.NoError(t, err)
	assert.Equal(t, mockNode.Name, cniNode.Name)
}

func TestPatchCNINodeCheckpointPreservesExistingStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	existingTrunk := &v1alpha1.TrunkInterface{
		ID:          "eni-old",
		SubnetID:    "subnet-00000000000000000",
		DeviceIndex: 2,
		MacAddress:  "00:11:22:33:44:55",
		Branches: []v1alpha1.BranchInterface{
			{
				ID:            "eni-branch",
				VlanID:        7,
				AssociationID: "trunk-assoc-1",
			},
		},
	}
	cniNode := &v1alpha1.CNINode{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName},
		Status: v1alpha1.CNINodeStatus{
			NodeNetworkState: &v1alpha1.NodeNetworkState{
				InstanceID:   "i-old",
				InstanceType: "m5.large",
			},
			TrunkInterface: existingTrunk.DeepCopy(),
		},
	}
	k8sClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(cniNode).
		Build()
	wrapper := NewK8sWrapper(k8sClient, k8sClient, fakeClientSet.NewSimpleClientset().CoreV1(), context.Background())

	state := v1alpha1.NodeNetworkState{
		InstanceID:   "i-00000000000000000",
		InstanceType: "m6i.large",
	}
	assert.NoError(t, wrapper.PatchCNINodeCheckpoint(nodeName, state, "eni-00000000000000000"))

	stored := &v1alpha1.CNINode{}
	assert.NoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, stored))
	assert.Equal(t, state, *stored.Status.NodeNetworkState)
	assert.Equal(t, "eni-00000000000000000", stored.Status.TrunkInterface.ID)
	assert.Equal(t, existingTrunk.SubnetID, stored.Status.TrunkInterface.SubnetID)
	assert.Equal(t, existingTrunk.DeviceIndex, stored.Status.TrunkInterface.DeviceIndex)
	assert.Equal(t, existingTrunk.MacAddress, stored.Status.TrunkInterface.MacAddress)
	assert.Equal(t, existingTrunk.Branches, stored.Status.TrunkInterface.Branches)
}

func TestPatchCNINodeCheckpointUsesCachedReaderOnHit(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	baseClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}).
		Build()

	cacheGetCalls := 0
	cacheClient := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption,
		) error {
			cacheGetCalls++
			return c.Get(ctx, key, obj, opts...)
		},
	})

	apiReaderGetCalls := 0
	apiReader := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey,
			client.Object, ...client.GetOption,
		) error {
			apiReaderGetCalls++
			return fmt.Errorf("checkpoint read must use the cache client on a cache hit")
		},
	})

	wrapper := NewK8sWrapper(
		cacheClient,
		apiReader,
		fakeClientSet.NewSimpleClientset().CoreV1(),
		context.Background(),
	)

	state := v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"}
	assert.NoError(t, wrapper.PatchCNINodeCheckpoint(nodeName, state, "eni-00000000000000000"))
	assert.Equal(t, 1, cacheGetCalls)
	assert.Zero(t, apiReaderGetCalls)

	stored := &v1alpha1.CNINode{}
	assert.NoError(t, baseClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, stored))
	assert.Equal(t, state, *stored.Status.NodeNetworkState)
	assert.Equal(t, "eni-00000000000000000", stored.Status.TrunkInterface.ID)
}

func TestPatchCNINodeCheckpointCreatesTrunkInterfaceWhenMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	k8sClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}).
		Build()
	wrapper := NewK8sWrapper(k8sClient, k8sClient, fakeClientSet.NewSimpleClientset().CoreV1(), context.Background())

	state := v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"}
	assert.NoError(t, wrapper.PatchCNINodeCheckpoint(nodeName, state, "eni-00000000000000000"))

	stored := &v1alpha1.CNINode{}
	assert.NoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, stored))
	assert.Equal(t, state, *stored.Status.NodeNetworkState)
	assert.Equal(t, "eni-00000000000000000", stored.Status.TrunkInterface.ID)
}

func TestPatchCNINodeCheckpointNoopWhenUnchanged(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	state := v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"}
	patchCalls := 0
	k8sClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			Status: v1alpha1.CNINodeStatus{
				NodeNetworkState: state.DeepCopy(),
				TrunkInterface: &v1alpha1.TrunkInterface{
					ID:       "eni-00000000000000000",
					SubnetID: "subnet-00000000000000000",
				},
			},
		}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string,
				obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption,
			) error {
				patchCalls++
				return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()
	wrapper := NewK8sWrapper(k8sClient, k8sClient, fakeClientSet.NewSimpleClientset().CoreV1(), context.Background())

	assert.NoError(t, wrapper.PatchCNINodeCheckpoint(
		nodeName,
		state,
		"eni-00000000000000000",
	))
	assert.Zero(t, patchCalls)
}

func TestPatchCNINodeCheckpointFallsBackToAPIReaderOnCacheMiss(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	baseClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}).
		Build()

	cacheGetCalls := 0
	cacheClient := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, key client.ObjectKey,
			_ client.Object, _ ...client.GetOption,
		) error {
			cacheGetCalls++
			return errors.NewNotFound(schema.GroupResource{Resource: "cninodes"}, key.Name)
		},
	})

	apiReaderGetCalls := 0
	apiReader := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption,
		) error {
			apiReaderGetCalls++
			if apiReaderGetCalls == 1 {
				return errors.NewNotFound(schema.GroupResource{Resource: "cninodes"}, key.Name)
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	wrapper := NewK8sWrapper(
		cacheClient,
		apiReader,
		fakeClientSet.NewSimpleClientset().CoreV1(),
		context.Background(),
	)

	err := wrapper.PatchCNINodeCheckpoint(
		nodeName,
		v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"},
		"eni-00000000000000000",
	)

	assert.NoError(t, err)
	assert.Equal(t, 1, cacheGetCalls)
	assert.Equal(t, 2, apiReaderGetCalls)
}

func TestPatchCNINodeCheckpointFallsBackToAPIReaderOnConflict(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	baseClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}).
		Build()

	cacheGetCalls := 0
	patchCalls := 0
	cacheClient := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption,
		) error {
			cacheGetCalls++
			return c.Get(ctx, key, obj, opts...)
		},
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string,
			obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption,
		) error {
			patchCalls++
			patchData, err := patch.Data(obj)
			if err != nil {
				return err
			}
			assert.Contains(t, string(patchData), `"resourceVersion":`)
			if patchCalls == 1 {
				return errors.NewConflict(
					schema.GroupResource{Resource: "cninodes"},
					obj.GetName(),
					fmt.Errorf("stale resource version"),
				)
			}
			return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
	})

	apiReaderGetCalls := 0
	apiReader := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption,
		) error {
			apiReaderGetCalls++
			return c.Get(ctx, key, obj, opts...)
		},
	})

	wrapper := NewK8sWrapper(
		cacheClient,
		apiReader,
		fakeClientSet.NewSimpleClientset().CoreV1(),
		context.Background(),
	)

	err := wrapper.PatchCNINodeCheckpoint(
		nodeName,
		v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"},
		"eni-00000000000000000",
	)

	assert.NoError(t, err)
	assert.Equal(t, 2, patchCalls)
	assert.Equal(t, 1, cacheGetCalls)
	assert.Equal(t, 1, apiReaderGetCalls)
}

func TestPatchCNINodeCheckpointRetriesTransientPatchFromCache(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	baseClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}).
		Build()

	cacheGetCalls := 0
	patchCalls := 0
	cacheClient := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption,
		) error {
			cacheGetCalls++
			return c.Get(ctx, key, obj, opts...)
		},
		SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string,
			obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption,
		) error {
			patchCalls++
			if patchCalls == 1 {
				return errors.NewServiceUnavailable("unavailable")
			}
			return c.SubResource(subResourceName).Patch(ctx, obj, patch, opts...)
		},
	})

	apiReaderGetCalls := 0
	apiReader := interceptor.NewClient(baseClient, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey,
			obj client.Object, opts ...client.GetOption,
		) error {
			apiReaderGetCalls++
			return c.Get(ctx, key, obj, opts...)
		},
	})

	wrapper := NewK8sWrapper(
		cacheClient,
		apiReader,
		fakeClientSet.NewSimpleClientset().CoreV1(),
		context.Background(),
	)

	err := wrapper.PatchCNINodeCheckpoint(
		nodeName,
		v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"},
		"eni-00000000000000000",
	)

	assert.NoError(t, err)
	assert.Equal(t, 2, patchCalls)
	assert.Equal(t, 2, cacheGetCalls)
	assert.Zero(t, apiReaderGetCalls)
}

func TestPatchCNINodeCheckpointTransientFailureIsBounded(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	patchCalls := 0
	k8sClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(context.Context, client.Client, string,
				client.Object, client.Patch, ...client.SubResourcePatchOption,
			) error {
				patchCalls++
				return errors.NewServiceUnavailable("unavailable")
			},
		}).
		Build()
	wrapper := NewK8sWrapper(k8sClient, k8sClient, fakeClientSet.NewSimpleClientset().CoreV1(), context.Background())

	err := wrapper.PatchCNINodeCheckpoint(
		nodeName,
		v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"},
		"eni-00000000000000000",
	)

	assert.Error(t, err)
	assert.Equal(t, retry.DefaultBackoff.Steps, patchCalls)
}

func TestPatchCNINodeCheckpointSkipsOtherManager(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)

	existingStatus := v1alpha1.CNINodeStatus{
		NodeNetworkState: &v1alpha1.NodeNetworkState{InstanceID: "i-existing"},
		TrunkInterface: &v1alpha1.TrunkInterface{
			ID:       "eni-existing",
			SubnetID: "subnet-existing",
		},
	}
	k8sClient := fakeClient.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.CNINode{}).
		WithRuntimeObjects(&v1alpha1.CNINode{
			ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			Spec: v1alpha1.CNINodeSpec{
				ManagedBy: v1alpha1.ManagedByEKSAutoMode,
			},
			Status: existingStatus,
		}).
		Build()
	wrapper := NewK8sWrapper(k8sClient, k8sClient, fakeClientSet.NewSimpleClientset().CoreV1(), context.Background())

	assert.NoError(t, wrapper.PatchCNINodeCheckpoint(
		nodeName,
		v1alpha1.NodeNetworkState{InstanceID: "i-00000000000000000"},
		"eni-00000000000000000",
	))

	stored := &v1alpha1.CNINode{}
	assert.NoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, stored))
	assert.Equal(t, existingStatus, stored.Status)
}

func TestShouldRetryCNINodeStatusUpdate(t *testing.T) {
	for _, err := range []error{
		errors.NewNotFound(schema.GroupResource{Resource: "cninodes"}, nodeName),
		errors.NewConflict(schema.GroupResource{Resource: "cninodes"}, nodeName, fmt.Errorf("conflict")),
		errors.NewTimeoutError("timeout", 1),
		errors.NewServerTimeout(schema.GroupResource{Resource: "cninodes"}, "patch", 1),
		errors.NewTooManyRequests("throttled", 1),
		errors.NewServiceUnavailable("unavailable"),
		errors.NewInternalError(fmt.Errorf("internal")),
	} {
		assert.True(t, shouldRetryCNINodeStatusUpdate(err), err.Error())
	}

	for _, err := range []error{
		errors.NewUnauthorized("unauthorized"),
		errors.NewForbidden(schema.GroupResource{Resource: "cninodes"}, nodeName, fmt.Errorf("forbidden")),
	} {
		assert.False(t, shouldRetryCNINodeStatusUpdate(err), err.Error())
	}
}
