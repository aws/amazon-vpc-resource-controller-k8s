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
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Prometheus metrics
var (
	prometheusRegisterOnce sync.Once

	// nodeInitStageLatency measures the latency in seconds of the load-details stage of a node Init
	// job. It pairs with nodeReinitEC2Skipped: on a hydrated re-init the EC2 LoadDetails is skipped, so
	// this metric naturally records ~0 observations, which is the direct proof that the zero-EC2 fast
	// path engaged.
	nodeInitStageLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "node_init_stage_latency",
			Help: "Latency in seconds of the serial stages of a node Init job",
			// Mirrors the objectives used by branch_provider_operation_latency
			// (pkg/provider/branch/provider.go) so all controller latency summaries share quantiles.
			Objectives: map[float64]float64{0: 0, 0.5: 0.05, 0.9: 0.01, 0.99: 0.001, 1: 0},
		},
		[]string{"stage"},
	)

	// nodeReinitEC2Skipped counts, per node Init, whether the instance details were rebuilt from the
	// persisted CNINode status snapshot (result="hydrated", the EC2 DescribeInstances in LoadDetails
	// was skipped) or the node fell back to the EC2 cold-start path (result="ec2_fallback", LoadDetails
	// ran). This is the direct proof signal that re-init after a controller/leader restart engages the
	// zero-EC2 fast path. It pairs with node_init_stage_latency{stage="load_details"}, which naturally
	// records ~0 observations on hydrated re-inits since LoadDetails is skipped.
	nodeReinitEC2Skipped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "node_reinit_ec2_skipped_total",
			Help: "Count of node Init jobs that hydrated instance details from the CNINode status snapshot (EC2 skipped) versus those that fell back to EC2 LoadDetails",
		},
		[]string{"result"},
	)
)

const (
	stageLoadDetails = "load_details"

	// result label values for nodeReinitEC2Skipped.
	reinitResultHydrated    = "hydrated"
	reinitResultEC2Fallback = "ec2_fallback"
)

// prometheusRegister registers the node prometheus metrics.
func prometheusRegister() {
	prometheusRegisterOnce.Do(func() {
		metrics.Registry.MustRegister(nodeInitStageLatency, nodeReinitEC2Skipped)
	})
}

type node struct {
	// lock to perform serial operations on a node
	lock sync.RWMutex
	// log is the logger setup with the key value pair set to node's name
	log logr.Logger
	// ready status indicates if the node is ready to process request or not
	ready bool
	// managed status indicates if the node is managed by the controller or not
	// This field should stay immutable in the lifecycle of node object
	managed bool
	// instance stores the ec2 instance details that is shared by all the providers
	instance ec2.EC2Instance
	// node has reference to k8s APIs
	k8sAPI k8s.K8sWrapper
	// node has reference to EC2 APIs
	ec2API api.EC2APIHelper
	// lastReconciledTime time.Time
	nextReconciliationTime time.Time
	// reconciliation interval between cleanups
	reconciliationInterval time.Duration
}

const (
	MaxNodeReconciliationInterval = 15 * time.Minute
	NodeInitialCleanupInterval    = 1 * time.Minute
)

// ErrInitResources to wrap error messages for all errors encountered
// during node initialization so the node can be de-registered on failure
type ErrInitResources struct {
	Message string
	Err     error
}

func (e *ErrInitResources) Error() string {
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

type Node interface {
	InitResources(resourceManager resource.ResourceManager) error
	DeleteResources(resourceManager resource.ResourceManager) error
	UpdateResources(resourceManager resource.ResourceManager) error

	UpdateCustomNetworkingSpecs(subnetID string, securityGroup []string)
	IsReady() bool
	IsManaged() bool
	IsNitroInstance() bool

	GetNodeInstanceID() string
	HasInstance() bool

	GetNextReconciliationTime() time.Time
	SetNextReconciliationTime(time time.Time)
	GetReconciliationInterval() time.Duration
	SetReconciliationInterval(time time.Duration)
}

// NewManagedNode returns node managed by the controller
func NewManagedNode(log logr.Logger, nodeName string, instanceID string, os string, k8sAPI k8s.K8sWrapper, ec2API api.EC2APIHelper) Node {
	prometheusRegister()
	return &node{
		managed: true,
		log: log.WithName("node resource handler").
			WithValues("node name", nodeName),
		instance:               ec2.NewEC2Instance(nodeName, instanceID, os, log.WithName("ec2instance")),
		k8sAPI:                 k8sAPI,
		ec2API:                 ec2API,
		reconciliationInterval: NodeInitialCleanupInterval,
	}
}

// NewUnManagedNode returns a node that's not managed by the controller
func NewUnManagedNode(log logr.Logger, nodeName, instanceID, os string) Node {
	// We should initialize instance for unmanaged node as well
	// only operate on managed node because unmanaged node has no instance initialized
	// operating on them could lead to nil pointer dereference
	return &node{
		managed: false,
		log: log.WithName("node resource handler").
			WithValues("node name", nodeName),
		instance: ec2.NewEC2Instance(nodeName, instanceID, os, log.WithName("ec2instance")),
	}
}

// UpdateNode refreshes the capacity if it's reset to 0
func (n *node) UpdateResources(resourceManager resource.ResourceManager) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	// It's possible to receive an update event on the node when the resource is still being
	// initialized, in that scenario we should not advertise the capacity. Capacity should only
	// be advertised when all the resources are initialized
	if !n.ready {
		n.log.Info("node is not initialized yet, will not advertise the capacity")
		return nil
	}

	var errUpdates []error
	for _, resourceProvider := range resourceManager.GetResourceProviders() {
		if resourceProvider.IsInstanceSupported(n.instance) {
			err := resourceProvider.UpdateResourceCapacity(n.instance)
			if err != nil {
				n.log.Error(err, "failed to initialize resource capacity")
				errUpdates = append(errUpdates, err)
			}
		}
	}
	if len(errUpdates) > 0 {
		return fmt.Errorf("failed to update one or more resources %v", errUpdates)
	}

	err := n.instance.UpdateCurrentSubnetAndCidrBlock(n.ec2API)
	if err != nil {
		n.log.Error(err, "failed to update cidr block", "instance", n.instance.Name())
	}

	return nil
}

// InitResources initializes the resource pool and provider of all supported resources
func (n *node) InitResources(resourceManager resource.ResourceManager) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	// Fast path: try to hydrate the instance from the persisted CNINode status
	// snapshot, which skips the expensive EC2 LoadDetails on re-init. Only fall
	// back to LoadDetails (cold-start / first-init path) when hydration fails.
	if n.tryHydrateInstanceFromCNINodeStatus() {
		// Hydrated from CNINode status: LoadDetails (EC2 DescribeInstances) is skipped.
		nodeReinitEC2Skipped.WithLabelValues(reinitResultHydrated).Inc()
	} else {
		// Fell back to the EC2 cold-start path: LoadDetails runs.
		nodeReinitEC2Skipped.WithLabelValues(reinitResultEC2Fallback).Inc()
		loadDetailsStart := time.Now()
		err := n.instance.LoadDetails(n.ec2API)
		nodeInitStageLatency.WithLabelValues(stageLoadDetails).Observe(time.Since(loadDetailsStart).Seconds())
		if err != nil {
			if errors.Is(err, utils.ErrNotFound) {
				// Send a node event for users' visibility
				msg := fmt.Sprintf("The instance type %s is not supported yet by the vpc resource controller", n.instance.Type())
				utils.SendNodeEventWithNodeName(n.k8sAPI, n.instance.Name(), utils.UnsupportedInstanceTypeReason, msg, v1.EventTypeWarning, n.log)
			}
			return &ErrInitResources{
				Message: "failed to load instance details",
				Err:     err,
			}
		}
	}

	var initializedProviders []provider.ResourceProvider
	var errInit error
	for _, resourceProvider := range resourceManager.GetResourceProviders() {
		// Check if the instance is supported and then initialize the provider
		if resourceProvider.IsInstanceSupported(n.instance) {
			errInit = resourceProvider.InitResource(n.instance)
			if errInit != nil {
				break
			}
			initializedProviders = append(initializedProviders, resourceProvider)
		}
	}

	if errInit != nil {
		// de-init all the providers that were already initialized, we will retry initialization
		// in next resync period
		for _, resourceProvider := range initializedProviders {
			errDeInit := resourceProvider.DeInitResource(n.instance)
			n.log.Error(errDeInit, "failed to de initialize resource")
		}
		// Return ErrInitResources so that the manager removes the node from the
		// cache allowing it to be retried in next sync period.
		return &ErrInitResources{
			Message: "failed to init resources",
			Err:     errInit,
		}
	}

	n.ready = true
	return errInit
}

func (n *node) tryHydrateInstanceFromCNINodeStatus() bool {
	if n.k8sAPI == nil {
		return false
	}

	cniNode, err := n.k8sAPI.GetCNINode(types.NamespacedName{Name: n.instance.Name()})
	if err != nil {
		n.log.V(1).Info("could not get CNINode status snapshot, falling back to EC2 instance details",
			"error", err)
		return false
	}

	hydrated, reason := n.instance.HydrateFromCNINodeStatus(cniNode.Status)
	if !hydrated {
		n.log.V(1).Info("CNINode status snapshot is not usable, falling back to EC2 instance details",
			"reason", reason)
		return false
	}

	n.log.Info("hydrated instance details from CNINode status snapshot")
	return true
}

// DeleteResources performs clean up of all the resource pools and provider of the nodes
func (n *node) DeleteResources(resourceManager resource.ResourceManager) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	// Mark the node as not ready to prevent processing for any further pod events
	n.ready = false

	var errDelete []error
	for _, resourceProvider := range resourceManager.GetResourceProviders() {
		if resourceProvider.IsInstanceSupported(n.instance) {
			err := resourceProvider.DeInitResource(n.instance)
			if err != nil {
				errDelete = append(errDelete, err)
				n.log.Error(err, "failed to de initialize provider")
			}
		}
	}

	if len(errDelete) > 0 {
		return fmt.Errorf("failed to intalize the resources %v", errDelete)
	}

	return nil
}

// UpdateInstanceCustomSubnet updates current required custom subnet
func (n *node) UpdateCustomNetworkingSpecs(subnetID string, securityGroup []string) {
	n.instance.SetNewCustomNetworkingSpec(subnetID, securityGroup)
}

// IsReady returns true if all the providers have been initialized
func (n *node) IsReady() bool {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return n.ready
}

// IsManaged returns true if the node is managed by the controller
func (n *node) IsManaged() bool {
	return n.managed
}

func (n *node) GetNodeInstanceID() string {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return n.instance.InstanceID()
}

func (n *node) HasInstance() bool {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return n.instance != nil
}

func (n *node) IsNitroInstance() bool {
	n.lock.RLock()
	defer n.lock.RUnlock()

	isNitroInstance, err := utils.IsNitroInstance(n.instance.Type())
	return err == nil && isNitroInstance
}

func (n *node) GetNextReconciliationTime() time.Time {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return n.nextReconciliationTime
}

func (n *node) SetNextReconciliationTime(time time.Time) {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.nextReconciliationTime = time
}

func (n *node) GetReconciliationInterval() time.Duration {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return n.reconciliationInterval
}

func (n *node) SetReconciliationInterval(time time.Duration) {
	n.lock.Lock()
	defer n.lock.Unlock()

	n.reconciliationInterval = time
}
