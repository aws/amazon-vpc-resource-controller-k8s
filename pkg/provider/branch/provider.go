/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package branch

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	branchProviderOperationsErrCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_provider_operations_err_count",
			Help: "The number of errors encountered for branch provider operations",
		},
		[]string{"operation"},
	)

	trunkENIOperationsErrCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "trunk_eni_operations_err_count",
			Help: "The number of errors encountered for operations on Trunk ENI",
		},
		[]string{"operation"},
	)

	branchProviderOperationLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "branch_provider_operation_latency",
			Help: "Branch Provider operations latency in ms",
		},
		[]string{"operation", "resource_count"},
	)

	operationCreateBranchENI            = "create_branch_eni"
	operationCreateBranchENIAndAnnotate = "create_and_annotate_branch_eni"
	operationDeleteBranchENI            = "delete_branch_eni"
	operationInitTrunk                  = "init_trunk"

	prometheusRegistered = false
)

var (
	ErrTrunkExistInCache = fmt.Errorf("trunk eni already exist in cache")
	ErrTrunkNotInCache   = fmt.Errorf("trunk eni not present in cache")
)

// branchENIProvider provides branch ENI to all nodes that support Trunk network interface
type branchENIProvider struct {
	// log is the logger initialized with branch eni provider value
	log logr.Logger
	// k8s client to perform operations on pod object
	k8s k8s.K8sWrapper
	// ec2APIHelper is the helper to make EC2 API calls
	ec2APIHelper api.EC2APIHelper
	// lock to prevent concurrent writes to the trunk eni map
	lock sync.RWMutex
	// trunkENICache is the map of node name to the trunk ENI
	trunkENICache map[string]TrunkENI
	// workerPool is the worker pool and queue for submitting async job
	workerPool worker.Worker
}

// NewBranchENIProvider returns the Branch ENI Provider for all nodes across the cluster
func NewBranchENIProvider(logger logr.Logger, k8sWrapper k8s.K8sWrapper,
	helper api.EC2APIHelper, worker worker.Worker) provider.ResourceProvider {
	if !prometheusRegistered {
		prometheusRegister()
	}

	return &branchENIProvider{
		log:           logger,
		k8s:           k8sWrapper,
		ec2APIHelper:  helper,
		workerPool:    worker,
		trunkENICache: make(map[string]TrunkENI),
	}
}

// prometheusRegister registers prometheus metrics
func prometheusRegister() {
	prometheus.MustRegister(branchProviderOperationsErrCount)
	prometheus.MustRegister(trunkENIOperationsErrCount)
	prometheus.MustRegister(branchProviderOperationLatency)

	prometheusRegistered = true
}

// timeSinceMs returns the time since MS from the start time
func timeSinceMs(start time.Time) float64 {
	return float64(time.Since(start).Milliseconds())
}

// InitResources initialized the resource for the given node name. The initialized trunk ENI is stored in
// cache for use in future Create/Delete Requests
func (b *branchENIProvider) InitResource(instance ec2.EC2Instance) error {
	log := b.log.WithValues("node name", instance.Name())
	trunkENI := NewTrunkENI(log, instance.InstanceID(), instance.SubnetID(), instance.SubnetCidrBlock(), b.ec2APIHelper)

	// Initialize the Trunk ENI
	start := time.Now()
	err := trunkENI.InitTrunk(instance)
	if err != nil {
		log.Error(err, "failed to init resource")
		branchProviderOperationsErrCount.WithLabelValues("init").Inc()
		return err
	}
	branchProviderOperationLatency.WithLabelValues(operationInitTrunk, "1").Observe(timeSinceMs(start))

	log.Info("initialized trunk eni")

	// Add the Trunk ENI to cache
	err = b.addTrunkToCache(instance.Name(), trunkENI)
	if err != nil {
		branchProviderOperationsErrCount.WithLabelValues("add_trunk_to_cache").Inc()
		return err
	}

	b.log.Info("initialized the resource provider successfully")

	return nil
}

// DeInitResources removes the trunk ENI from the cache. Network Interface are not deleted here.
// TODO: Only delete trunk interface when the node is removed from etcd.
func (b *branchENIProvider) DeInitResource(instance ec2.EC2Instance) error {
	b.removeTrunkFromCache(instance.Name())
	b.log.Info("de-initialized resource provider successfully", "node name", instance.Name())

	return nil
}

// SubmitAsyncJob submits the job to the k8s worker queue and returns immediately without waiting for the job to
// complete. Using the k8s worker queue features we can ensure that the same job is not submitted more than once.
func (b *branchENIProvider) SubmitAsyncJob(job interface{}) error {
	return b.workerPool.SubmitJob(job)
}

// ProcessAsyncJob is the job being executed in the worker pool routine. The job must be submitted using the
// SubmitAsyncJob in order to be processed asynchronously by the caller.
func (b *branchENIProvider) ProcessAsyncJob(job interface{}) (ctrl.Result, error) {
	onDemandJob, isValid := job.(worker.OnDemandJob)
	if !isValid {
		return ctrl.Result{}, fmt.Errorf("invalid job type")
	}

	// Get the pod from cache
	pod, err := b.k8s.GetPod(onDemandJob.PodNamespace, onDemandJob.PodName)
	if err != nil {
		branchProviderOperationsErrCount.WithLabelValues("get_pod").Inc()
		return ctrl.Result{}, err
	}

	// TODO: Fix use case where CREATE is ongoing and DELETE is requested.

	if onDemandJob.Operation == worker.OperationDelete {
		return b.DeleteResources(pod)
	} else if onDemandJob.Operation == worker.OperationCreate {
		if _, ok := pod.Annotations[config.ResourceNamePodENI]; !ok {
			// Pod doesn't have an annotation yet. Create Branch ENI and annotate the pod
			return b.CreateAndAnnotateResources(pod, int(onDemandJob.RequestCount))
		}
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, fmt.Errorf("unsupported operation type")
}

// GetResourceCapacity returns the resource capacity for the given instance.
func (b *branchENIProvider) UpdateResourceCapacity(instance ec2.EC2Instance) error {
	capacity := vpc.InstanceBranchENIsAvailable[instance.Type()]
	if capacity != 0 {
		err := b.k8s.AdvertiseCapacityIfNotSet(instance.Name(), config.ResourceNamePodENI, capacity)
		if err != nil {
			branchProviderOperationsErrCount.WithLabelValues("advertise_capacity").Inc()
			return err
		}
	}
	return nil
}

// CreateAndAnnotateResources creates resource for the pod, the function can run concurrently for different pods without
// any locking as long as caller guarantees this function is not called concurrently for same pods.
func (b *branchENIProvider) CreateAndAnnotateResources(pod *v1.Pod, resourceCount int) (ctrl.Result, error) {
	log := b.log.WithValues("pod namespace", pod.Namespace, "pod name", pod.Name, "node name", pod.Spec.NodeName)
	start := time.Now()
	trunkENI, isPresent := b.getTrunkFromCache(pod.Spec.NodeName)
	if !isPresent {
		// This should never happen
		branchProviderOperationsErrCount.WithLabelValues("get_trunk_create").Inc()
		return ctrl.Result{}, fmt.Errorf("trunk not found for node %s", pod.Spec.NodeName)
	}

	var branches []*BranchENI
	var branch *BranchENI
	var err error

	for i := 0; i < resourceCount; i++ {
		// TODO: Pass Security Groups Here
		// TODO: Fallback to etho security gorup if no interface found
		branch, err = trunkENI.CreateAndAssociateBranchToTrunk(nil)
		if err != nil {
			branchProviderOperationsErrCount.WithLabelValues("create_trunk").Inc()
			break
		}
		branches = append(branches, branch)
	}

	// One or more Branch ENI failed to create, delete all created branch ENIs
	if err != nil {
		return b.handleCreateFailed(err, pod.Spec.NodeName, trunkENI, branches)
	}

	branchProviderOperationLatency.WithLabelValues(operationCreateBranchENI, string(resourceCount)).
		Observe(timeSinceMs(start))

	jsonBytes, err := json.Marshal(branches)
	if err != nil {
		return b.handleCreateFailed(err, pod.Spec.NodeName, trunkENI, branches)
	}

	err = b.k8s.AnnotatePod(pod.Namespace, pod.Name, config.ResourceNamePodENI, string(jsonBytes))
	if err != nil {
		branchProviderOperationsErrCount.WithLabelValues("annotate_branch_eni").Inc()
		return b.handleCreateFailed(err, pod.Spec.NodeName, trunkENI, branches)
	}

	branchProviderOperationLatency.WithLabelValues(operationCreateBranchENIAndAnnotate, string(resourceCount)).
		Observe(timeSinceMs(start))

	log.Info("created and annotated branch interface/s successfully", "branches", branches)

	return ctrl.Result{}, nil
}

// TODO: On next retry only delete failed ENIs
// handleCreateFailed deletes the list of branch interfaces for the given trunk network interface
func (b *branchENIProvider) handleCreateFailed(err error, nodeName string, trunkENI TrunkENI,
	branches []*BranchENI) (ctrl.Result, error) {
	deleteErrors := b.deleteBranchInterfaces(nodeName, trunkENI, branches)
	if deleteErrors != nil && len(deleteErrors) > 0 {
		return ctrl.Result{}, fmt.Errorf("failed to create %v and delete created branches %+v", err, deleteErrors)
	}
	return ctrl.Result{}, err
}

// DeleteResources deletes the branch ENIs present in the annotation of the pod
func (b *branchENIProvider) DeleteResources(pod *v1.Pod) (ctrl.Result, error) {
	log := b.log.WithValues("pod namespace", pod.Namespace, "pod name", pod.Name, "node name", pod.Spec.NodeName)
	start := time.Now()
	trunk, isPresent := b.getTrunkFromCache(pod.Spec.NodeName)
	if !isPresent {
		branchProviderOperationsErrCount.WithLabelValues("get_trunk_delete").Inc()
		return ctrl.Result{}, fmt.Errorf("trunk not found for node %s", pod.Spec.NodeName)
	}

	var branchENIs []*BranchENI
	podENIAnnotation := pod.Annotations[config.ResourceNamePodENI]
	if err := json.Unmarshal([]byte(podENIAnnotation), &branchENIs); err != nil {
		log.Error(err, "failed to unmarshal resource annotation", "annotation", podENIAnnotation)
		return ctrl.Result{}, err
	}

	err := b.deleteBranchInterfaces(pod.Spec.NodeName, trunk, branchENIs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("faield to delete branch ENI/s %+v", err)
	}

	branchProviderOperationLatency.WithLabelValues(operationDeleteBranchENI, string(len(branchENIs))).
		Observe(timeSinceMs(start))

	log.Info("deleted specified branch interface/s ", "interface id/s", branchENIs)

	return ctrl.Result{}, nil
}

// deleteBranchInterfaces deletes the all the branch interfaces provided as the argument belonging to the trunk ENI
func (b *branchENIProvider) deleteBranchInterfaces(nodeName string, trunk TrunkENI, branchENIs []*BranchENI) []error {
	log := b.log.WithValues("node", nodeName)
	var errors []error
	for _, branchENI := range branchENIs {
		err := trunk.DeleteBranchNetworkInterface(branchENI)
		if err != nil {
			branchProviderOperationsErrCount.WithLabelValues("delete_branch_eni").Inc()
			errors = append(errors, err)
			continue
		}
		log.Info("deleted branch network interface successfully", "id", branchENI.BranchENId)
	}
	return errors
}

// addTrunkToCache adds the trunk eni to cache, if the trunk already exists an error is thrown
func (b *branchENIProvider) addTrunkToCache(nodeName string, trunkENI TrunkENI) error {
	b.lock.Lock()
	defer b.lock.Unlock()

	log := b.log.WithValues("node", nodeName)

	if _, ok := b.trunkENICache[nodeName]; ok {
		branchProviderOperationsErrCount.WithLabelValues("add_to_cache").Inc()
		log.Error(ErrTrunkExistInCache, "trunk already exist in cache")
		return ErrTrunkExistInCache
	}

	b.trunkENICache[nodeName] = trunkENI
	log.Info("trunk added to cache successfully")
	return nil
}

// removeTrunkFromCache removes the trunk eni from cache for the given node name
func (b *branchENIProvider) removeTrunkFromCache(nodeName string) {
	b.lock.Lock()
	defer b.lock.Unlock()

	log := b.log.WithValues("node", nodeName)

	if _, ok := b.trunkENICache[nodeName]; !ok {
		branchProviderOperationsErrCount.WithLabelValues("remove_from_cache").Inc()
		// No need to propagate the error
		log.Error(ErrTrunkNotInCache, "trunk doesn't exist in cache")
		return
	}

	delete(b.trunkENICache, nodeName)
	log.Info("trunk removed from cache successfully")
	return
}

// getTrunkFromCache returns the trunkENI form the cache for the given node name
func (b *branchENIProvider) getTrunkFromCache(nodeName string) (trunkENI TrunkENI, present bool) {
	b.lock.RLock()
	defer b.lock.RUnlock()

	trunkENI, present = b.trunkENICache[nodeName]
	return
}
