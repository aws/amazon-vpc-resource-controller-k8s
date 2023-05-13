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

package ip

import (
	"fmt"
	"reflect"
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
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	nodeName     = "node-1"
	instanceType = "t3.medium"

	ip1 = "192.168.1.1"
	ip2 = "192.168.1.2"
	ip3 = "192.168.1.3"

	nodeCapacity = 14
	isPDEnabled  = false
)

// TestIpv4Provider_difference tests difference removes the difference between an array and a set
func TestIpv4Provider_difference(t *testing.T) {
	allIPs := []string{ip1, ip2, ip3}
	usedIPSet := map[string]struct{}{ip3: {}, ip2: {}}

	unusedIPs := difference(allIPs, usedIPSet)
	assert.Equal(t, []string{ip1}, unusedIPs)
}

// TestIpv4Provider_no_difference tests that an empty slice is returned if there is no difference
func TestIpv4Provider_no_difference(t *testing.T) {
	allIPs := []string{ip1, ip2}
	usedIPSet := map[string]struct{}{ip1: {}, ip2: {}}

	unusedIPs := difference(allIPs, usedIPSet)
	assert.Empty(t, unusedIPs)
}

// TestNewIPv4Provider_getCapacity tests capacity of different os type
func TestNewIPv4Provider_getCapacity(t *testing.T) {
	capacityLinux := getCapacity(instanceType, config.OSLinux)
	capacityWindows := getCapacity(instanceType, config.OSWindows)
	capacityUnknown := getCapacity("x.large", "linux")

	assert.Zero(t, capacityUnknown)
	// IP(6) - 1(Primary) = 5
	assert.Equal(t, 5, capacityWindows)
	// (IP(6) - 1(Primary)) * 3(ENI) = 15
	assert.Equal(t, 15, capacityLinux)
}

// TestNewIPv4Provider_deleteInstanceProviderAndPool tests that the ResourcePoolAndProvider for given node is removed from
// cache after calling the API
func TestNewIPv4Provider_deleteInstanceProviderAndPool(t *testing.T) {
	ipProvider := getMockIpProvider()
	ipProvider.instanceProviderAndPool[nodeName] = &ResourceProviderAndPool{}
	ipProvider.deleteInstanceProviderAndPool(nodeName)
	assert.NotContains(t, ipProvider.instanceProviderAndPool, nodeName)
}

// TestNewIPv4Provider_getInstanceProviderAndPool tests if the resource pool and provider is present in cache it's returned
func TestNewIPv4Provider_getInstanceProviderAndPool(t *testing.T) {
	ipProvider := getMockIpProvider()
	resourcePoolAndProvider := &ResourceProviderAndPool{}
	ipProvider.instanceProviderAndPool[nodeName] = resourcePoolAndProvider
	result, found := ipProvider.getInstanceProviderAndPool(nodeName)

	assert.True(t, found)
	assert.Equal(t, resourcePoolAndProvider, result)
}

// TestIpv4Provider_putInstanceProviderAndPool tests put stores teh resource pool and provider into the cache
func TestIpv4Provider_putInstanceProviderAndPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)

	ipProvider := getMockIpProvider()
	ipProvider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)

	assert.Equal(t, &ResourceProviderAndPool{resourcePool: mockPool, eniManager: mockManager, capacity: nodeCapacity, isPrevPDEnabled: isPDEnabled}, ipProvider.instanceProviderAndPool[nodeName])
}

// TestIpv4Provider_updatePoolAndReconcileIfRequired_NoFurtherReconcile tests pool is updated and reconciliation is not
// performed again
func TestIpv4Provider_updatePoolAndReconcileIfRequired_NoFurtherReconcile(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWorker := mock_worker.NewMockWorker(ctrl)
	mockPool := mock_pool.NewMockPool(ctrl)
	provider := ipv4Provider{workerPool: mockWorker}

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
	provider := ipv4Provider{workerPool: mockWorker}

	job := &worker.WarmPoolJob{Operations: worker.OperationCreate}

	mockPool.EXPECT().UpdatePool(job, true).Return(true)
	mockPool.EXPECT().ReconcilePool().Return(job)
	mockWorker.EXPECT().SubmitJob(job)

	provider.updatePoolAndReconcileIfRequired(mockPool, job, true)
}

// TestIpv4Provider_DeletePrivateIPv4AndUpdatePool tests job with empty resources is passed back if some of the resource
// fail to delete
func TestIpv4Provider_DeletePrivateIPv4AndUpdatePool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ipv4Provider := getMockIpProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)
	resourcesToDelete := []string{ip1, ip2}

	deleteJob := &worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     resourcesToDelete,
		ResourceCount: 2,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().DeleteIPV4Resource(resourcesToDelete, config.ResourceTypeIPv4Address, nil, gomock.Any()).Return([]string{}, nil)
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     []string{},
		ResourceCount: 2,
		NodeName:      nodeName,
	}, true).Return(false)

	ipv4Provider.DeletePrivateIPv4AndUpdatePool(deleteJob)
}

// TestIpv4Provider_DeletePrivateIPv4AndUpdatePool_SomeResourceFail tests if some resource fail to delete those resources
// are passed back inside the job to the resource pool
func TestIpv4Provider_DeletePrivateIPv4AndUpdatePool_SomeResourceFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ipv4Provider := getMockIpProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)
	resourcesToDelete := []string{ip1, ip2}
	failedResources := []string{ip2}

	deleteJob := worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     resourcesToDelete,
		ResourceCount: 2,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().DeleteIPV4Resource(resourcesToDelete, config.ResourceTypeIPv4Address, nil, gomock.Any()).Return(failedResources, nil)
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationDeleted,
		Resources:     failedResources,
		ResourceCount: 2,
		NodeName:      nodeName,
	}, true).Return(false)

	ipv4Provider.DeletePrivateIPv4AndUpdatePool(&deleteJob)
}

// TestIPv4Provider_CreatePrivateIPv4AndUpdatePool tests if resources are created then the job object is updated
// with the resources and the pool is updated
func TestIPv4Provider_CreatePrivateIPv4AndUpdatePool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ipv4Provider := getMockIpProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)
	createdResources := []string{ip1, ip2}

	createJob := &worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     []string{},
		ResourceCount: 2,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().CreateIPV4Resource(2, config.ResourceTypeIPv4Address, nil, gomock.Any()).Return(createdResources, nil)
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     createdResources,
		ResourceCount: 2,
		NodeName:      nodeName,
	}, true).Return(false)

	ipv4Provider.CreatePrivateIPv4AndUpdatePool(createJob)
}

// TestIPv4Provider_CreatePrivateIPv4AndUpdatePool_Fail tests that if some of the create fails then the pool is
// updated with the created resource and success status as false
func TestIPv4Provider_CreatePrivateIPv4AndUpdatePool_Fail(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ipv4Provider := getMockIpProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)
	createdResources := []string{ip1, ip2}

	createJob := &worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     []string{},
		ResourceCount: 2,
		NodeName:      nodeName,
	}

	mockManager.EXPECT().CreateIPV4Resource(2, config.ResourceTypeIPv4Address, nil, gomock.Any()).Return(createdResources, fmt.Errorf("failed"))
	mockPool.EXPECT().UpdatePool(&worker.WarmPoolJob{
		Operations:    worker.OperationCreate,
		Resources:     createdResources,
		ResourceCount: 2,
		NodeName:      nodeName,
	}, false).Return(false)

	ipv4Provider.CreatePrivateIPv4AndUpdatePool(createJob)
}

func TestIpv4Provider_ReSyncPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ipv4Provider := getMockIpProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)
	resources := []string{ip1, ip2}

	reSyncJob := &worker.WarmPoolJob{
		Operations: worker.OperationReSyncPool,
		NodeName:   nodeName,
	}

	// When error occurs, pool should not be re-synced
	mockManager.EXPECT().InitResources(ipv4Provider.apiWrapper.EC2API).Return(nil, fmt.Errorf(""))
	ipv4Provider.ReSyncPool(reSyncJob)

	// When no error occurs, pool should be re-synced
	ipV4Resources := &eni.IPv4Resource{PrivateIPv4Addresses: resources}
	mockManager.EXPECT().InitResources(ipv4Provider.apiWrapper.EC2API).Return(ipV4Resources, nil)
	mockPool.EXPECT().ReSync(resources)
	ipv4Provider.ReSyncPool(reSyncJob)
}

// TestIPv4Provider_SubmitAsyncJob tests that the job is submitted to the worker on calling SubmitAsyncJob
func TestIPv4Provider_SubmitAsyncJob(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockWorker := mock_worker.NewMockWorker(ctrl)
	ipv4Provider := ipv4Provider{workerPool: mockWorker}

	job := worker.NewWarmPoolDeleteJob(nodeName, nil)

	mockWorker.EXPECT().SubmitJob(job)

	ipv4Provider.SubmitAsyncJob(job)
}

// TestIPv4Provider_UpdateResourceCapacity_FromFromPDToIP tests the warm pool is set to active when secondary IP mode is enabled and
// resource capacity is updated by calling the k8s wrapper
func TestIPv4Provider_UpdateResourceCapacity_FromFromPDToIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	mockWorker := mock_worker.NewMockWorker(ctrl)
	ipV4WarmPoolConfig := config.WarmPoolConfig{
		DesiredSize:  config.IPv4DefaultWPSize,
		MaxDeviation: config.IPv4DefaultMaxDev,
		ReservedSize: config.IPv4DefaultResSize,
	}
	ipv4Provider := ipv4Provider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper}, workerPool: mockWorker, config: &ipV4WarmPoolConfig,
		instanceProviderAndPool: map[string]*ResourceProviderAndPool{}, log: zap.New(zap.UseDevMode(true)).WithName("ip provider"), conditions: mockConditions}

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, !isPDEnabled)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(false)

	job := &worker.WarmPoolJob{Operations: worker.OperationCreate}
	mockPool.EXPECT().SetToActive(&ipV4WarmPoolConfig).Return(job)
	mockWorker.EXPECT().SubmitJob(job)

	mockInstance.EXPECT().Name().Return(nodeName).Times(3)
	mockInstance.EXPECT().Type().Return(instanceType)
	mockInstance.EXPECT().Os().Return(config.OSWindows)
	mockK8sWrapper.EXPECT().AdvertiseCapacityIfNotSet(nodeName, config.ResourceNameIPAddress, 14).Return(nil)

	err := ipv4Provider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestIPv4Provider_UpdateResourceCapacity_FromFromIPToPD tests the warm pool is drained when PD is enabled
func TestIPv4Provider_UpdateResourceCapacity_FromFromIPToPD(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	mockWorker := mock_worker.NewMockWorker(ctrl)
	ipv4Provider := ipv4Provider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper}, workerPool: mockWorker,
		instanceProviderAndPool: map[string]*ResourceProviderAndPool{}, log: zap.New(zap.UseDevMode(true)).WithName("ip provider"), conditions: mockConditions}

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(true)

	job := &worker.WarmPoolJob{Operations: worker.OperationDeleted}
	mockPool.EXPECT().SetToDraining().Return(job)
	mockWorker.EXPECT().SubmitJob(job)
	mockInstance.EXPECT().Name().Return(nodeName).Times(1)

	err := ipv4Provider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestIPv4Provider_UpdateResourceCapacity_FromPDToPD tests the resource capacity is not updated when PD mode stays enabled
func TestIPv4Provider_UpdateResourceCapacity_FromPDToPD(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	ipv4Provider := ipv4Provider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper}, instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log: zap.New(zap.UseDevMode(true)).WithName("ip provider"), conditions: mockConditions}

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	mockInstance.EXPECT().Name().Return(nodeName).Times(1)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, !isPDEnabled)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(true)

	err := ipv4Provider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

// TestIPv4Provider_UpdateResourceCapacity_FromIPToIP tests the resource capacity is not updated when secondary IP mode stays enabled
func TestIPv4Provider_UpdateResourceCapacity_FromIPToIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockInstance := mock_ec2.NewMockEC2Instance(ctrl)
	mockK8sWrapper := mock_k8s.NewMockK8sWrapper(ctrl)
	mockConditions := mock_condition.NewMockConditions(ctrl)
	ipv4Provider := ipv4Provider{apiWrapper: api.Wrapper{K8sAPI: mockK8sWrapper}, instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log: zap.New(zap.UseDevMode(true)).WithName("ip provider"), conditions: mockConditions}

	mockPool := mock_pool.NewMockPool(ctrl)
	mockManager := mock_eni.NewMockENIManager(ctrl)
	mockInstance.EXPECT().Name().Return(nodeName).Times(1)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, mockManager, nodeCapacity, isPDEnabled)
	mockConditions.EXPECT().IsWindowsPrefixDelegationEnabled().Return(false)

	err := ipv4Provider.UpdateResourceCapacity(mockInstance)
	assert.NoError(t, err)
}

func TestIpv4Provider_GetPool(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ipv4Provider := getMockIpProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, nil, nodeCapacity, isPDEnabled)

	pool, found := ipv4Provider.GetPool(nodeName)
	assert.True(t, found)
	assert.Equal(t, mockPool, pool)
}

func TestIpv4Provider_Introspect(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ipv4Provider := getMockIpProvider()
	mockPool := mock_pool.NewMockPool(ctrl)
	ipv4Provider.putInstanceProviderAndPool(nodeName, mockPool, nil, nodeCapacity, isPDEnabled)
	expectedResp := pool.IntrospectResponse{}

	mockPool.EXPECT().Introspect().Return(expectedResp)
	resp := ipv4Provider.Introspect()
	assert.True(t, reflect.DeepEqual(resp, map[string]pool.IntrospectResponse{nodeName: expectedResp}))

	mockPool.EXPECT().Introspect().Return(expectedResp)
	resp = ipv4Provider.IntrospectNode(nodeName)
	assert.Equal(t, resp, expectedResp)

	resp = ipv4Provider.IntrospectNode("unregistered-node")
	assert.Equal(t, resp, struct{}{})
}

func getMockIpProvider() ipv4Provider {
	return ipv4Provider{instanceProviderAndPool: map[string]*ResourceProviderAndPool{},
		log: zap.New(zap.UseDevMode(true)).WithName("ip provider")}
}
