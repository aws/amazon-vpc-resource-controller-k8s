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

package ipam

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/ipam/eni"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
)

var (
	ErrPoolAtMaxCapacity          = fmt.Errorf("cannot assign any more resource from IPAM")
	ErrResourceAreBeingCooledDown = fmt.Errorf("cannot assign resource now, resources are being cooled down")
	ErrResourcesAreBeingCreated   = fmt.Errorf("cannot assign resource now, resources are being created")
	ErrWarmPoolEmpty              = fmt.Errorf("IPAM is empty")
	ErrResourceAlreadyAssigned    = fmt.Errorf("resource is already assigned to the requestor")
	ErrResourceDoesntExist        = fmt.Errorf("requested resource doesn't exist in used pool")
	ErrIncorrectResourceOwner     = fmt.Errorf("resource doesn't belong to the requestor")
)

type Ipam interface {
	AllocatePrefix(numberOfPrefixes int, apiWrapper api.Wrapper) (resources []string, success bool)
	DeAllocatePrefix(prefixes []string, apiWrapper api.Wrapper) (resources []string, success bool)
	AssignResource(requesterID string) (resourceDetail worker.IPAMResourceInfo, shouldReconcile bool, err error)
	FreeResource(requesterID string, resourceID string) (shouldReconcile bool, err error)
	GetAssignedResource(requesterID string) (resourceDetail worker.IPAMResourceInfo, ownsResource bool)
	UpdatePool(job *worker.WarmPoolJob, didSucceed bool) (shouldReconcile bool)
	InitIPAM(instance ec2.EC2Instance, apiWrapper api.Wrapper) (resourcesAvailable []worker.IPAMResourceInfo, eniManager eni.ENIManager, err error)
	ReSync(resources []string)
	ReconcilePool() *worker.WarmPoolJob
	ProcessCoolDownQueue() bool
	Introspect() IntrospectResponse
}

type ipam struct {
	// log is the logger initialized with the pool details
	log logr.Logger
	// capacity is the max number of resources that can be allocated from this pool
	capacity int
	// warmPoolConfig is the configuration for the given pool
	warmPoolConfig *config.WarmPoolConfig
	// lock to concurrently make modification to the poll resources
	lock sync.RWMutex // following resources are guarded by the lock
	// Unused resources prefix mapping [{IP: prefix}, {IP: prefix}...]
	warmResources []worker.IPAMResourceInfo
	// Used resources {PodID: {IP Address, Prefix}}
	usedResources map[string]worker.IPAMResourceInfo
	// All prefixes that is available through ENI [Prefix1, Prefix2, Prefix3]
	allocatedPrefix []string
	// Total IPs across different prefixes {Prefix: # of used addresses}
	prefixUsage map[string]int
	// coolDownQueue is the resources that sit in the queue for the cool down period
	coolDownQueue []CoolDownResource
	// pendingCreate represents the number of resources being created asynchronously
	pendingCreate int
	// pendingDelete represents the number of resources being deleted asynchronously
	pendingDelete int
	// nodeName k8s name of the node
	nodeName string
	// reSyncRequired is set if the upstream and pool are possibly out of sync due to
	// errors in creating/deleting resources
	reSyncRequired bool

	// ENI Manager
	eniManager eni.ENIManager
}

type prefixInfo struct {
	// Prefix
	prefix string
	// Number of used addresses
	usedAddresses int
}

type CoolDownResource struct {
	// ResourceID is the unique ID of the resource
	ResourceID worker.IPAMResourceInfo
	// DeletionTimestamp is the time when the owner of the resource was deleted
	DeletionTimestamp time.Time
}

// IntrospectResponse is the pool state returned to the introspect API
type IntrospectResponse struct {
	UsedResources    map[string]worker.IPAMResourceInfo
	WarmResources    []worker.IPAMResourceInfo
	CoolingResources []CoolDownResource
}

func NewResourceIPAM(log logr.Logger, poolConfig *config.WarmPoolConfig, usedResources map[string]worker.IPAMResourceInfo,
	warmResources []worker.IPAMResourceInfo, allocatedPrefix []string, prefixUsage map[string]int, nodeName string, capacity int) Ipam {
	ipam := &ipam{
		log:             log,
		warmPoolConfig:  poolConfig,
		usedResources:   usedResources,
		warmResources:   warmResources,
		allocatedPrefix: allocatedPrefix,
		prefixUsage:     prefixUsage,
		capacity:        capacity,
		nodeName:        nodeName,
	}
	return ipam
}

func (i *ipam) InitIPAM(instance ec2.EC2Instance, apiWrapper api.Wrapper) (resourcesAvailable []worker.IPAMResourceInfo, initEniManager eni.ENIManager, err error) {
	nodeName := instance.Name()

	eniManager := eni.NewENIManager(instance)
	presentPrefixes, err := eniManager.InitResources(apiWrapper.EC2API)
	i.eniManager = eniManager
	if err != nil {
		i.log.Error(err, "Error initalizing eni manager")
		return []worker.IPAMResourceInfo{}, nil, err
	}

	pods, err := apiWrapper.PodAPI.GetRunningPodsOnNode(nodeName)
	if err != nil {
		i.log.Error(err, "Error getting pods on node")
		return []worker.IPAMResourceInfo{}, nil, err
	}

	// Mapping IPs to prefixes
	ipToResourceInfoMapping := make(map[string]worker.IPAMResourceInfo)

	// Create mapping for current IPs and prefixes. Also update prefix tracking
	for _, prefix := range presentPrefixes {
		allPossibleIPs, _ := DeconstructPrefix(prefix)
		i.allocatedPrefix = append(i.allocatedPrefix, prefix)
		i.prefixUsage[prefix] = 0
		for _, ip := range allPossibleIPs {
			ipToResourceInfoMapping[ip] = worker.IPAMResourceInfo{ResourceID: ip, PrefixOrigin: prefix}
		}
	}

	// Remap pod to IP. Also updates prefix tracking
	podToResourceMap := map[string]worker.IPAMResourceInfo{}
	usedIPSet := map[string]worker.IPAMResourceInfo{}
	for _, pod := range pods {
		annotation, present := pod.Annotations[config.ResourceNameIPAddress]
		if !present {
			continue
		}
		podToResourceMap[string(pod.UID)] = ipToResourceInfoMapping[annotation]
		usedIPSet[annotation] = ipToResourceInfoMapping[annotation]
		i.prefixUsage[ipToResourceInfoMapping[annotation].PrefixOrigin]++
	}
	i.usedResources = podToResourceMap
	i.log.Info("Remapped pod and IP association", "Pod Mapping", i.usedResources)

	// Remap warm resources
	tempWarmResources := []worker.IPAMResourceInfo{}
	allResources := []worker.IPAMResourceInfo{}
	for _, resourceInfo := range ipToResourceInfoMapping {
		_, present := usedIPSet[resourceInfo.ResourceID]
		if !present {
			tempWarmResources = append(tempWarmResources, resourceInfo)
		}
		allResources = append(allResources, resourceInfo)
	}
	i.warmResources = tempWarmResources
	i.log.Info("Remapped warm resource", "Warm resources", i.warmResources)

	i.log.Info("initialized the resource provider for resource IPv4 prefixes")
	return allResources, eniManager, nil
}

// ReSync syncs state of upstream with the local pool. If local resources have additional
// resource which doesn't reflect in upstream list then these resources are removed. If the
// upstream has additional resources which are not present locally, these resources are added
// to the IPAM. During ReSync all Create/Delete operations on the Pool should be halted
// but Assign/Free on the Pool can be allowed.
func (i *ipam) ReSync(upstreamResource []string) {
	i.lock.Lock()
	defer i.lock.Unlock()

	allResources := []worker.IPAMResourceInfo{}

	// Mapping IPs to prefixes
	ipToResourceInfoMapping := make(map[string]worker.IPAMResourceInfo)

	// Create mapping for current IPs and prefixes
	for _, prefix := range upstreamResource {
		allPossibleIPs, _ := DeconstructPrefix(prefix)
		for _, ip := range allPossibleIPs {
			ipToResourceInfoMapping[ip] = worker.IPAMResourceInfo{ResourceID: ip, PrefixOrigin: prefix}
		}
	}

	for _, resourceInfo := range ipToResourceInfoMapping {
		allResources = append(allResources, resourceInfo)
	}

	// This is possible if two Re-Sync were requested at same time
	if !i.reSyncRequired {
		i.log.Info("duplicate re-sync request, will be ignored")
		return
	}

	i.reSyncRequired = false

	// Get the list of local resources
	var localResources []worker.IPAMResourceInfo

	for _, resource := range i.coolDownQueue {
		localResources = append(localResources, resource.ResourceID)
	}

	for _, v := range i.usedResources {
		localResources = append(localResources, v)
	}

	localResources = append(localResources, i.warmResources...)

	// resources that are present upstream but missing in the pool
	newResources := Difference(allResources, localResources)

	// resources that are deleted from upstream but still present in the pool
	deletedResources := Difference(localResources, allResources)

	if len(newResources) == 0 && len(deletedResources) == 0 {
		i.log.Info("local and upstream state is in sync")
		return
	}

	if len(newResources) > 0 {
		i.log.Info("adding new resources to IPAM", "resource", newResources)
		i.warmResources = append(i.warmResources, newResources...)
	}

	if len(deletedResources) > 0 {
		i.log.Info("attempting to remove deleted resources",
			"deleted resources", deletedResources)

		for _, deletedResource := range deletedResources {
			for j := len(i.warmResources) - 1; j >= 0; j-- {
				if i.warmResources[j] == deletedResource {
					i.log.Info("removing resource from IPAM",
						"resource id", deletedResource)
					i.warmResources = append(i.warmResources[:j], i.warmResources[j+1:]...)
				}
			}
			for j := len(i.coolDownQueue) - 1; j >= 0; j-- {
				if i.coolDownQueue[j].ResourceID == deletedResource {
					i.log.Info("removing resource from cool down queue",
						"resource id", deletedResource)
					i.coolDownQueue = append(i.coolDownQueue[:j], i.coolDownQueue[j+1:]...)
				}
			}
		}
	}
}

// AssignResource assigns a resources to the requester, the caller must retry in case there is capacity and the IPAM
// is currently empty
func (i *ipam) AssignResource(requesterID string) (resourceDetail worker.IPAMResourceInfo, shouldReconcile bool, err error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if _, isAlreadyAssigned := i.usedResources[requesterID]; isAlreadyAssigned {
		return worker.IPAMResourceInfo{}, false, ErrResourceAlreadyAssigned
	}

	if len(i.usedResources) == i.capacity {
		return worker.IPAMResourceInfo{}, false, ErrPoolAtMaxCapacity
	}

	// Caller must retry at max by 30 seconds [Max time resource will sit in the cool down queue]
	if len(i.usedResources)+len(i.coolDownQueue) == i.capacity {
		return worker.IPAMResourceInfo{}, false, ErrResourceAreBeingCooledDown
	}

	// Caller can retry in 600 ms [Average time to create and attach a new ENI] or less
	if len(i.usedResources)+len(i.coolDownQueue)+i.pendingCreate+i.pendingDelete == i.capacity {
		return worker.IPAMResourceInfo{}, false, ErrResourcesAreBeingCreated
	}

	// Caller can retry in 600 ms [Average time to create and attach a new ENI] or less
	// Different from above check because here we want to perform reconciliation
	if len(i.warmResources) == 0 {
		return worker.IPAMResourceInfo{}, true, ErrWarmPoolEmpty
	}

	// Allocate the resource
	resourceDetail = i.warmResources[0]
	i.warmResources = i.warmResources[1:]

	/// Add the resource in the used resource key-value pair
	i.usedResources[requesterID] = resourceDetail
	i.prefixUsage[resourceDetail.PrefixOrigin]++

	i.log.V(1).Info("assigned resource",
		"resource id", resourceDetail.ResourceID, "requester id", requesterID)

	return resourceDetail, true, nil
}

func (i *ipam) GetAssignedResource(requesterID string) (resourceDetail worker.IPAMResourceInfo, ownsResource bool) {
	i.lock.Lock()
	defer i.lock.Unlock()

	resourceDetail, ownsResource = i.usedResources[requesterID]
	return
}

// FreeResource puts the resource allocated to the given requester into the cool down queue
func (i *ipam) FreeResource(requesterID string, resourceID string) (shouldReconcile bool, err error) {
	i.lock.Lock()
	defer i.lock.Unlock()

	actualResourceID, isAssigned := i.usedResources[requesterID]

	if !isAssigned {
		return false, ErrResourceDoesntExist
	}

	if actualResourceID.ResourceID != resourceID {
		return false, ErrIncorrectResourceOwner
	}

	// Decrement prefix usage
	i.prefixUsage[actualResourceID.PrefixOrigin]--
	delete(i.usedResources, requesterID)

	// Put the resource in cool down queue
	resource := CoolDownResource{
		ResourceID:        actualResourceID,
		DeletionTimestamp: time.Now(),
	}

	i.coolDownQueue = append(i.coolDownQueue, resource)

	i.log.V(1).Info("added the resource to cool down queue",
		"id", resourceID, "owner id", requesterID)

	return true, nil
}

// UpdatePool updates the IPAM with the result of the asynchronous job executed by the provider
func (i *ipam) UpdatePool(job *worker.WarmPoolJob, didSucceed bool) (shouldReconcile bool) {
	i.lock.Lock()
	defer i.lock.Unlock()

	log := i.log.WithValues("operation", job.Operations)

	if !didSucceed {
		// If the job fails, re-sync the state of the Pool with upstream
		i.reSyncRequired = true
		shouldReconcile = true
		log.Error(fmt.Errorf("IPAM job failed: %v", job), "operation failed")
	}

	if job.Resources != nil && len(job.Resources) > 0 {
		// Add the resources to the IPAM
		for _, resource := range job.Resources {
			availableIPs, error := DeconstructPrefix(resource)

			if error != nil {
				fmt.Println("Error occurred")
			}

			for j := 0; j < len(availableIPs); j++ {
				i.warmResources = append(i.warmResources, worker.IPAMResourceInfo{ResourceID: availableIPs[j], PrefixOrigin: resource})
			}

			i.allocatedPrefix = append(i.allocatedPrefix, resource)
			i.prefixUsage[resource] = 0
		}
		log.Info("added resource to the IPAM", "resources", job.Resources)
	}

	if job.Operations == worker.OperationCreate {
		i.pendingCreate -= job.ResourceCount * 15
	} else if job.Operations == worker.OperationDeleted {
		i.pendingDelete -= job.ResourceCount * 15
	}

	log.V(1).Info("processed job response", "job", job, "pending create",
		i.pendingCreate, "pending delete", i.pendingDelete)

	return shouldReconcile
}

// ProcessCoolDownQueue adds the resources back to the IPAM once they have cooled down
func (i *ipam) ProcessCoolDownQueue() (needFurtherProcessing bool) {
	i.lock.Lock()
	defer i.lock.Unlock()

	if len(i.coolDownQueue) == 0 {
		return false
	}

	for index, resource := range i.coolDownQueue {
		if time.Since(resource.DeletionTimestamp) >= config.CoolDownPeriod {
			// Add back to the cool down queue
			i.warmResources = append(i.warmResources, resource.ResourceID)
			i.log.Info("moving the resource from delete to cool down queue",
				"resource id", resource.ResourceID, "deletion time", resource.DeletionTimestamp)
		} else {
			// Remove the items from cool down queue that are processed and return
			i.coolDownQueue = i.coolDownQueue[index:]
			return true
		}
	}

	// All items were processed empty the cool down queue
	i.coolDownQueue = i.coolDownQueue[:0]

	return false
}

// reconcilePoolIfRequired reconciles the IPAM to make it reach it's desired state by submitting either create or delete
// request to the IPAM
func (i *ipam) ReconcilePool() *worker.WarmPoolJob {
	i.lock.Lock()
	defer i.lock.Unlock()

	// Total created resources includes all the resources for the instance that are not yet deleted
	totalCreatedResources := len(i.warmResources) + len(i.usedResources) + len(i.coolDownQueue) +
		i.pendingCreate + i.pendingDelete

	log := i.log.WithValues("resync", i.reSyncRequired, "warm", len(i.warmResources), "used",
		len(i.usedResources), "pending create", i.pendingCreate, "pending delete", &i.pendingDelete,
		"cool down queue", len(i.coolDownQueue), "total resources", totalCreatedResources,
		"max capacity", i.capacity, "desired size", i.warmPoolConfig.DesiredSize)

	// Counts number of free prefixes
	freePrefixes := make([]string, 0)

	// Count number of unused prefixes
	for j := 0; j < len(i.allocatedPrefix); j++ {
		if i.prefixUsage[i.allocatedPrefix[j]] == 0 {
			freePrefixes = append(freePrefixes, i.allocatedPrefix[j])
		}
	}

	if i.reSyncRequired {
		// If Pending operations are present then we can't re-sync as the upstream
		// and pool could change during re-sync
		if i.pendingCreate != 0 || i.pendingDelete != 0 {
			i.log.Info("cannot re-sync as there are pending add/delete request")
			return &worker.WarmPoolJob{
				Operations: worker.OperationReconcileNotRequired,
			}
		}
		i.log.Info("submitting request re-sync the pool")
		return worker.NewWarmPoolReSyncJob(i.nodeName)
	}

	if len(i.usedResources)+i.pendingCreate+i.pendingDelete+len(i.coolDownQueue) == i.capacity {
		log.V(1).Info("cannot reconcile, at max capacity")
		return &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}
	}

	// Consider pending create as well so we don't create multiple subsequent create request
	deviation := i.warmPoolConfig.DesiredSize - (len(i.warmResources) + i.pendingCreate)

	// Need to create more resources for IPAM
	if deviation > i.warmPoolConfig.MaxDeviation {
		// The maximum number of resources that can be created
		canCreateUpto := i.capacity - totalCreatedResources
		if canCreateUpto == 0 {
			return &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}
		}

		// Need to add to IPAM
		if deviation > canCreateUpto {
			log.V(1).Info("can only create limited resources", "can create", canCreateUpto,
				"requested", deviation, "desired", deviation)
			deviation = canCreateUpto
		}

		// Increment the pending to the size of deviation, once we get async response on creation success we can decrement
		// pending
		prefixesToAdd := int(math.Ceil(float64(deviation / 15)))
		i.pendingCreate += prefixesToAdd * 15

		log.Info("created job to add resources to IPAM", "requested count", prefixesToAdd)

		return worker.NewWarmPoolCreateJob(i.nodeName, prefixesToAdd)

	} else if -deviation > i.warmPoolConfig.MaxDeviation && len(freePrefixes) > 0 {
		// Need to delete from IPAM
		deviation = -deviation

		// Amount of unused prefixes to remove
		prefixesToRemove := int(math.Ceil(float64(deviation / 15)))

		if prefixesToRemove > len(freePrefixes) {
			prefixesToRemove = len(freePrefixes)
		}

		prefixesRemoved := []string{}
		resourceToDelete := []worker.IPAMResourceInfo{}
		for j := 0; j < prefixesToRemove; j++ {
			for k := 0; k < len(i.warmResources); k++ {
				if i.warmResources[k].PrefixOrigin == freePrefixes[j] {
					resourceToDelete = append(resourceToDelete, i.warmResources[k])
				}
			}
			prefixesRemoved = append(prefixesRemoved, freePrefixes[j])
		}

		var newWarmResources []worker.IPAMResourceInfo
		k := 0
		if len(resourceToDelete) == 0 {
			for j := 0; j < len(i.warmResources) && k < len(resourceToDelete); j++ {
				if i.warmResources[j].ResourceID != resourceToDelete[k].ResourceID {
					newWarmResources = append(newWarmResources, i.warmResources[j])
				} else {
					k++
				}
			}
			i.warmResources = newWarmResources
		}
		// Increment pending to the number of resource being deleted, once successfully deleted the count can be decremented
		i.pendingDelete += len(prefixesRemoved) * 15

		// Submit the job to delete resources
		log.Info("created job to delete resources from IPAM", "resources to delete", resourceToDelete)

		return worker.NewWarmPoolDeleteJob(i.nodeName, prefixesRemoved)
	}

	log.V(1).Info("no need for reconciliation")

	return &worker.WarmPoolJob{Operations: worker.OperationReconcileNotRequired}
}

func (i *ipam) Introspect() IntrospectResponse {
	i.lock.RLock()
	defer i.lock.RUnlock()

	usedResources := make(map[string]worker.IPAMResourceInfo)
	for k, v := range i.usedResources {
		usedResources[k] = v
	}

	return IntrospectResponse{
		UsedResources:    usedResources,
		WarmResources:    i.warmResources,
		CoolingResources: i.coolDownQueue,
	}
}

// Assign prefix to ENI
func (i *ipam) AllocatePrefix(numberOfPrefixes int, apiWrapper api.Wrapper) (resources []string, success bool) {
	didSucceed := true
	prefixes, err := i.eniManager.CreateIPV4Prefix(numberOfPrefixes, apiWrapper.EC2API, i.log)
	if err != nil {
		i.log.Error(err, "failed to create all/some of the IPv4 prefixes", "created prefixes", prefixes)
		didSucceed = false
	}
	return prefixes, didSucceed
}

// Unassign prefix to ENI
func (i *ipam) DeAllocatePrefix(prefixes []string, apiWrapper api.Wrapper) (resources []string, success bool) {
	didSucceed := true

	if len(prefixes) == 0 {
		return prefixes, didSucceed
	}

	i.log.Info("Prefixes being deleted", "Prefixes", prefixes)
	prefixes, err := i.eniManager.DeleteIPV4Prefix(prefixes, apiWrapper.EC2API, i.log)
	if err != nil {
		i.log.Error(err, "failed to create all/some of the IPv4 prefixes", "created prefixes", prefixes)
		didSucceed = false
	}
	return prefixes, didSucceed
}

// Difference returns a-b, elements present in a and not in b
func Difference(a, b []worker.IPAMResourceInfo) (diff []worker.IPAMResourceInfo) {
	m := make(map[string]struct{})

	for _, item := range b {
		m[item.ResourceID] = struct{}{}
	}
	for _, item := range a {
		if _, ok := m[item.ResourceID]; !ok {
			diff = append(diff, item)
		}
	}
	return
}

// Deconstruction of prefix Input: 10.0.0.0/28 | Output: {10.0.0.0/28: [10.0.0.0/28, 10.0.0.1/28, 10.0.0.2/28, 10.0.0.3/28, 10.0.0.4/28...]}
func DeconstructPrefix(inputPrefix string) (warmResourceBundle []string, err error) {
	// Remove masking #
	var segmentedIP = strings.Split(inputPrefix, "/")

	var ipAddress = strings.Split(segmentedIP[0], ".")

	// Increment host number
	var hostNumber, error = strconv.Atoi(ipAddress[3])

	if error != nil {
		fmt.Print("Unproperly parsed host number")
	}

	availableIPs := make([]string, 0)
	for i := 0; i < 15; i++ {
		var newIP = ipAddress[0] + "." + ipAddress[1] + "." + ipAddress[2] + "." + strconv.Itoa(hostNumber) + "/28"
		availableIPs = append(availableIPs, newIP)
		hostNumber++
	}
	return availableIPs, nil
}
