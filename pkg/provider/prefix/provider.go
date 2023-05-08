package prefix

import (
	"fmt"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/ip/eni"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ipv4PrefixProvider struct {
	// log is the logger initialized with prefix provider details
	log logr.Logger
	// apiWrapper wraps all clients used by the controller
	apiWrapper api.Wrapper
	// workerPool with worker routine to execute asynchronous job on the prefix provider
	workerPool worker.Worker
	// config is the warm pool configuration for the resource prefix
	config *config.WarmPoolConfig
	// lock to allow multiple routines to access the cache concurrently
	lock sync.RWMutex // guards the following
	// instanceResources stores the ENIManager and the resource pool per instance
	instanceProviderAndPool map[string]*ResourceProviderAndPool
	// conditions is used to check which IP allocation mode is enabled
	conditions condition.Conditions
}

// ResourceProviderAndPool contains the instance's ENI manager and the resource pool
type ResourceProviderAndPool struct {
	// lock guards the struct
	lock         sync.RWMutex
	eniManager   eni.ENIManager
	resourcePool pool.Pool
	// capacity is stored so that it can be advertised when node is updated
	capacity int
	// isPrevPDEnabled stores whether PD was enabled previously
	isPrevPDEnabled bool
}

func NewIPv4PrefixProvider(log logr.Logger, apiWrapper api.Wrapper, workerPool worker.Worker,
	resourceConfig config.ResourceConfig, conditions condition.Conditions) provider.ResourceProvider {
	return &ipv4PrefixProvider{
		instanceProviderAndPool: make(map[string]*ResourceProviderAndPool),
		config:                  resourceConfig.WarmPoolConfig,
		log:                     log,
		apiWrapper:              apiWrapper,
		workerPool:              workerPool,
		conditions:              conditions,
	}
}

func (p *ipv4PrefixProvider) InitResource(instance ec2.EC2Instance) error {
	nodeName := instance.Name()

	eniManager := eni.NewENIManager(instance)
	presentIPs, presentPrefixes, err := eniManager.InitResources(p.apiWrapper.EC2API)
	if err != nil {
		return err
	}

	pods, err := p.apiWrapper.PodAPI.GetRunningPodsOnNode(nodeName)
	if err != nil {
		return err
	}

	// Construct map of all possible IPs to prefix for each assigned prefix
	warmResourceIDToGroup := map[string]string{}
	for _, prefix := range presentPrefixes {
		ips, err := pool.DeconstructIPsFromPrefix(prefix)
		if err != nil {
			return err
		}
		for _, ip := range ips {
			warmResourceIDToGroup[ip] = prefix
		}
	}

	podToResourceMap := make(map[string]pool.Resource)

	for _, pod := range pods {
		annotation, present := pod.Annotations[config.ResourceNameIPAddress]
		if !present {
			continue
		}

		prefix, found := warmResourceIDToGroup[annotation]
		if found {
			podToResourceMap[string(pod.UID)] = pool.Resource{GroupID: prefix, ResourceID: annotation}
			// remove running pod's IP from warm resources
			delete(warmResourceIDToGroup, annotation)
		} else {
			// If running pod's IP is not constructed from an assigned prefix on the instance, ignore it
			p.log.Info("ignoring secondary IP", "IPv4 address ", annotation)
		}
	}

	// Construct list of warm Resources for each assigned prefix
	warmResources := make(map[string][]pool.Resource)
	for ip, prefix := range warmResourceIDToGroup {
		warmResources[prefix] = append(warmResources[prefix], pool.Resource{GroupID: prefix, ResourceID: ip})
	}

	nodeCapacity := (getCapacity(instance.Type(), instance.Os()) - len(presentIPs)) * pool.NumIPv4AddrPerPrefix

	// Retrieve dynamic configuration for prefix delegation from config map and override the default resource config
	resourceConfig := make(map[string]config.ResourceConfig)
	vpcCniConfigMap, err := p.apiWrapper.K8sAPI.GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace)
	if err == nil {
		resourceConfig = config.LoadResourceConfigFromConfigMap(vpcCniConfigMap)
	} else {
		ctrl.Log.Error(err, "failed to read from config map, will use default resource config")
		resourceConfig = config.LoadResourceConfig()
	}
	p.config = resourceConfig[config.ResourceNameIPAddressFromPrefix].WarmPoolConfig

	// Set warm pool config to empty if PD is not enabled
	prefixIPWPConfig := p.config
	isPDEnabled := p.conditions.IsWindowsPrefixDelegationEnabled()
	if !isPDEnabled {
		prefixIPWPConfig = &config.WarmPoolConfig{}
	}

	resourcePool := pool.NewResourcePool(p.log.WithName("prefix ipv4 address resource pool").
		WithValues("node name", instance.Name()), prefixIPWPConfig, podToResourceMap,
		warmResources, instance.Name(), nodeCapacity, true)

	p.putInstanceProviderAndPool(nodeName, resourcePool, eniManager, nodeCapacity, isPDEnabled)

	p.log.Info("initialized the resource provider for prefix ipv4 address",
		"capacity", nodeCapacity, "node name", nodeName, "instance type",
		instance.Type(), "instance ID", instance.InstanceID(), "warmPoolConfig", p.config)

	job := resourcePool.ReconcilePool()
	if job.Operations != worker.OperationReconcileNotRequired {
		p.SubmitAsyncJob(job)
	}

	// Submit the async job to periodically process the delete queue
	p.SubmitAsyncJob(worker.NewWarmProcessDeleteQueueJob(nodeName))
	return nil
}

func (p *ipv4PrefixProvider) DeInitResource(instance ec2.EC2Instance) error {
	nodeName := instance.Name()
	p.deleteInstanceProviderAndPool(nodeName)

	return nil
}

func (p *ipv4PrefixProvider) UpdateResourceCapacity(instance ec2.EC2Instance) error {
	resourceProviderAndPool, isPresent := p.getInstanceProviderAndPool(instance.Name())
	if !isPresent {
		p.log.Error(nil, "cannot find the instance provider and pool form the cache", "node-name", instance.Name())
		return nil
	}

	resourceProviderAndPool.lock.Lock()
	defer resourceProviderAndPool.lock.Unlock()

	// Check if PD is enabled
	isCurrPDEnabled := p.conditions.IsWindowsPrefixDelegationEnabled()

	// Previous state and current state are both PD disabled, no need to update the warm pool config or node capacity for prefix IP provider
	if !resourceProviderAndPool.isPrevPDEnabled && !isCurrPDEnabled {
		return nil
	}

	// If prefix delegation is disabled, then set the prefix IP pool state to draining and
	// do not update the capacity as that would be done by secondary IP provider
	if resourceProviderAndPool.isPrevPDEnabled && !isCurrPDEnabled {
		resourceProviderAndPool.isPrevPDEnabled = false
		emptyWPConfig := &config.WarmPoolConfig{}
		job := resourceProviderAndPool.resourcePool.UpdateWarmPoolConfig(emptyWPConfig)
		p.log.Info("Secondary IP provider should be active")
		p.SubmitAsyncJob(job)
		return nil
	}

	resourceProviderAndPool.isPrevPDEnabled = true

	// Load dynamic configuration for prefix delegation from config map as it could be changed after initialization
	warmPoolConfig := p.config
	vpcCniConfigMap, err := p.apiWrapper.K8sAPI.GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace)
	resourceConfig := config.LoadResourceConfigFromConfigMap(vpcCniConfigMap)
	prefixIPResourceConfig, ok := resourceConfig[config.ResourceNameIPAddressFromPrefix]
	if !ok {
		p.log.Error(fmt.Errorf("failed to find resource configuration"), "resourceName", config.ResourceNameIPAddressFromPrefix)
	} else {
		warmPoolConfig = prefixIPResourceConfig.WarmPoolConfig
	}
	// Set the secondary IP provider pool state to active
	job := resourceProviderAndPool.resourcePool.UpdateWarmPoolConfig(warmPoolConfig)
	p.SubmitAsyncJob(job)

	instanceType := instance.Type()
	instanceName := instance.Name()
	os := instance.Os()

	capacity := resourceProviderAndPool.capacity

	// Advertise capacity of private IPv4 addresses deconstructed from prefixes
	err = p.apiWrapper.K8sAPI.AdvertiseCapacityIfNotSet(instance.Name(), config.ResourceNameIPAddress, capacity)
	if err != nil {
		return err
	}
	p.log.V(1).Info("advertised capacity",
		"instance", instanceName, "instance type", instanceType, "os", os, "capacity", capacity)

	return nil
}

func (p *ipv4PrefixProvider) SubmitAsyncJob(job interface{}) {
	p.workerPool.SubmitJob(job)
}

func (p *ipv4PrefixProvider) ProcessAsyncJob(job interface{}) (ctrl.Result, error) {
	warmPoolJob, isValid := job.(*worker.WarmPoolJob)
	if !isValid {
		return ctrl.Result{}, fmt.Errorf("invalid job type")
	}

	switch warmPoolJob.Operations {
	case worker.OperationCreate:
		p.CreateIPv4PrefixAndUpdatePool(warmPoolJob)
	case worker.OperationDeleted:
		p.DeleteIPv4PrefixAndUpdatePool(warmPoolJob)
	case worker.OperationReSyncPool:
		p.ReSyncPool(warmPoolJob)
	case worker.OperationProcessDeleteQueue:
		return p.ProcessDeleteQueue(warmPoolJob)
	}

	return ctrl.Result{}, nil
}

// CreateIPv4PrefixAndUpdatePool executes the Create IPv4 Prefix workflow by assigning enough IPv4 prefixes to satisfy
// the desired number of IPs required by the warm pool job
func (p *ipv4PrefixProvider) CreateIPv4PrefixAndUpdatePool(job *worker.WarmPoolJob) {
	instanceResource, found := p.getInstanceProviderAndPool(job.NodeName)
	if !found {
		p.log.Error(fmt.Errorf("cannot find the instance provider and pool form the cache"), "node", job.NodeName)
		return
	}
	didSucceed := true
	resources, err := instanceResource.eniManager.CreateIPV4Resource(job.ResourceCount, config.ResourceTypeIPv4Prefix, p.apiWrapper.EC2API,
		p.log)

	if err != nil {
		p.log.Error(err, "failed to create all/some of the IPv4 prefixes", "created resources", resources)
		didSucceed = false
	}
	job.Resources = resources
	p.updatePoolAndReconcileIfRequired(instanceResource.resourcePool, job, didSucceed)
}

// DeleteIPv4PrefixAndUpdatePool executes the Delete IPv4 workflow for the list of IPs provided in the warm pool job
func (p *ipv4PrefixProvider) DeleteIPv4PrefixAndUpdatePool(job *worker.WarmPoolJob) {
	instanceResource, found := p.getInstanceProviderAndPool(job.NodeName)
	if !found {
		p.log.Error(fmt.Errorf("cannot find the instance provider and pool form the cache"), "node", job.NodeName)
		return
	}

	didSucceed := true
	failedResources, err := instanceResource.eniManager.DeleteIPV4Resource(job.Resources, config.ResourceTypeIPv4Prefix, p.apiWrapper.EC2API,
		p.log)

	if err != nil {
		p.log.Error(err, "failed to delete all/some of the IPv4 prefixes", "failed resources", failedResources)
		didSucceed = false
	}
	job.Resources = failedResources
	p.updatePoolAndReconcileIfRequired(instanceResource.resourcePool, job, didSucceed)
}

func (p *ipv4PrefixProvider) ReSyncPool(job *worker.WarmPoolJob) {
	providerAndPool, found := p.instanceProviderAndPool[job.NodeName]
	if !found {
		p.log.Error(fmt.Errorf("instance provider not found"), "node is not initialized",
			"name", job.NodeName)
		return
	}

	_, resources, err := providerAndPool.eniManager.InitResources(p.apiWrapper.EC2API)
	if err != nil {
		p.log.Error(err, "failed to get init resources for the node",
			"name", job.NodeName)
		return
	}

	providerAndPool.resourcePool.ReSync(resources)
}

func (p *ipv4PrefixProvider) ProcessDeleteQueue(job *worker.WarmPoolJob) (ctrl.Result, error) {
	resourceProviderAndPool, isPresent := p.getInstanceProviderAndPool(job.NodeName)
	if !isPresent {
		p.log.Info("forgetting the delete queue processing job", "node", job.NodeName)
		return ctrl.Result{}, nil
	}
	// TODO: For efficiency run only when required in next release
	resourceProviderAndPool.resourcePool.ProcessCoolDownQueue()

	// After the cool down queue is processed check if we need to do reconciliation
	job = resourceProviderAndPool.resourcePool.ReconcilePool()
	if job.Operations != worker.OperationReconcileNotRequired {
		p.SubmitAsyncJob(job)
	}

	// Re-submit the job to execute after cool down period has ended
	return ctrl.Result{Requeue: true, RequeueAfter: config.CoolDownPeriod}, nil
}

// updatePoolAndReconcileIfRequired updates the resource pool and reconcile again and submit a new job if required
func (p *ipv4PrefixProvider) updatePoolAndReconcileIfRequired(resourcePool pool.Pool, job *worker.WarmPoolJob, didSucceed bool) {
	// Update the pool to add the created/failed resource to the warm pool and decrement the pending count
	shouldReconcile := resourcePool.UpdatePool(job, didSucceed)

	if shouldReconcile {
		job := resourcePool.ReconcilePool()
		if job.Operations != worker.OperationReconcileNotRequired {
			p.SubmitAsyncJob(job)
		}
	}
}

func (p *ipv4PrefixProvider) GetPool(nodeName string) (pool.Pool, bool) {
	providerAndPool, exists := p.getInstanceProviderAndPool(nodeName)
	if !exists {
		return nil, false
	}
	return providerAndPool.resourcePool, true
}

func (p *ipv4PrefixProvider) IsInstanceSupported(instance ec2.EC2Instance) bool {
	//TODO add check for Nitro instances
	if instance.Os() == config.OSWindows {
		return true
	}
	return false
}

func (p *ipv4PrefixProvider) Introspect() interface{} {
	p.lock.RLock()
	defer p.lock.RUnlock()

	response := make(map[string]pool.IntrospectResponse)
	for nodeName, resource := range p.instanceProviderAndPool {
		response[nodeName] = resource.resourcePool.Introspect()
	}
	return response
}

func (p *ipv4PrefixProvider) IntrospectNode(node string) interface{} {
	p.lock.RLock()
	defer p.lock.RUnlock()

	resource, found := p.instanceProviderAndPool[node]
	if !found {
		return struct{}{}
	}
	return resource.resourcePool.Introspect()
}

// putInstanceProviderAndPool stores the node's instance provider and pool to the cache
func (p *ipv4PrefixProvider) putInstanceProviderAndPool(nodeName string, resourcePool pool.Pool, manager eni.ENIManager, capacity int,
	isPrevPDEnabled bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	resource := &ResourceProviderAndPool{
		eniManager:      manager,
		resourcePool:    resourcePool,
		capacity:        capacity,
		isPrevPDEnabled: isPrevPDEnabled,
	}

	p.instanceProviderAndPool[nodeName] = resource
}

// getInstanceProviderAndPool returns the node's instance provider and pool from the cache
func (p *ipv4PrefixProvider) getInstanceProviderAndPool(nodeName string) (*ResourceProviderAndPool, bool) {
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

// getCapacity returns the capacity for IPv4 addresses deconstructed from IPv4 prefixes based on the instance type and the instance os;
func getCapacity(instanceType string, instanceOs string) int {
	// Assign only 1st ENIs non-primary IP
	limits, found := vpc.Limits[instanceType]
	if !found {
		return 0
	}
	var capacity int
	if instanceOs == config.OSWindows {
		capacity = limits.IPv4PerInterface - 1
	} else {
		capacity = (limits.IPv4PerInterface - 1) * limits.Interface
	}

	return capacity
}
