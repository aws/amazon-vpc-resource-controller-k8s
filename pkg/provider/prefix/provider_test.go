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

package prefix

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"

	mock_ec2 "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2"
	mock_condition "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/condition"
	mock_k8s "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/provider/ip/eni"
	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/worker"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/ip/eni"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	nodeName     = "node-1"
	instanceType = "t3.medium"

	prefix1 = "192.168.1.0/28"
	prefix2 = "192.168.2.0/28"

	nodeCapacity = 224

	pdWarmPoolConfig = &config.WarmPoolConfig{
		DesiredSize:      config.IPv4PDDefaultWPSize,
		MaxDeviation:     config.IPv4PDDefaultMaxDev,
		WarmIPTarget:     config.IPv4PDDefaultWarmIPTargetSize,
		MinIPTarget:      config.IPv4PDDefaultMinIPTargetSize,
		WarmPrefixTarget: config.IPv4PDDefaultWarmPrefixTargetSize,
	}

	vpcCNIConfig = &v1.ConfigMap{
		Data: map[string]string{
			config.EnableWindowsIPAMKey:             "true",
			config.EnableWindowsPrefixDelegationKey: "true",
			config.WarmIPTarget:                     strconv.Itoa(config.IPv4PDDefaultWarmIPTargetSize),
			config.MinimumIPTarget:                  strconv.Itoa(config.IPv4PDDefaultMinIPTargetSize),
			config.WarmPrefixTarget:                 strconv.Itoa(config.IPv4PDDefaultWarmPrefixTargetSize),
		},
	}
)

// TestNewIPv4PrefixProvider_getCapacity tests capacity of different os type
func TestNewIPv4PrefixProvider_getCapacity(t *testing.T) {
	capacityLinux := getCapacity(instanceType, config.OSLinux)
	capacityWindows := getCapacity(instanceType, config.OSWindows)
	capacityUnknown := getCapacity("x.large", "linux")

	assert.Zero(t, capacityUnknown)
	// IP(6) - 1(Primary) = 5
	assert.Equal(t, 5, capacityWindows)
	// (IP(6) - 1(Primary)) * 3(ENI) = 15
	assert.Equal(t, 15, capacityLinux)
}

// TestNewIPv4PrefixProvider_deleteInstanceProviderAndPool tests that the ResourcePoolAndProvider for given node is removed from
// cache after calling the API
func TestNewIPv4PrefixProvider_deleteInstanceProviderAndPool(t *testing.T) {
	prefixProvider := getMockIPv4PrefixProvider()
	prefixProvider.instanceProviderAndPool[nodeName] = &ResourceProviderAndPool{}
	prefixProvider.deleteInstanceProviderAndPool(nodeName)
	assert.NotContains(t, prefixProvider.instanceProviderAndPool, nodeName)
}

// TestNewIPv4PrefixProvider_getInstanceProviderAndPool tests if the resource pool and provider is present in cache
func TestNewIPv4PrefixProvider_getInstanceProviderAndPool(t *testing.T) {
	prefixProvider := getMockIPv4PrefixProvider()
	resourcePoolAndProvider := &ResourceProviderAndPool{}
	prefixProvider.instanceProviderAndPool[nodeName] = resourcePoolAndProvider
	result, found := prefixProvider.getInstanceProviderAndPool(nodeName)

	assert.True(t, found)
	assert.Equal(t, resourcePoolAndProvider, result)
}

// TestIpv4PrefixProvider_putInstanceProviderAndPool tests put stores the resource pool and provider into the cache
func TestIpv4PrefixProvider_putInstanceProviderAndPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)

	prefixProvider := getMockIPv4PrefixProvider()
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)

	assert.Equal(t, &ResourceProviderAndPool{resourcePool: mockPool, eniManager: mockManager, capacity: nodeCapacity, isPrevPDEnabled: true},
		prefixProvider.instanceProviderAndPool[nodeName])
}

// TestIpv4PrefixProvider_updatePoolAndReconcileIfRequired_NoFurtherReconcile tests pool is updated and reconciliation is not
// performed again
func TestIpv4PrefixProvider_updatePoolAndReconcileIfRequired_NoFurtherReconcile(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWorker := mock_worker.NewMockWorker(ctrl)
	mockPool := mock_pool.NewMockPool(ctrl)
	provider := ipv4PrefixProvider{workerPool: mockWorker}

	job := &worker.WarmPoolJob{Operations: worker.OperationCreate}

	mockPool.EXPECT().UpdatePool(job, true).Return(false)

	provider.updatePoolAndReconcileIfRequired(mockPool, job, true)
}

// TestIpv4Provider_updatePoolAndReconcileIfRequired_ReconcileRequired tests pool is updated and reconciliation is
// performed again and the job submitted to the worker
func TestIpv4Provider_updatePoolAndReconcileIfRequired_ReconcileRequired(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWorker := mock_worker.NewMockWorker(ctrl)
	mockPool := mock_pool.NewMockPool(ctrl)
	provider := ipv4PrefixProvider{workerPool: mockWorker}

	job := &worker.WarmPoolJob{Operations: worker.OperationCreate}

	mockPool.EXPECT().UpdatePool(job, true).Return(true)
	mockPool.EXPECT().ReconcilePool().Return(job)
	mockWorker.EXPECT().SubmitJob(job)

	provider.updatePoolAndReconcileIfRequired(mockPool, job, true)
}

// TestIpv4PrefixProvider_DeleteIPv4PrefixAndUpdatePool tests job with empty resources is passed back if some resource
// fail to delete
func TestIpv4PrefixProvider_DeleteIPv4PrefixAndUpdatePool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	provider := getMockIPv4PrefixProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	resourcesToDelete := []string{prefix1, prefix2}

	deleteJob := &worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     resourcesToDelete,
		ResourceCount: 2,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().DeleteIPV4Resource(resourcesToDelete, config.ResourceTypeIPv4Prefix, nil, gomock.Any()).Return([]string{}, nil)
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     []string{},
		ResourceCount: 2,
		NodeName:      nodeName,
	}, true).Return(false)

	provider.DeleteIPv4PrefixAndUpdatePool(deleteJob)
}

// TestIpv4PrefixProvider_DeletePrivateIPv4AndUpdatePool_SomeResourceFail tests if some resource fail to delete those resources
// are passed back inside the job to the resource pool
func TestIpv4PrefixProvider_DeletePrivateIPv4AndUpdatePool_SomeResourceFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prefixProvider := getMockIPv4PrefixProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	resourcesToDelete := []string{prefix1, prefix2}
	failedResources := []string{prefix2}

	deleteJob := worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     resourcesToDelete,
		ResourceCount: 2,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().DeleteIPV4Resource(resourcesToDelete, config.ResourceTypeIPv4Prefix, nil, gomock.Any()).Return(failedResources, nil)
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     failedResources,
		ResourceCount: 2,
		NodeName:      nodeName,
	}, true).Return(false)

	prefixProvider.DeleteIPv4PrefixAndUpdatePool(&deleteJob)
}

// TestIPv4PrefixProvider_CreateIPv4PrefixAndUpdatePool tests if resources are created then the job object is updated
// with the resources and the pool is updated
func TestIPv4PrefixProvider_CreateIPv4PrefixAndUpdatePool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prefixProvider := getMockIPv4PrefixProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	createdResources := []string{prefix1, prefix2}

	createJob := &worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     []string{},
		ResourceCount: 2,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().CreateIPV4Resource(2, config.ResourceTypeIPv4Prefix, nil, gomock.Any()).Return(createdResources, nil)
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     createdResources,
		ResourceCount: 2,
		NodeName:      nodeName,
	}, true).Return(false)

	prefixProvider.CreateIPv4PrefixAndUpdatePool(createJob)
}

// TestIPv4PrefixProvider_CreateIPv4PrefixAndUpdatePool_Fail tests that if some create fails then the pool is
// updated with the created resources and success status as false
func TestIPv4PrefixProvider_CreateIPv4PrefixAndUpdatePool_Fail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prefixProvider := getMockIPv4PrefixProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	createdResources := []string{prefix1, prefix2}

	createJob := &worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     []string{},
		ResourceCount: 3,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().CreateIPV4Resource(3, config.ResourceTypeIPv4Prefix, nil, gomock.Any()).Return(createdResources,
		fmt.Errorf("failed"))
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     createdResources,
		ResourceCount: 3,
		NodeName:      nodeName,
	}, false).Return(false)

	prefixProvider.CreateIPv4PrefixAndUpdatePool(createJob)
}

// TestIPv4PrefixProvider_ReSyncPool
func TestIPv4PrefixProvider_ReSyncPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prefixProvider := getMockIPv4PrefixProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	resources := []string{prefix1, prefix2}

	reSyncJob := &worker.WarmPoolJob{
		Operations: worker.OperationReSyncPool,
		NodeName:   nodeName,
	}

	// When error occurs, pool should not be re-synced
	mockManager.EXPECT().InitResources(prefixProvider.apiWrapper.EC2API).Return(nil, fmt.Errorf(""))
	prefixProvider.ReSyncPool(reSyncJob)

	// When no error occurs, pool should be re-synced
	ipV4Resources := &eni.IPv4Resource{IPv4Prefixes: resources}
	mockManager.EXPECT().InitResources(prefixProvider.apiWrapper.EC2API).Return(ipV4Resources, nil)
	mockPool.EXPECT().ReSync(resources)
	prefixProvider.ReSyncPool(reSyncJob)
}

// TestIPv4PrefixProvider_SubmitAsyncJob tests that the job is submitted to the worker on calling SubmitAsyncJob
func TestIPv4PrefixProvider_SubmitAsyncJob(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWorker := mock_worker.NewMockWorker(ctrl)
	prefixProvider := ipv4PrefixProvider{workerPool: mockWorker}

	job := worker.NewWarmPoolDeleteJob(nodeName, nil)

	mockWorker.EXPECT().SubmitJob(job)

	prefixProvider.SubmitAsyncJob(job)
}

// TestIPv4PrefixProvider_UpdateResourceCapacity_FromFromIPToPD tests the warm pool is set to active when PD mode is enabled and
// resource capacity is updated by calling the k8s wrapper
func TestIPv4PrefixProvider_UpdateResourceCapacity_FromFromIPToPD(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	mockWorker := mock_worker.NewMockWorker(ctrl)

	// prefix provider starts with empty warm pool config since PD was disabled
	prefixProvider := ipv4PrefixProvider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper}, workerPool: mockWorker, config: pdWarmPoolConfig,
		instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log:                     zap.New(zap.UseDevMode(true)).WithName("prefix provider"), conditions: mockConditions}

	mockK8sWrapper.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(vpcCNIConfig, nil)
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(true)

	job := &worker.WarmPoolJob{Operations: worker.OperationCreate}
	mockPool.EXPECT().SetToActive(pdWarmPoolConfig).Return(job)
	mockWorker.EXPECT().SubmitJob(job)

	mockInstance.EXPECT().Name().Return(nodeName).Times(3)
	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().Os().Return(config.OSWindows)
	mockK8sWrapper.EXPECT().AdvertiseCapacityIfNotSet(nodeName, config.ResourceNameIPAddress, 224).Return(nil)

	err := prefixProvider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestIPv4PrefixProvider_UpdateResourceCapacity_FromFromPDToIP tests the warm pool is drained when PD is disabled
func TestIPv4PrefixProvider_UpdateResourceCapacity_FromFromPDToIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	mockWorker := mock_worker.NewMockWorker(ctrl)
	prefixProvider := ipv4PrefixProvider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper}, workerPool: mockWorker, config: pdWarmPoolConfig,
		instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log:                     zap.New(zap.UseDevMode(true)).WithName("prefix provider"), conditions: mockConditions}

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(false)

	job := &worker.WarmPoolJob{Operations: worker.OperationDeleted}
	mockPool.EXPECT().SetToDraining().Return(job)
	mockWorker.EXPECT().SubmitJob(job)
	mockInstance.EXPECT().Name().Return(nodeName).Times(1)

	err := prefixProvider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestIPv4PrefixProvider_UpdateResourceCapacity_FromPDToPD tests the resource capacity is not updated when PD mode stays enabled
func TestIPv4PrefixProvider_UpdateResourceCapacity_FromPDToPD(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	mockWorker := mock_worker.NewMockWorker(ctrl)

	prefixProvider := ipv4PrefixProvider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper}, workerPool: mockWorker, config: pdWarmPoolConfig,
		instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log:                     zap.New(zap.UseDevMode(true)).WithName("prefix provider"), conditions: mockConditions}

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, true)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(true)

	mockK8sWrapper.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(vpcCNIConfig, nil)

	job := &worker.WarmPoolJob{Operations: worker.OperationCreate}
	mockPool.EXPECT().SetToActive(pdWarmPoolConfig).Return(job)
	mockWorker.EXPECT().SubmitJob(job)

	mockInstance.EXPECT().Name().Return(nodeName).Times(3)
	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().Os().Return(config.OSWindows)
	mockK8sWrapper.EXPECT().AdvertiseCapacityIfNotSet(nodeName, config.ResourceNameIPAddress, 224).Return(nil)

	err := prefixProvider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestIPv4PrefixProvider_UpdateResourceCapacity_FromIPToIP tests the resource capacity is not updated when secondary IP mode stays enabled
func TestIPv4PrefixProvider_UpdateResourceCapacity_FromIPToIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	prefixProvider := ipv4PrefixProvider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper},
		instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log:                     zap.New(zap.UseDevMode(true)).WithName("prefix provider"), conditions: mockConditions}

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	mockInstance.EXPECT().Name().Return(nodeName).Times(1)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, false)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(false)

	err := prefixProvider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestIpv4PrefixProvider_GetPool
func TestIpv4PrefixProvider_GetPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prefixProvider := getMockIPv4PrefixProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, nil, nodeCapacity, true)

	pool, found := prefixProvider.GetPool(nodeName)
	assert.True(t, found)
	assert.Equal(t, mockPool, pool)
}

// TestIpv4PrefixProvider_Introspect
func TestIpv4PrefixProvider_Introspect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	prefixProvider := getMockIPv4PrefixProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	prefixProvider.putInstanceProviderAndPool(nodeName, mockPool, nil, nodeCapacity, true)
	expectedResp := pool.IntrospectResponse{}

	mockPool.EXPECT().Introspect().Return(expectedResp)
	resp := prefixProvider.Introspect()
	assert.True(t, reflect.DeepEqual(resp, map[string]pool.IntrospectResponse{nodeName: expectedResp}))

	mockPool.EXPECT().Introspect().Return(expectedResp)
	resp = prefixProvider.IntrospectNode(nodeName)
	assert.Equal(t, resp, expectedResp)

	resp = prefixProvider.IntrospectNode("unregistered-node")
	assert.Equal(t, resp, struct{}{})
}

func getMockIPv4PrefixProvider() ipv4PrefixProvider {
	return ipv4PrefixProvider{instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log: zap.New(zap.UseDevMode(true)).WithName("prefix provider")}
}

func TestGetPDWarmPoolConfig(t *testing.T) {
	// TODO
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	//mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	prefixProvider := ipv4PrefixProvider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper},
		instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log:                     zap.New(zap.UseDevMode(true)).WithName("prefix provider"), conditions: mockConditions}

	mockK8sWrapper.EXPECT().GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace).Return(vpcCNIConfig, nil)

	config := prefixProvider.getPDWarmPoolConfig()
	assert.Equal(t, pdWarmPoolConfig, config)
}

func TestIsInstanceSupported(t *testing.T) {
	// TODO
}
