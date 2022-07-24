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
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/ipam"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/ipam/eni"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ipv4PrefixProvider struct {
	// log is the logger initialized with ip provider details
	log logr.Logger
	// apiWrapper wraps all clients used by the controller
	apiWrapper api.Wrapper
	// workerPool with worker routine to execute asynchronous job on the ip provider
	workerPool worker.Worker
	// config is the warm pool configuration for the resource IPv4
	config *config.WarmPoolConfig
	// lock to allow multiple routines to access the cache concurrently
	lock sync.RWMutex // guards the following
	// instanceResources stores the ENIManager and the resource pool per instance
	instanceProviderAndPool map[string]ResourceProviderAndIPAM
}

// InstanceResource contains the instance's ENI manager and the resource pool
type ResourceProviderAndIPAM struct {
	eniManager   eni.ENIManager
	resourceIpam ipam.Ipam // Change to IPAM
}

func NewIPv4PrefixProvider(log logr.Logger, apiWrapper api.Wrapper,
	workerPool worker.Worker, resourceConfig config.ResourceConfig) provider.ResourceProvider {
	return &ipv4PrefixProvider{
		instanceProviderAndPool: make(map[string]ResourceProviderAndIPAM),
		config:                  resourceConfig.WarmPoolConfig,
		log:                     log,
		apiWrapper:              apiWrapper,
		workerPool:              workerPool,
	}
}

func (i *ipv4PrefixProvider) InitResource(instance ec2.EC2Instance) error {
	nodeName := instance.Name()

	// Init IPAM to resync
	nodeCapacity := getCapacity(instance.Type(), instance.Os())

	ipamPool := ipam.NewResourceIPAM(i.log.WithName("ipv4 prefix resource pool").
		WithValues("node name", instance.Name()), i.config, make(map[string]worker.IPAMResourceInfo),
		[]worker.IPAMResourceInfo{}, []string{}, map[string]int{}, instance.Name(), nodeCapacity)

	_, eniManager, err := ipamPool.InitIPAM(instance, i.apiWrapper)

	if err != nil {
		i.log.Error(err, "Failed to initialize IPAM")
	}

	// Reconcile pool after starting up and submit the async job
	i.log.Info(err, "Failed to initialize IPAM")
	job := ipamPool.ReconcilePool()
	if job.Operations != worker.OperationReconcileNotRequired {
		i.SubmitAsyncJob(job)
	}

	i.putInstanceProviderAndPool(nodeName, ipamPool, eniManager)
	// Submit the async job to periodically process the delete queue
	i.SubmitAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(nodeName))
	return nil
}

func (i *ipv4PrefixProvider) DeInitResource(instance ec2.EC2Instance) error {
	nodeName := instance.Name()
	i.deleteInstanceProviderAndPool(nodeName)

	return nil
}

// UpdateResourceCapacity updates the resource capacity based on the type of instance
func (i *ipv4PrefixProvider) UpdateResourceCapacity(instance ec2.EC2Instance) error {
	instanceType := instance.Type()
	instanceName := instance.Name()
	os := instance.Os()

	capacity := getCapacity(instanceType, os)

	err := i.apiWrapper.K8sAPI.AdvertiseCapacityIfNotSet(instance.Name(), config.ResourceNameIPAddress, capacity)
	if err != nil {
		return err
	}
	i.log.V(1).Info("advertised capacity",
		"instance", instanceName, "instance type", instanceType, "os", os, "capacity", capacity)

	return nil
}

func (i *ipv4PrefixProvider) ProcessDeleteQueue(job *worker.WarmPoolJob) (ctrl.Result, error) {
	resourceProviderAndPool, isPresent := i.getInstanceProviderAndPool(job.NodeName)
	if !isPresent {
		i.log.Info("forgetting the delete queue processing job", "node", job.NodeName)
		return ctrl.Result{}, nil
	}
	// TODO: For efficiency run only when required in next release
	resourceProviderAndPool.resourceIpam.ProcessCoolDownQueue()

	// After the cool down queue is processed check if we need to do reconciliation
	job = resourceProviderAndPool.resourceIpam.ReconcilePool()
	if job.Operations != worker.OperationReconcileNotRequired {
		i.SubmitAsyncJob(job)
	}

	// Re submit the job to execute after cool down period has ended
	return ctrl.Result{Requeue: true, RequeueAfter: config.CoolDownPeriod}, nil
}

// SubmitAsyncJob submits an asynchronous job to the worker pool
func (i *ipv4PrefixProvider) SubmitAsyncJob(job interface{}) {
	i.workerPool.SubmitJob(job)
}

// ProcessAsyncJob processes the job, the function should be called using the worker pool in order to be processed
// asynchronously
func (i *ipv4PrefixProvider) ProcessAsyncJob(job interface{}) (ctrl.Result, error) {
	ipamJob, isValid := job.(*worker.WarmPoolJob)
	if !isValid {
		return ctrl.Result{}, fmt.Errorf("invalid job type")
	}

	switch ipamJob.Operations {
	case worker.OperationCreate:
		i.CreatePrivateIPv4PrefixAndUpdatePool(ipamJob)
	case worker.OperationDeleted:
		i.DeletePrivateIPv4AndUpdatePool(ipamJob)
	case worker.OperationReSyncPool:
		i.ReSyncPool(ipamJob)
	case worker.OperationProcessDeleteQueue:
		return i.ProcessDeleteQueue(ipamJob)
	}

	return ctrl.Result{}, nil
}

// CreatePrivateIPv4AndUpdatePool executes the Create IPv4 workflow by assigning the desired number of IPv4 address
// provided in the warm pool job
func (i *ipv4PrefixProvider) CreatePrivateIPv4PrefixAndUpdatePool(job *worker.WarmPoolJob) {
	instanceResource, found := i.getInstanceProviderAndPool(job.NodeName)
	if !found {
		i.log.Error(fmt.Errorf("cannot find the instance provider and pool form the cache"), "node", job.NodeName)
		return
	}
	didSucceed := true

	i.log.Info("Logging Debug")
	i.log.Info("Resource count", "Amount", job.ResourceCount)
	i.log.Info("API Wrapper", "API", i.apiWrapper)
	// Get ENI to create IPV4 prefix [Change feature]
	prefixes, didSucceed := instanceResource.resourceIpam.AllocatePrefix(job.ResourceCount, i.apiWrapper)

	job.Resources = prefixes

	i.updatePoolAndReconcileIfRequired(instanceResource.resourceIpam, job, didSucceed)
}

func (i *ipv4PrefixProvider) ReSyncPool(job *worker.WarmPoolJob) {
	providerAndPool, found := i.instanceProviderAndPool[job.NodeName]
	if !found {
		i.log.Error(fmt.Errorf("instance provider not found"), "node is not initialized",
			"name", job.NodeName)
		return
	}

	resources, err := providerAndPool.eniManager.InitResources(i.apiWrapper.EC2API)
	if err != nil {
		i.log.Error(err, "failed to get init resources for the node",
			"name", job.NodeName)
		return
	}

	providerAndPool.resourceIpam.ReSync(resources)
}

// DeletePrivateIPv4AndUpdatePool executes the Delete IPv4 workflow for the list of IPs provided in the warm pool job
func (i *ipv4PrefixProvider) DeletePrivateIPv4AndUpdatePool(job *worker.WarmPoolJob) {
	instanceResource, found := i.getInstanceProviderAndPool(job.NodeName)
	if !found {
		i.log.Error(fmt.Errorf("cannot find the instance provider and pool form the cache"), "node", job.NodeName)
		return
	}
	didSucceed := true
	failedIPs, err := instanceResource.eniManager.DeleteIPV4Prefix(job.Resources, i.apiWrapper.EC2API, i.log)
	if err != nil {
		i.log.Error(err, "failed to delete all/some of the IPv4 addresses", "failed ips", failedIPs)
		didSucceed = false
	}
	job.Resources = failedIPs
	i.updatePoolAndReconcileIfRequired(instanceResource.resourceIpam, job, didSucceed)
}

// updatePoolAndReconcileIfRequired updates the resource pool and reconcile again and submit a new job if required
func (i *ipv4PrefixProvider) updatePoolAndReconcileIfRequired(resourceIpam ipam.Ipam, job *worker.WarmPoolJob, didSucceed bool) {
	// Update the pool to add the created/failed resource to the warm pool and decrement the pending count
	shouldReconcile := resourceIpam.UpdatePool(job, didSucceed)

	if shouldReconcile {
		job := resourceIpam.ReconcilePool()
		if job.Operations != worker.OperationReconcileNotRequired {
			i.SubmitAsyncJob(job)
		}
	}
}

// putInstanceProviderAndPool stores the node's instance provider and pool to the cache
func (i *ipv4PrefixProvider) putInstanceProviderAndPool(nodeName string, resourceIpam ipam.Ipam, manager eni.ENIManager) {
	i.lock.Lock()
	defer i.lock.Unlock()

	resource := ResourceProviderAndIPAM{
		eniManager:   manager,
		resourceIpam: resourceIpam,
	}

	i.instanceProviderAndPool[nodeName] = resource
}

// getInstanceProviderAndPool returns the node's instance provider and pool from the cache
func (p *ipv4PrefixProvider) getInstanceProviderAndPool(nodeName string) (ResourceProviderAndIPAM, bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	resource, found := p.instanceProviderAndPool[nodeName]
	return resource, found
}

// deleteInstanceProviderAndPool deletes the node's instance provider and pool from the cache
func (p *ipv4PrefixProvider) deleteInstanceProviderAndPool(nodeName string) {
	p.lock.Lock()
	defer p.lock.Unlock()

	delete(p.instanceProviderAndPool, nodeName)
}

// getCapacity returns the capacity based on the instance type and the instance os
func getCapacity(instanceType string, instanceOs string) int {
	// Assign only 1st ENIs non primary IP
	limits, found := vpc.Limits[instanceType]
	if !found {
		return 0
	}
	var capacity int
	if instanceOs == config.OSWindows {
		capacity = (limits.IPv4PerInterface - 1) * 16
	} else {
		capacity = (limits.IPv4PerInterface - 1) * 16 * limits.Interface
	}

	return capacity
}

// GetPool returns the warm pool for the IPv4 resources
func (i *ipv4PrefixProvider) GetPool(nodeName string) (pool.Pool, bool) {
	return nil, false
}

// GetIPAM returns the IPAM for the IPv4 prefix resources
func (i *ipv4PrefixProvider) GetIPAM(nodeName string) (ipam.Ipam, bool) {
	providerAndPool, exists := i.getInstanceProviderAndPool(nodeName)
	if !exists {
		return nil, false
	}
	return providerAndPool.resourceIpam, true
}

// IsInstanceSupported returns true for windows node as IP as extended resource is only supported by windows node now
func (i *ipv4PrefixProvider) IsInstanceSupported(instance ec2.EC2Instance) bool {
	if instance.Os() == config.OSWindows {
		return true
	}
	return false
}

func (i *ipv4PrefixProvider) Introspect() interface{} {
	i.lock.RLock()
	defer i.lock.RUnlock()

	response := make(map[string]ipam.IntrospectResponse)
	for nodeName, resource := range i.instanceProviderAndPool {
		response[nodeName] = resource.resourceIpam.Introspect()
	}
	return response
}

func (i *ipv4PrefixProvider) IntrospectNode(nodeName string) interface{} {
	i.lock.RLock()
	defer i.lock.RUnlock()

	resource, found := i.instanceProviderAndPool[nodeName]
	if !found {
		return struct{}{}
	}
	return resource.resourceIpam.Introspect()
}
