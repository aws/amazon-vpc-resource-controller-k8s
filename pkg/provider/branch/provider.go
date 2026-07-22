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
	"slices"
	"strconv"
	"sync"
	"time"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	operationCreateBranchENI   = "create_branch_eni"
	operationAnnotateBranchENI = "annotate_branch_eni"
	operationInitTrunk         = "init_trunk"
	operationReconcileBranch   = "reconcile_unassigned_branch_enis"
	resourceCountLabel         = "resource_count"
	operationLabel             = "branch_provider_operation"
	resultLabel                = "result"
	reasonLabel                = "reason"
	pathLabel                  = "path"

	trunkInitPathEC2           = "ec2"
	trunkInitPathCNINodeStatus = "cninode_status"
	resultSuccess              = "success"
	resultError                = "error"
	resultHit                  = "hit"
	resultMiss                 = "miss"

	// self-heal result label values for cniNodeStatusSelfHealCount.
	selfHealResultUpToDate    = "up_to_date"   // status already populated with the current snapshot, nothing to do
	selfHealResultPatched     = "patched"      // status was empty/stale and successfully re-persisted
	selfHealResultError       = "error"        // failed to read the CNINode or patch its status
	selfHealResultNotReady    = "not_ready"    // node not ready / trunk not initialized yet, skipped
	selfHealResultTrunkAbsent = "trunk_absent" // no trunk in cache for the node, skipped

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

	cniNodeStatusFastPathCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cninode_status_fast_path_total",
			Help: "The number of attempts to initialize trunk cache from CNINode status",
		},
		[]string{resultLabel, reasonLabel},
	)

	trunkCacheRebuildLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "trunk_cache_rebuild_latency",
			Help:       "Trunk cache rebuild latency in seconds by initialization path",
			Objectives: map[float64]float64{0: 0, 0.5: 0.05, 0.9: 0.01, 0.99: 0.001, 1: 0},
		},
		[]string{pathLabel, resultLabel},
	)

	cniNodeStatusBackgroundReconcileCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cninode_status_background_reconcile_total",
			Help: "The number of background EC2 reconciles after CNINode status fast path initialization",
		},
		[]string{resultLabel},
	)

	cniNodeStatusSelfHealCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cninode_status_self_heal_total",
			Help: "The number of periodic self-heal patches that attempted to repopulate an empty or stale CNINode status snapshot",
		},
		[]string{resultLabel},
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
			branchProviderOperationLatency,
			cniNodeStatusFastPathCount,
			trunkCacheRebuildLatency,
			cniNodeStatusBackgroundReconcileCount,
			cniNodeStatusSelfHealCount)

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

	if _, err := b.initTrunk(instance, trunkENI, podList, log); err != nil {
		return b.handleInitTrunkFailure(instance, nodeName, err)
	}

	branchProviderOperationLatency.WithLabelValues(operationInitTrunk, "1").Observe(timeSinceSeconds(start))

	// Add the Trunk ENI to cache if it does not already exist
	if err := b.addTrunkToCache(nodeName, trunkENI); err != nil && err != ErrTrunkExistInCache {
		branchProviderOperationsErrCount.WithLabelValues("add_trunk_to_cache").Inc()
		return err
	}

	// The orphan branch-ENI reclaim (ReconcileUnassignedBranchENIs) is intentionally NOT triggered
	// here. It issues an EC2 DescribeNetworkInterfaces per node, so triggering it on re-init produced a
	// describe flood at fleet scale on every controller restart. It now runs on the existing per-node
	// reconcile timer instead (see ReconcileNode), keeping re-init free of EC2 orphan-reclaim calls.

	// TODO: For efficiency submit the process delete queue job only when the delete queue has items.
	// Submit periodic jobs for the given node name
	b.SubmitAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(nodeName))

	b.log.Info("initialized the resource provider successfully")

	// send an event to notify user this node has trunk interface initialized
	utils.SendNodeEventWithNodeName(b.apiWrapper.K8sAPI, nodeName, utils.NodeTrunkInitiatedReason, "The node has trunk interface initialized successfully", v1.EventTypeNormal, b.log)

	return nil
}

func (b *branchENIProvider) initTrunk(
	instance ec2.EC2Instance,
	trunkENI trunk.TrunkENI,
	podList []v1.Pod,
	log logr.Logger,
) (bool, error) {
	if instance.LoadedFromCNINodeStatus() {
		statusStart := time.Now()
		// Non-cached API server read: a lagging informer cache on restart / leader change would
		// spuriously miss here and force the EC2 fallback, defeating the zero-EC2 re-init goal.
		cniNode, err := b.apiWrapper.K8sAPI.GetCNINodeFromAPIServer(types.NamespacedName{Name: instance.Name()})
		if err == nil {
			err = trunkENI.InitTrunkFromStatus(cniNode.Status.TrunkENI, podList)
			if err == nil {
				cniNodeStatusFastPathCount.WithLabelValues(resultHit, "status_valid").Inc()
				trunkCacheRebuildLatency.WithLabelValues(trunkInitPathCNINodeStatus, resultSuccess).
					Observe(timeSinceSeconds(statusStart))
				return true, nil
			}
			cniNodeStatusFastPathCount.WithLabelValues(resultMiss, "trunk_status_invalid").Inc()
			trunkCacheRebuildLatency.WithLabelValues(trunkInitPathCNINodeStatus, resultError).
				Observe(timeSinceSeconds(statusStart))
			log.Error(err, "failed to initialize trunk from CNINode status, falling back to EC2")
		} else {
			cniNodeStatusFastPathCount.WithLabelValues(resultMiss, "get_cninode_error").Inc()
			trunkCacheRebuildLatency.WithLabelValues(trunkInitPathCNINodeStatus, resultError).
				Observe(timeSinceSeconds(statusStart))
			log.Error(err, "failed to read CNINode status, falling back to EC2")
		}

		if err := instance.LoadDetails(b.apiWrapper.EC2API); err != nil {
			branchProviderOperationsErrCount.WithLabelValues("load_instance_details_fallback").Inc()
			return false, fmt.Errorf("loading instance details after CNINode status fallback: %w", err)
		}
	}

	ec2Start := time.Now()
	if err := trunkENI.InitTrunk(instance, podList); err != nil {
		trunkCacheRebuildLatency.WithLabelValues(trunkInitPathEC2, resultError).Observe(timeSinceSeconds(ec2Start))
		return false, err
	}
	trunkCacheRebuildLatency.WithLabelValues(trunkInitPathEC2, resultSuccess).Observe(timeSinceSeconds(ec2Start))
	b.persistCNINodeStatus(instance.Name(), instance, trunkENI, log)
	return false, nil
}

func (b *branchENIProvider) persistCNINodeStatus(
	nodeName string,
	instance ec2.EC2Instance,
	trunkENI trunk.TrunkENI,
	log logr.Logger,
) {
	status := rcv1alpha1.CNINodeStatus{
		SnapshotVersion: rcv1alpha1.CNINodeStatusSnapshotVersion,
		LastUpdated:     metav1.Now(),
		Instance:        instance.CNINodeStatus(),
		TrunkENI:        trunkENI.CNINodeStatus(),
	}
	if err := b.apiWrapper.K8sAPI.UpdateCNINodeStatus(nodeName, status); err != nil {
		branchProviderOperationsErrCount.WithLabelValues("update_cninode_status").Inc()
		log.Error(err, "failed to update CNINode status snapshot")
	}
}

func (b *branchENIProvider) handleInitTrunkFailure(instance ec2.EC2Instance, nodeName string, err error) error {
	// If it's an AWS Error, get the exit code without the error message to avoid
	// broadcasting multiple different messaged events
	var apiErr smithy.APIError

	if errors.As(err, &apiErr) {
		node, errGetNode := b.apiWrapper.K8sAPI.GetNode(instance.Name())
		if errGetNode != nil {
			return fmt.Errorf("failed to get node for event advertisement: %v: %v", errGetNode, err)
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
	case worker.OperationReconcileUnassignedBranchENIs:
		return b.ReconcileUnassignedBranchENIs(onDemandJob.NodeName)
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

	// NOTE: The EC2 orphan branch-ENI reclaim (ReconcileUnassignedBranchENIs) is intentionally NOT
	// submitted here. It issues a DescribeNetworkInterfaces per node, so running it on this fast
	// (1-15min) reconcile cadence is massively over-provisioned - orphans arise only from rare EC2 API
	// failures. It now runs on its own independent, low-frequency, jittered timer owned by the node
	// manager, which calls SubmitReconcileUnassignedBranchENIsJob on the slow cadence. The zero-EC2
	// Reconcile above stays on the fast cadence because it is free.

	return foundLeakedENI
}

// SubmitReconcileUnassignedBranchENIsJob submits the EC2 orphan branch-ENI reclaim as an async job.
// It is invoked by the node manager on the independent, low-frequency orphan sweep timer (separate
// from the fast per-node reconcile). Submitting as an async job reuses the existing delete-queue +
// VLAN-reserve safety net, and the job handler (ReconcileUnassignedBranchENIs) guards on
// trunk-in-cache so an un-hydrated ledger never mis-classifies attached ENIs as orphans.
func (b *branchENIProvider) SubmitReconcileUnassignedBranchENIsJob(nodeName string) {
	b.SubmitAsyncJob(worker.NewOnDemandReconcileUnassignedBranchENIsJob(nodeName))
}

// ReconcileCNINodeStatus is the periodic self-heal for the CNINode status snapshot. When a node's
// trunk is initialized in memory but its CNINode status is empty or stale, the inline persist during
// onboarding either never ran or lost the create/delete-recreate race, leaving hydrate unable to skip
// EC2 on the next re-init. This rebuilds the status snapshot from the cached (in-memory) trunk and
// re-persists it. It only ever PATCHes an existing CNINode via UpdateCNINodeStatus; it never creates
// one (creation belongs to AddNode and the CNINode controller's delete-recreate logic). It is
// event-driven and cheap: it makes zero EC2 calls and returns quickly. The caller is responsible for
// only invoking this once the node is ready.
func (b *branchENIProvider) ReconcileCNINodeStatus(nodeName string) {
	log := b.log.WithValues("node", nodeName)

	trunkENI, isPresent := b.getTrunkFromCache(nodeName)
	if !isPresent {
		// Trunk not initialized in memory yet; nothing to persist.
		cniNodeStatusSelfHealCount.WithLabelValues(selfHealResultTrunkAbsent).Inc()
		return
	}

	desired := rcv1alpha1.CNINodeStatus{
		SnapshotVersion: rcv1alpha1.CNINodeStatusSnapshotVersion,
		Instance:        trunkENI.InstanceStatus(),
		TrunkENI:        trunkENI.CNINodeStatus(),
	}
	// A trunk still missing its ID is not usable for hydrate; skip until it is set.
	if desired.TrunkENI.ID == "" {
		cniNodeStatusSelfHealCount.WithLabelValues(selfHealResultNotReady).Inc()
		return
	}

	cniNode, err := b.apiWrapper.K8sAPI.GetCNINode(types.NamespacedName{Name: nodeName})
	if err != nil {
		cniNodeStatusSelfHealCount.WithLabelValues(selfHealResultError).Inc()
		log.V(1).Info("self-heal skipped: could not read CNINode", "error", err)
		return
	}

	if cniNodeStatusUpToDate(cniNode.Status, desired) {
		cniNodeStatusSelfHealCount.WithLabelValues(selfHealResultUpToDate).Inc()
		return
	}

	desired.LastUpdated = metav1.Now()
	if err := b.apiWrapper.K8sAPI.UpdateCNINodeStatus(nodeName, desired); err != nil {
		cniNodeStatusSelfHealCount.WithLabelValues(selfHealResultError).Inc()
		branchProviderOperationsErrCount.WithLabelValues("self_heal_cninode_status").Inc()
		log.V(1).Info("self-heal failed to patch CNINode status, will retry next reconcile", "error", err)
		return
	}
	cniNodeStatusSelfHealCount.WithLabelValues(selfHealResultPatched).Inc()
	log.Info("self-healed CNINode status snapshot")
}

// cniNodeStatusUpToDate reports whether the persisted status already reflects the desired snapshot
// for the purposes of hydrate. It intentionally ignores LastUpdated (a timestamp that would always
// differ) and otherwise compares exactly the fields that HydrateFromCNINodeStatus
// (pkg/aws/ec2/instance.go) reads back into the in-memory instance: snapshot version, trunk identity
// (ID + subnet), and the full instance identity, subnet (v4/v6 CIDR + masks), both security-group
// sets, and connection-tracking settings. Compare and validate must stay in lockstep so a stale or
// partial snapshot (e.g. missing security groups) is treated as out-of-date and re-patched, rather
// than skipped only to fail hydrate on the next re-init. Security groups are compared as sets to
// mirror hydrate's order-independent sameStringSet semantics and avoid needless re-patch churn.
func cniNodeStatusUpToDate(persisted, desired rcv1alpha1.CNINodeStatus) bool {
	if persisted.SnapshotVersion != desired.SnapshotVersion {
		return false
	}
	if persisted.TrunkENI.ID != desired.TrunkENI.ID ||
		persisted.TrunkENI.SubnetID != desired.TrunkENI.SubnetID {
		return false
	}
	pi, di := persisted.Instance, desired.Instance
	if pi.InstanceID != di.InstanceID ||
		pi.InstanceType != di.InstanceType ||
		pi.InstanceSubnetID != di.InstanceSubnetID ||
		pi.InstanceSubnetCIDRBlock != di.InstanceSubnetCIDRBlock ||
		pi.InstanceSubnetV6CIDRBlock != di.InstanceSubnetV6CIDRBlock ||
		pi.CurrentSubnetID != di.CurrentSubnetID ||
		pi.CurrentSubnetCIDRBlock != di.CurrentSubnetCIDRBlock ||
		pi.CurrentSubnetV6CIDRBlock != di.CurrentSubnetV6CIDRBlock ||
		pi.SubnetMask != di.SubnetMask ||
		pi.SubnetV6Mask != di.SubnetV6Mask ||
		pi.PrimaryNetworkInterfaceID != di.PrimaryNetworkInterfaceID {
		return false
	}
	if !sameStringSet(pi.CurrentInstanceSecurityGroups, di.CurrentInstanceSecurityGroups) ||
		!sameStringSet(pi.PrimaryNetworkInterfaceSecurityGroups, di.PrimaryNetworkInterfaceSecurityGroups) {
		return false
	}
	if !sameConnectionTracking(pi.ConnectionTracking, di.ConnectionTracking) {
		return false
	}
	return true
}

// sameStringSet reports whether two string slices contain the same elements, ignoring order and
// duplicates-position. Mirrors ec2.sameStringSet so self-heal comparison and hydrate validation
// treat security-group ordering identically.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = slices.Clone(a)
	b = slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// sameConnectionTracking reports whether two connection-tracking snapshots are equivalent, treating
// a nil struct as equivalent to one with all-nil timeout pointers (both mean "no override recorded").
func sameConnectionTracking(a, b *rcv1alpha1.ConnectionTrackingStatus) bool {
	return sameInt32Ptr(connTrackTCP(a), connTrackTCP(b)) &&
		sameInt32Ptr(connTrackUDPStream(a), connTrackUDPStream(b)) &&
		sameInt32Ptr(connTrackUDP(a), connTrackUDP(b))
}

func connTrackTCP(c *rcv1alpha1.ConnectionTrackingStatus) *int32 {
	if c == nil {
		return nil
	}
	return c.TCPEstablishedTimeout
}

func connTrackUDPStream(c *rcv1alpha1.ConnectionTrackingStatus) *int32 {
	if c == nil {
		return nil
	}
	return c.UDPStreamTimeout
}

func connTrackUDP(c *rcv1alpha1.ConnectionTrackingStatus) *int32 {
	if c == nil {
		return nil
	}
	return c.UDPTimeout
}

func sameInt32Ptr(a, b *int32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
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

func (b *branchENIProvider) ReconcileUnassignedBranchENIs(nodeName string) (ctrl.Result, error) {
	trunkENI, isPresent := b.getTrunkFromCache(nodeName)
	log := b.log.WithValues("node", nodeName, "operation", operationReconcileBranch)
	if !isPresent {
		log.Info("stopping background branch ENI reconcile job")
		return ctrl.Result{}, nil
	}

	foundUnassignedBranchENI, err := trunkENI.ReconcileUnassignedBranchENIs()
	if err != nil {
		cniNodeStatusBackgroundReconcileCount.WithLabelValues(resultError).Inc()
		branchProviderOperationsErrCount.WithLabelValues(operationReconcileBranch).Inc()
		return ctrl.Result{}, err
	}

	cniNodeStatusBackgroundReconcileCount.WithLabelValues(resultSuccess).Inc()
	if foundUnassignedBranchENI {
		b.SubmitAsyncJob(worker.NewOnDemandProcessDeleteQueueJob(nodeName))
	}
	return ctrl.Result{}, nil
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
