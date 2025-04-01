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

package branch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/cooldown"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/aws/smithy-go"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	operationCreateBranchENI   = "create_branch_eni"
	operationAnnotateBranchENI = "annotate_branch_eni"
	operationInitTrunk         = "init_trunk"
	resourceCountLabel         = "resource_count"
	operationLabel             = "branch_provider_operation"

	ReasonSecurityGroupRequested    = "SecurityGroupRequested"
	ReasonResourceAllocated         = "ResourceAllocated"
	ReasonBranchAllocationFailed    = "BranchAllocationFailed"
	ReasonBranchENIAnnotationFailed = "BranchENIAnnotationFailed"

	ReasonTrunkENICreationFailed = "TrunkENICreationFailed"
)

var (
	branchProviderOperationsErrCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "branch_provider_operations_err_count",
			Help: "The number of errors encountered for branch provider operations",
		},
		[]string{operationLabel},
	)

	branchProviderOperationLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "branch_provider_operation_latency",
			Help:       "Branch Provider operations latency in seconds",
			Objectives: map[float64]float64{0: 0, 0.5: 0.05, 0.9: 0.01, 0.99: 0.001, 1: 0},
		},
		[]string{operationLabel, resourceCountLabel},
	)

	deleteQueueRequeueRequest = ctrl.Result{RequeueAfter: time.Second * 30, Requeue: true}

	// NodeDeleteRequeueRequestDelay represents the time after which the resources belonging to a node will be cleaned
	// up after receiving the actual node delete event.
	NodeDeleteRequeueRequestDelay = time.Minute * 1

	prometheusRegistered = false

	ErrTrunkExistInCache = fmt.Errorf("trunk eni already exist in cache")
	ErrTrunkNotInCache   = fmt.Errorf("trunk eni not present in cache")
)

// branchENIProvider provides branch ENI to all nodes that support Trunk network interface
type branchENIProvider struct {
	// log is the logger initialized with branch eni provider value
	log logr.Logger
	// lock to prevent concurrent writes to the trunk eni map
	lock sync.RWMutex
	// trunkENICache is the map of node name to the trunk ENI
	trunkENICache map[string]trunk.TrunkENI
	// workerPool is the worker pool and queue for submitting async job
	workerPool worker.Worker
	// apiWrapper
	apiWrapper api.Wrapper
	ctx        context.Context
	checker    healthz.Checker
}

// NewBranchENIProvider returns the Branch ENI Provider for all nodes across the cluster
func NewBranchENIProvider(logger logr.Logger, wrapper api.Wrapper,
	worker worker.Worker, _ config.ResourceConfig, ctx context.Context,
) provider.ResourceProvider {
	prometheusRegister()
	trunk.PrometheusRegister()

	provider := &branchENIProvider{
		apiWrapper:    wrapper,
		log:           logger,
		workerPool:    worker,
		trunkENICache: make(map[string]trunk.TrunkENI),
		ctx:           ctx,
	}
	provider.checker = provider.check()
	return provider
}

// prometheusRegister registers prometheus metrics
func prometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(
			branchProviderOperationsErrCount,
			branchProviderOperationLatency)

		prometheusRegistered = true
	}
}

// timeSinceSeconds returns the time elapsed in seconds from the start time
func timeSinceSeconds(start time.Time) float64 {
	return float64(time.Since(start).Seconds())
}

// InitResources initialized the resource for the given node name. The initialized trunk ENI is stored in
// cache for use in future Create/Delete Requests
func (b *branchENIProvider) InitResource(instance ec2.EC2Instance) error {
	nodeName := instance.Name()
	log := b.log.WithValues("nodeName", nodeName)
	trunkENI := trunk.NewTrunkENI(log, instance, b.apiWrapper.EC2API)

	// Initialize the Trunk ENI
	start := time.Now()

	podList, err := b.apiWrapper.PodAPI.GetRunningPodsOnNode(nodeName)
	if err != nil {
		log.Error(err, "failed to get list of pod on node")
		return err
	}

	if err := trunkENI.InitTrunk(instance, podList); err != nil {
		// If it's an AWS Error, get the exit code without the error message to avoid
		// broadcasting multiple different messaged events

		var apiErr smithy.APIError

		if errors.As(err, &apiErr) {

			node, errGetNode := b.apiWrapper.K8sAPI.GetNode(instance.Name())
			if errGetNode != nil {
				return fmt.Errorf("failed to get node for event advertisment: %v: %v", errGetNode, err)
			}
			eventMessage := fmt.Sprintf("Failed to create trunk interface: "+
				"Error Code: %s", apiErr.ErrorCode())
			if apiErr.ErrorCode() == "UnauthorizedOperation" {
				// Append resolution to the event message for users for common error
				eventMessage = fmt.Sprintf("%s: %s", eventMessage,
					"Please verify the cluster IAM role has AmazonEKSVPCResourceController policy")
			}
			b.apiWrapper.K8sAPI.BroadcastEvent(node, ReasonTrunkENICreationFailed, eventMessage, v1.EventTypeWarning)
		}

		utils.SendNodeEventWithNodeName(b.apiWrapper.K8sAPI, nodeName, utils.NodeTrunkFailedInitializationReason, "The node failed initializing trunk interface", v1.EventTypeNormal, b.log)
		branchProviderOperationsErrCount.WithLabelValues("init").Inc()
		return fmt.Errorf("initializing trunk, %w", err)
	}
	branchProviderOperationLatency.WithLabelValues(operationInitTrunk, "1").Observe(timeSinceSeconds(start))

	// Add the Trunk ENI to cache if it does not already exist
	if err := b.addTrunkToCache(nodeName, trunkENI); err != nil && err != ErrTrunkExistInCache {
		branchProviderOperationsErrCount.WithLabelValues("add_trunk_to_cache").Inc()
		return err
	}

	// TODO: For efficiency submit the process delete queue job only when the delete queue has items.
	// Submit periodic jobs for the given node name
	b.SubmitAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(nodeName))

	b.log.Info("initialized the resource provider successfully")

	// send an event to notify user this node has trunk interface initialized
	utils.SendNodeEventWithNodeName(b.apiWrapper.K8sAPI, nodeName, utils.NodeTrunkInitiatedReason, "The node has trunk interface initialized successfully", v1.EventTypeNormal, b.log)

	return nil
}

// DeInitResources adds a an asynchronous delete job to the worker which will execute after a certain period.
// This is done because we receive the Node Delete Event First and the Pods are evicted after the node no longer exists
// leading to all the pod events to be ignored since the node has been de initialized and hence leaking branch ENs.
func (b *branchENIProvider) DeInitResource(instance ec2.EC2Instance) error {
	nodeName := instance.Name()
	b.log.Info("will clean up resources later to allow pods to be evicted first",
		"node name", nodeName, "cleanup after", NodeDeleteRequeueRequestDelay)
	b.workerPool.SubmitJobAfter(worker.NewOnDemandDeleteNodeJob(nodeName), NodeDeleteRequeueRequestDelay)
	return nil
}

// SubmitAsyncJob submits the job to the k8s worker queue and returns immediately without waiting for the job to
// complete. Using the k8s worker queue features we can ensure that the same job is not submitted more than once.
func (b *branchENIProvider) SubmitAsyncJob(job interface{}) {
	b.workerPool.SubmitJob(job)
}

// ProcessAsyncJob is the job being executed in the worker pool routine. The job must be submitted using the
// SubmitAsyncJob in order to be processed asynchronously by the caller.
func (b *branchENIProvider) ProcessAsyncJob(job interface{}) (ctrl.Result, error) {
	onDemandJob, isValid := job.(worker.OnDemandJob)
	if !isValid {
		return ctrl.Result{}, fmt.Errorf("invalid job type")
	}

	switch onDemandJob.Operation {
	case worker.OperationCreate:
		return b.CreateAndAnnotateResources(onDemandJob.PodNamespace, onDemandJob.PodName, onDemandJob.RequestCount)
	case worker.OperationDeleted:
		return b.DeleteBranchUsedByPods(onDemandJob.NodeName, onDemandJob.UID)
	case worker.OperationProcessDeleteQueue:
		return b.ProcessDeleteQueue(onDemandJob.NodeName)
	case worker.OperationDeleteNode:
		return b.DeleteNode(onDemandJob.NodeName)
	}

	return ctrl.Result{}, fmt.Errorf("unsupported operation type")
}

// DeleteNode deletes all the cached branch ENIs associated with the trunk and removes the trunk from the cache.
func (b *branchENIProvider) DeleteNode(nodeName string) (ctrl.Result, error) {
	_, isPresent := b.getTrunkFromCache(nodeName)
	if !isPresent {
		return ctrl.Result{}, fmt.Errorf("failed to find node %s", nodeName)
	}

	// At this point, the finalizer routine should have deleted all available branch ENIs
	// Any leaked ENIs will be deleted by the periodic cleanup routine if cluster is active
	// remove trunk from cache and de-initializer the resource provider
	b.removeTrunkFromCache(nodeName)

	b.log.Info("de-initialized resource provider successfully", "nodeName", nodeName)

	return ctrl.Result{}, nil
}

// GetResourceCapacity returns the resource capacity for the given instance.
func (b *branchENIProvider) UpdateResourceCapacity(instance ec2.EC2Instance) error {
	instanceName := instance.Name()
	instanceType := instance.Type()
	capacity := vpc.Limits[instanceType].BranchInterface

	if capacity != 0 {
		err := b.apiWrapper.K8sAPI.AdvertiseCapacityIfNotSet(instanceName, config.ResourceNamePodENI, capacity)
		if err != nil {
			branchProviderOperationsErrCount.WithLabelValues("advertise_capacity").Inc()
			return err
		}
		b.log.V(1).Info("advertised capacity", "instance", instanceName,
			"instance type", instanceType, "capacity", capacity)
	}
	return nil
}

// ReconcileNode reconciles a nodes by getting the list of pods from K8s and comparing the result
// with the internal cache.
func (b *branchENIProvider) ReconcileNode(nodeName string) bool {
	trunkENI, isPresent := b.getTrunkFromCache(nodeName)
	log := b.log.WithValues("node", nodeName)
	if !isPresent {
		// return true to set the node next clean up asap since we don't know why trunk is missing
		log.V(1).Info("trunk ENI not found, requeue node", "nodeName", nodeName)
		return true
	}
	podList, err := b.apiWrapper.PodAPI.ListPods(nodeName)
	if err != nil {
		// return true to set the node next cleanup asap since the LIST call may fail for other reasons
		// we should assume that there are leaked resources need to be cleaned up
		log.Error(err, "failed to list pods, requeue node", "nodeName", nodeName)
		return true
	}
	foundLeakedENI := trunkENI.Reconcile(podList.Items)
	return foundLeakedENI
}

// ProcessDeleteQueue removes cooled down ENIs associated with a trunk for a given node
func (b *branchENIProvider) ProcessDeleteQueue(nodeName string) (ctrl.Result, error) {
	trunkENI, isPresent := b.getTrunkFromCache(nodeName)
	log := b.log.WithValues("node", nodeName)
	if !isPresent {
		log.Info("stopping the process delete queue job")
		return ctrl.Result{}, nil
	}
	trunkENI.DeleteCooledDownENIs()
	return deleteQueueRequeueRequest, nil
}

// CreateAndAnnotateResources creates resource for the pod, the function can run concurrently for different pods without
// any locking as long as caller guarantees this function is not called concurrently for same pods.
func (b *branchENIProvider) CreateAndAnnotateResources(podNamespace string, podName string, resourceCount int) (ctrl.Result, error) {
	// Get the pod from cache
	pod, err := b.apiWrapper.PodAPI.GetPod(podNamespace, podName)
	if err != nil {
		branchProviderOperationsErrCount.WithLabelValues("create_get_pod").Inc()
		return ctrl.Result{}, err
	}

	if _, ok := pod.Annotations[config.ResourceNamePodENI]; ok {
		// Pod from cache already has annotation, skip the job
		return ctrl.Result{}, nil
	}

	// Get the pod object again directly from API Server as the cache can be stale
	pod, err = b.apiWrapper.PodAPI.GetPodFromAPIServer(b.ctx, podNamespace, podName)
	if err != nil {
		branchProviderOperationsErrCount.WithLabelValues("get_pod_api_server").Inc()
		return ctrl.Result{}, err
	}

	if _, ok := pod.Annotations[config.ResourceNamePodENI]; ok {
		// Pod doesn't have an annotation yet. Create Branch ENI and annotate the pod
		b.log.Info("skipping pod event as the pod already has pod-eni allocated",
			"namespace", pod.Namespace, "name", pod.Name)
		return ctrl.Result{}, nil
	}

	securityGroups, err := b.apiWrapper.SGPAPI.GetMatchingSecurityGroupForPods(pod)
	if err != nil {
		return ctrl.Result{}, err
	}

	if len(securityGroups) == 0 {
		b.apiWrapper.K8sAPI.BroadcastEvent(pod, ReasonSecurityGroupRequested,
			"Pod will get the instance security group as the pod didn't match any Security Group from "+
				"SecurityGroupPolicy", v1.EventTypeWarning)
	} else {
		b.apiWrapper.K8sAPI.BroadcastEvent(pod, ReasonSecurityGroupRequested, fmt.Sprintf("Pod will get the following "+
			"Security Groups %v", securityGroups), v1.EventTypeNormal)
	}

	log := b.log.WithValues("pod namespace", pod.Namespace, "pod name", pod.Name, "nodeName", pod.Spec.NodeName)

	start := time.Now()
	trunkENI, isPresent := b.getTrunkFromCache(pod.Spec.NodeName)
	if !isPresent {
		// This should never happen
		branchProviderOperationsErrCount.WithLabelValues("get_trunk_create").Inc()
		return ctrl.Result{}, fmt.Errorf("trunk not found for node %s", pod.Spec.NodeName)
	}

	// Get the list of branch ENIs that will be allocated to the pod object
	branchENIs, err := trunkENI.CreateAndAssociateBranchENIs(pod, securityGroups, resourceCount)
	if err != nil {
		if err == trunk.ErrCurrentlyAtMaxCapacity {
			return ctrl.Result{RequeueAfter: cooldown.GetCoolDown().GetCoolDownPeriod(), Requeue: true}, nil
		}
		b.apiWrapper.K8sAPI.BroadcastEvent(pod, ReasonBranchAllocationFailed,
			fmt.Sprintf("failed to allocate branch ENI to pod: %v", err), v1.EventTypeWarning)
		return ctrl.Result{}, err
	}

	branchProviderOperationLatency.WithLabelValues(operationCreateBranchENI, strconv.Itoa(resourceCount)).
		Observe(timeSinceSeconds(start))

	jsonBytes, err := json.Marshal(branchENIs)
	if err != nil {
		trunkENI.PushENIsToFrontOfDeleteQueue(pod, branchENIs)
		b.log.Info("pushed the ENIs to the delete queue as failed to unmarshal ENI details", "ENI/s", branchENIs)
		branchProviderOperationsErrCount.WithLabelValues("annotate_branch_eni").Inc()
		return ctrl.Result{}, err
	}

	start = time.Now()
	// Annotate the pod with the created resources
	err = b.apiWrapper.PodAPI.AnnotatePod(pod.Namespace, pod.Name, pod.UID,
		config.ResourceNamePodENI, string(jsonBytes))
	if err != nil {
		trunkENI.PushENIsToFrontOfDeleteQueue(pod, branchENIs)
		b.log.Info("pushed the ENIs to the delete queue as failed to annotate the pod", "ENI/s", branchENIs)
		b.apiWrapper.K8sAPI.BroadcastEvent(pod, ReasonBranchENIAnnotationFailed,
			fmt.Sprintf("failed to annotate pod with branch ENI details: %v", err), v1.EventTypeWarning)
		branchProviderOperationsErrCount.WithLabelValues("annotate_branch_eni").Inc()
		return ctrl.Result{}, err
	}

	// Broadcast event to indicate the resource has been successfully created and annotated to the pod object
	b.apiWrapper.K8sAPI.BroadcastEvent(pod, ReasonResourceAllocated,
		fmt.Sprintf("Allocated %s to the pod", string(jsonBytes)), v1.EventTypeNormal)

	branchProviderOperationLatency.WithLabelValues(operationAnnotateBranchENI, strconv.Itoa(resourceCount)).
		Observe(timeSinceSeconds(start))

	log.Info("created and annotated branch interface/s successfully", "branches", branchENIs)

	return ctrl.Result{}, nil
}

func (b *branchENIProvider) DeleteBranchUsedByPods(nodeName string, UID string) (ctrl.Result, error) {
	trunkENI, isPresent := b.getTrunkFromCache(nodeName)
	if !isPresent {
		// trunk cache is local map with lock. it shouldn't return not found error if trunk exists
		// if the node's trunk is not found, we shouldn't retry
		// worst case we rely on node based clean up goroutines to clean branch ENIs up
		b.log.Info("failed to find trunk ENI for the node", "nodeName", nodeName)
		return ctrl.Result{}, nil
	}

	trunkENI.PushBranchENIsToCoolDownQueue(UID)

	return ctrl.Result{}, nil
}

// addTrunkToCache adds the trunk eni to cache, if the trunk already exists an error is thrown
func (b *branchENIProvider) addTrunkToCache(nodeName string, trunkENI trunk.TrunkENI) error {
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
}

// getTrunkFromCache returns the trunkENI form the cache for the given node name
func (b *branchENIProvider) getTrunkFromCache(nodeName string) (trunkENI trunk.TrunkENI, present bool) {
	b.lock.RLock()
	defer b.lock.RUnlock()

	trunkENI, present = b.trunkENICache[nodeName]
	return
}

// GetPool is not supported for Branch ENI
func (b *branchENIProvider) GetPool(_ string) (pool.Pool, bool) {
	return nil, false
}

// IsInstanceSupported returns true for linux node as pod eni is only supported for linux worker node
func (b *branchENIProvider) IsInstanceSupported(instance ec2.EC2Instance) bool {
	if instance.Os() != config.OSLinux {
		return false
	}

	limits, found := vpc.Limits[instance.Type()]
	supported := found && limits.IsTrunkingCompatible

	if !supported {
		// Send a node event for users' visibility
		msg := fmt.Sprintf("The instance type %s is not supported for trunk interface (Security Group for Pods)", instance.Type())
		utils.SendNodeEventWithNodeName(b.apiWrapper.K8sAPI, instance.Name(), utils.UnsupportedInstanceTypeReason, msg, v1.EventTypeWarning, b.log)
	}

	return supported
}

func (b *branchENIProvider) Introspect() interface{} {
	b.lock.RLock()
	defer b.lock.RUnlock()

	allResponse := make(map[string]trunk.IntrospectResponse)

	for nodeName, trunkENI := range b.trunkENICache {
		response := trunkENI.Introspect()
		allResponse[nodeName] = response
	}
	return allResponse
}

func (b *branchENIProvider) IntrospectSummary() interface{} {
	b.lock.RLock()
	defer b.lock.RUnlock()

	allResponse := make(map[string]trunk.IntrospectSummaryResponse)

	for nodeName, trunkENI := range b.trunkENICache {
		response := trunkENI.Introspect()
		allResponse[nodeName] = changeToIntrospectSummary(response)
	}
	return allResponse
}

func changeToIntrospectSummary(details trunk.IntrospectResponse) trunk.IntrospectSummaryResponse {
	return trunk.IntrospectSummaryResponse{
		TrunkENIID:     details.TrunkENIID,
		InstanceID:     details.InstanceID,
		BranchENICount: len(details.PodToBranchENI),
		DeleteQueueLen: len(details.DeleteQueue),
	}
}

func (b *branchENIProvider) IntrospectNode(nodeName string) interface{} {
	b.lock.RLock()
	defer b.lock.RUnlock()

	trunkENI, found := b.trunkENICache[nodeName]
	if !found {
		return struct{}{}
	}
	return trunkENI.Introspect()
}

func (b *branchENIProvider) check() healthz.Checker {
	b.log.Info("Branch provider's healthz subpath was added")
	return func(req *http.Request) error {
		err := rcHealthz.PingWithTimeout(func(c chan<- error) {
			var ping interface{}
			// check on job queue
			b.SubmitAsyncJob(ping)
			// check on trunk cache map
			testNodeName := "test-node" + uuid.New().String()
			trunk, found := b.getTrunkFromCache(testNodeName)
			b.log.V(1).Info("healthz check vulnerable site on locks around trunk map", "TestTrunk", trunk, "FoundInCache", found)
			b.log.V(1).Info("***** health check on branch ENI provider tested SubmitAsyncJob *****")
			c <- nil
		}, b.log)

		return err
	}
}

func (b *branchENIProvider) GetHealthChecker() healthz.Checker {
	return b.checker
}
