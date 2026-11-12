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

package manager

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/resource"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	asyncWorker "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/google/uuid"
	"github.com/samber/lo"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

type manager struct {
	// Log is the logger for node manager
	Log logr.Logger
	// lock to prevent multiple routines to write/update to data store concurrently
	lock sync.RWMutex
	// dataStore is the in memory data store of the current managed/un-managed node for each
	// Kubernetes Node name. A replaced Node is retained by its async job, not by this cache.
	dataStore map[string]node.Node
	// resourceManager provides the resource provider for all supported resources
	resourceManager resource.ResourceManager
	// wrapper around the clients for all APIs used by controller
	wrapper api.Wrapper
	// worker for performing async operation on node APIs
	worker            asyncWorker.Worker
	conditions        condition.Conditions
	controllerVersion string
	stopHealthCheckAt time.Time
	clusterName       string
	// removedNodes holds, per node name, a managed node removed from the data store, so a new instance that registers
	// under the name while the removed node's state may remain is counted as a node name reuse.
	removedNodes map[string]removedNode
	// reusedNodes holds, per node name, the new instance of a counted node name reuse that has not initialized yet.
	reusedNodes map[string]string
}

// removedNode is the instance ID of a managed node removed from the data store, and when it was removed.
type removedNode struct {
	instanceID string
	removedAt  time.Time
}

// Manager to perform operation on list of managed/un-managed node
type Manager interface {
	GetNode(nodeName string) (node node.Node, found bool)
	AddNode(nodeName string) error
	UpdateNode(nodeName string) error
	DeleteNode(nodeName string) error
	CheckNodeForLeakedENIs(nodeName string)
	SkipHealthCheck() bool
}

// AsyncOperation is operation on a node after the lock has been released.
// All AsyncOperation are done without lock as it involves API calls that
// will temporarily block the access to Manager in Pod Watcher. This is done
// to prevent Pod startup latency for already processed nodes.
type AsyncOperation string

const (
	Init   = AsyncOperation("Init")
	Update = AsyncOperation("Update")
	Delete = AsyncOperation("Delete")
)

// NodeUpdateStatus represents the status of the Node on Update operation.
type NodeUpdateStatus string

const (
	ManagedToUnManaged = NodeUpdateStatus("managedToUnManaged")
	UnManagedToManaged = NodeUpdateStatus("UnManagedToManaged")
	StillManaged       = NodeUpdateStatus("Managed")
	StillUnManaged     = NodeUpdateStatus("UnManaged")
)

type AsyncOperationJob struct {
	op       AsyncOperation
	node     node.Node
	nodeName string
	// replaced is the node of an earlier instance with the same name, de-initialized before node is initialized
	replaced node.Node
}

const pausingHealthCheckDuration = 10 * time.Minute

// removedNodeRetention is how long a removed node's state, such as its cached trunk, may outlive the node.
const removedNodeRetention = 10 * time.Minute

var (
	// nodeNameReuseDetected and nodeNameReuseInitialized compare node name reuse with its outcome. They should move
	// together; a gap is a new instance that reused the node name of an earlier one and never got its resources.
	nodeNameReuseDetected = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "node_name_reuse_detected_total",
		Help: "Number of new instances found registered under the node name of an earlier instance that used " +
			"security groups for pods, while state of the earlier instance remained",
	})
	nodeNameReuseInitialized = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "node_name_reuse_initialized_total",
		Help: "Number of the instances counted by node_name_reuse_detected_total that initialized their resources",
	})
	registerMetricsOnce sync.Once
)

// ErrReplacedCNINodeDeleting is returned while the CNINode of a replaced node with the same name is still being
// deleted. It is an expected wait after EC2 private-IP reuse, not a failure.
var ErrReplacedCNINodeDeleting = errors.New("waiting for the CNINode of a replaced node to be deleted")

// NewNodeManager returns a new node manager
func NewNodeManager(logger logr.Logger, resourceManager resource.ResourceManager,
	wrapper api.Wrapper, worker asyncWorker.Worker, conditions condition.Conditions, clusterName string, controllerVersion string, healthzHandler *rcHealthz.HealthzHandler) (Manager, error) {
	registerMetricsOnce.Do(func() {
		metrics.Registry.MustRegister(nodeNameReuseDetected, nodeNameReuseInitialized)
	})

	manager := &manager{
		resourceManager:   resourceManager,
		Log:               logger,
		dataStore:         make(map[string]node.Node),
		wrapper:           wrapper,
		worker:            worker,
		conditions:        conditions,
		controllerVersion: controllerVersion,
		clusterName:       clusterName,
	}

	// add health check on subpath for node manager
	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{"health-node-manager": manager.check()},
	)

	return manager, worker.StartWorkerPool(manager.performAsyncOperation)
}

func (m *manager) CheckNodeForLeakedENIs(nodeName string) {
	cachedNode, found := m.GetNode(nodeName)
	if !found || !cachedNode.IsManaged() {
		m.Log.V(1).Info("node not found or not managed by controller, skip reconciliation", "nodeName", nodeName)
		return
	}

	// Only start a goroutine when need to
	if time.Now().After(cachedNode.GetNextReconciliationTime()) {
		go func() {
			if resourceProvider, found := m.resourceManager.GetResourceProvider(config.ResourceNamePodENI); found {
				foundLeakedENI := resourceProvider.ReconcileNode(nodeName)
				if foundLeakedENI {
					cachedNode.SetReconciliationInterval(node.NodeInitialCleanupInterval)
				} else {
					interval := wait.Jitter(cachedNode.GetReconciliationInterval(), 5)
					if interval > node.MaxNodeReconciliationInterval {
						interval = node.MaxNodeReconciliationInterval
					}
					cachedNode.SetReconciliationInterval(interval)
				}
				cachedNode.SetNextReconciliationTime(time.Now().Add(cachedNode.GetReconciliationInterval()))
				m.Log.Info("reconciled node to cleanup leaked branch ENIs", "NodeName", nodeName, "NextInterval", cachedNode.GetReconciliationInterval(), "NextReconciliationTime", cachedNode.GetNextReconciliationTime())
			} else {
				// no SGP provider enabled
				return
			}
		}()
	}
}

// GetNode returns the node from in memory data store
func (m *manager) GetNode(nodeName string) (node node.Node, found bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	node, found = m.dataStore[nodeName]
	return node, found
}

// AddNode adds the managed and un-managed nodes to the in memory data store, the
// user of node can verify if the node is managed before performing any operations
func (m *manager) AddNode(nodeName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	k8sNode, err := m.wrapper.K8sAPI.GetNode(nodeName)
	if err != nil {
		return fmt.Errorf("failed to add node %s, doesn't exist in cache anymore", nodeName)
	}

	cachedNode, nodeFound := m.dataStore[k8sNode.Name]
	if nodeFound {
		if cachedNode.GetNodeInstanceID() == utils.GetNodeInstanceID(k8sNode) {
			m.Log.Info("node is already processed, not processing add event again", "node name", k8sNode.Name)
			return nil
		}
		return m.addNode(k8sNode, cachedNode)
	}
	return m.addNode(k8sNode, nil)
}

// addNode adds the node with the manager lock held. A previous Node instance with
// the same name is de-initialized before the new managed Node is initialized.
func (m *manager) addNode(k8sNode *v1.Node, replaced node.Node) error {
	nodeName := k8sNode.Name
	log := m.Log.WithValues("node name", nodeName, "request", "add")
	replacedManaged := replaced != nil && replaced.IsManaged()
	if replaced != nil {
		log.Info("node name was reused by a new instance, replacing the cached node",
			"replaced instance", replaced.GetNodeInstanceID(), "instance", utils.GetNodeInstanceID(k8sNode))
	}
	instanceID := utils.GetNodeInstanceID(k8sNode)
	if replacedManaged && replaced.GetNodeInstanceID() != "" && instanceID != "" &&
		replaced.GetNodeInstanceID() != instanceID {
		m.countNodeNameReuse(nodeName, instanceID, "cached node")
	} else if removed, ok := m.removedNodes[nodeName]; ok {
		if removed.instanceID != instanceID && time.Since(removed.removedAt) < removedNodeRetention {
			m.countNodeNameReuse(nodeName, instanceID, "removed node")
		}
		delete(m.removedNodes, nodeName)
	}

	var newNode node.Node

	if err := m.CreateCNINodeIfNotExisting(k8sNode); err != nil {
		if errors.Is(err, ErrReplacedCNINodeDeleting) {
			log.Info("waiting for the CNINode of a replaced node to be deleted")
		} else {
			m.Log.Error(err, "Failed to create CNINode for k8sNode", "NodeName", k8sNode.Name)
		}
		return err
	}

	shouldManage, err := m.isSelectedForManagement(k8sNode)
	if err != nil {
		return err
	}

	var op AsyncOperation

	if shouldManage {
		newNode = node.NewManagedNode(m.Log, k8sNode.Name, utils.GetNodeInstanceID(k8sNode),
			utils.GetNodeOS(k8sNode), m.wrapper.K8sAPI, m.wrapper.EC2API)
		err := m.updateSubnetIfUsingENIConfig(newNode, k8sNode)
		if err != nil {
			return err
		}
		m.dataStore[nodeName] = newNode
		log.Info("node added as a managed node")
		op = Init
	} else {
		newNode = node.NewUnManagedNode(m.Log, k8sNode.Name, utils.GetNodeInstanceID(k8sNode),
			utils.GetNodeOS(k8sNode))
		m.dataStore[nodeName] = newNode
		log.V(1).Info("node added as an un-managed node")
		if replacedManaged {
			m.worker.SubmitJob(AsyncOperationJob{op: Delete, node: replaced, nodeName: nodeName})
		}
		return nil
	}

	if replacedManaged {
		m.worker.SubmitJob(AsyncOperationJob{
			op:       op,
			node:     newNode,
			nodeName: nodeName,
			replaced: replaced,
		})
	}
	if !replacedManaged {
		m.worker.SubmitJob(AsyncOperationJob{op: op, node: newNode, nodeName: nodeName})
	}
	return nil
}

func (m *manager) CreateCNINodeIfNotExisting(node *v1.Node) error {
	cniNode, err := m.wrapper.K8sAPI.GetCNINode(types.NamespacedName{Name: node.Name})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CNINodeReconciler owns creation and watches Node objects. Waiting
			// here prevents the two reconcilers racing to create a replacement
			// with different feature sets.
			return fmt.Errorf("%w: %s", ErrReplacedCNINodeDeleting, node.Name)
		}
		return err
	}
	if !utils.OwnedByReplacedNode(cniNode.OwnerReferences, node) && cniNode.DeletionTimestamp.IsZero() {
		m.Log.Info("The CNINode is already existing", "cninode", cniNode.Name, "features", cniNode.Spec.Features)
		return nil
	}
	// The CNINode belongs to an earlier instance with the same node name, see EC2 private-IP reuse.
	if state := cniNode.Status.NodeNetworkState; state != nil && state.InstanceID != utils.GetNodeInstanceID(node) &&
		lo.ContainsBy(cniNode.Spec.Features, func(f v1alpha1.Feature) bool { return f.Name == v1alpha1.SecurityGroupsForPods }) {
		m.countNodeNameReuse(node.Name, utils.GetNodeInstanceID(node), "CNINode")
	}
	// CNINodeReconciler owns replacement. It either transfers ownership or
	// recreates a deleting object with its features; the node manager waits
	// for that stable result.
	return fmt.Errorf("%w: %s", ErrReplacedCNINodeDeleting, cniNode.Name)
}

// UpdateNode updates the node object and, if the node is previously un-managed and now
// is selected for management, node resources are initialized, if the node is managed
// and now is not required to be managed, it's resources are de-initialized. Finally,
// if there is no toggling, the resources are updated
func (m *manager) UpdateNode(nodeName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	k8sNode, err := m.wrapper.K8sAPI.GetNode(nodeName)
	if err != nil {
		return fmt.Errorf("failed to update node %s, doesn't exist in cache anymore", nodeName)
	}

	log := m.Log.WithValues("node name", nodeName, "request", "update")

	cachedNode, found := m.dataStore[nodeName]
	if !found {
		return m.addNode(k8sNode, nil)
	}
	if cachedNode.GetNodeInstanceID() != utils.GetNodeInstanceID(k8sNode) {
		return m.addNode(k8sNode, cachedNode)
	}

	var op AsyncOperation
	status, err := m.GetNodeUpdateStatus(k8sNode, cachedNode)

	if err != nil {
		return err
	}

	switch status {
	case UnManagedToManaged:
		log.Info("node was previously un-managed, will be added as managed node now")
		cachedNode = node.NewManagedNode(m.Log, k8sNode.Name,
			utils.GetNodeInstanceID(k8sNode), utils.GetNodeOS(k8sNode), m.wrapper.K8sAPI, m.wrapper.EC2API)
		// Update the Subnet if the node has custom networking configured
		err = m.updateSubnetIfUsingENIConfig(cachedNode, k8sNode)
		if err != nil {
			return err
		}
		m.dataStore[nodeName] = cachedNode
		op = Init
	case ManagedToUnManaged:
		log.Info("node was being managed earlier, will be added as un-managed node now")
		// Change the node in cache, but for de initializing all resource providers
		// pass the async job the older cached value instead
		m.dataStore[nodeName] = node.NewUnManagedNode(m.Log, k8sNode.Name,
			utils.GetNodeInstanceID(k8sNode), utils.GetNodeOS(k8sNode))
		op = Delete
	case StillManaged:
		// We only need to update the Subnet for Managed Node. This subnet is required for creating
		// Branch ENIs when user is using Custom Networking. In future, we should move this to
		// UpdateResources for Trunk ENI Provider as this is resource specific
		err = m.updateSubnetIfUsingENIConfig(cachedNode, k8sNode)
		if err != nil {
			return err
		}
		op = Update
	case StillUnManaged:
		log.V(1).Info("node not managed, no operation required")
		// No async operation required for un-managed nodes
		return nil
	}

	m.worker.SubmitJob(AsyncOperationJob{
		op:       op,
		node:     cachedNode,
		nodeName: nodeName,
	})
	return nil
}

func (m *manager) GetNodeUpdateStatus(k8sNode *v1.Node, cachedNode node.Node) (NodeUpdateStatus, error) {
	isSelectedForManagement, err := m.isSelectedForManagement(k8sNode)

	if err != nil {
		return "", err
	}

	if isSelectedForManagement && !cachedNode.IsManaged() {
		return UnManagedToManaged, err
	} else if !isSelectedForManagement && cachedNode.IsManaged() {
		return ManagedToUnManaged, err
	} else if isSelectedForManagement {
		return StillManaged, err
	} else {
		return StillUnManaged, err
	}
}

// DeleteNode deletes the nodes from the cache and cleans up the resources used by all the resource providers
func (m *manager) DeleteNode(nodeName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	log := m.Log.WithValues("node name", nodeName, "request", "delete")

	cachedNode, found := m.dataStore[nodeName]
	if !found {
		log.Info("node not found in the data store, ignoring the event")
		return nil
	}
	delete(m.dataStore, nodeName)

	delete(m.reusedNodes, nodeName)

	if cachedNode.IsManaged() {
		m.recordRemovedNode(nodeName, cachedNode.GetNodeInstanceID())
		m.worker.SubmitJob(AsyncOperationJob{
			op:       Delete,
			node:     cachedNode,
			nodeName: nodeName,
		})
	}

	log.Info("node removed from data store")

	return nil
}

// updateSubnetIfUsingENIConfig updates the subnet id for the node to the subnet specified in ENIConfig if the node is
// using custom networking
func (m *manager) updateSubnetIfUsingENIConfig(cachedNode node.Node, k8sNode *v1.Node) error {
	eniConfigName, isPresent := k8sNode.Labels[config.CustomNetworkingLabel]
	var cniNodeEnabled bool
	var err error
	if !isPresent {
		if cniNodeEnabled, err = m.customNetworkEnabledInCNINode(k8sNode); err != nil {
			return err
		}
	}

	if isPresent || cniNodeEnabled {
		if !isPresent {
			var err error
			eniConfigName, err = m.GetEniConfigName(k8sNode)
			// if we couldn't find the name from CNINode, this should be a misconfiguration
			// as long as the feature is registered in CNINode, we couldn't easily use "" as eniconfig name
			// when not able to find the name from CNINode.
			if err != nil {
				if errors.Is(err, utils.ErrNotFound) {
					utils.SendNodeEventWithNodeObject(
						m.wrapper.K8sAPI, k8sNode, utils.EniConfigNameNotFoundReason, err.Error(), v1.EventTypeWarning, m.Log)
				}
				return err
			}
		}
		eniConfig, err := m.wrapper.K8sAPI.GetENIConfig(eniConfigName)
		if err != nil {
			return fmt.Errorf("failed to find the ENIConfig %s: %v", eniConfigName, err)
		}
		if eniConfig.Spec.Subnet != "" {
			m.Log.V(1).Info("node is using custom networking, updating the subnet", "node", k8sNode.Name,
				"subnet", eniConfig.Spec.Subnet)
			cachedNode.UpdateCustomNetworkingSpecs(eniConfig.Spec.Subnet, eniConfig.Spec.SecurityGroups)
			return nil
		}
	} else {
		cachedNode.UpdateCustomNetworkingSpecs("", nil)
	}
	return nil
}

func (m *manager) GetEniConfigName(node *v1.Node) (string, error) {
	cniNode, err := m.wrapper.K8sAPI.GetCNINode(types.NamespacedName{Name: node.Name})
	if err != nil {
		return "", err
	}
	for _, feature := range cniNode.Spec.Features {
		if feature.Name == v1alpha1.CustomNetworking && feature.Value != "" {
			return feature.Value, nil
		}
	}

	return "", fmt.Errorf("couldn't find custom networking eniconfig name for node %s, error: %w", node.Name, utils.ErrNotFound)
}

// performAsyncOperation performs the operation on a node without taking the node manager lock
func (m *manager) performAsyncOperation(job interface{}) (ctrl.Result, error) {
	asyncJob, ok := job.(AsyncOperationJob)
	if !ok {
		m.Log.Error(fmt.Errorf("wrong job type submitted"), "not re-queuing")
		return ctrl.Result{}, nil
	}

	log := m.Log.WithValues("node", asyncJob.nodeName, "operation", asyncJob.op)

	var err error
	switch asyncJob.op {
	case Init:
		if asyncJob.replaced != nil {
			if err := asyncJob.replaced.DeleteResources(m.resourceManager); err != nil {
				log.Error(err, "failed to de-initialize the replaced node", "replaced instance", asyncJob.replaced.GetNodeInstanceID())
			}
		}
		utils.SendNodeEventWithNodeName(m.wrapper.K8sAPI, asyncJob.nodeName, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", m.controllerVersion), v1.EventTypeNormal, m.Log)
		err = asyncJob.node.InitResources(m.resourceManager)
		if err != nil {
			if pauseHealthCheckOnError(err) && !m.SkipHealthCheck() {
				m.setStopHealthCheck()
				log.Info("node manager sets a pause on health check due to observing a EC2 error", "error", err.Error())
			}
			log.Error(err, "removing the node from cache as it failed to initialize")
			m.removeNodeSafe(asyncJob.nodeName, asyncJob.node)
			// if initializing node failed, we want to make this visible although the manager will retry
			// the trunk label will stay as false until retry succeed

			// Node will be retried for init on next event
			return ctrl.Result{}, nil
		}

		m.countNodeNameReuseInitialized(asyncJob.nodeName, asyncJob.node)
		// If there's no error, we need to update the node so the capacity is advertised
		asyncJob.op = Update
		return m.performAsyncOperation(asyncJob)
	case Update:
		err = asyncJob.node.UpdateResources(m.resourceManager)
	case Delete:
		err = asyncJob.node.DeleteResources(m.resourceManager)
	default:
		m.Log.V(1).Info("no operation operation requested",
			"node", asyncJob.nodeName)
		return ctrl.Result{}, nil
	}

	if err == nil {
		log.V(1).Info("successfully performed node operation")
		return ctrl.Result{}, nil
	}
	log.Error(err, "failed to perform node operation")

	return ctrl.Result{}, nil
}

// isSelectedForManagement returns true if the node should be managed by the controller
func (m *manager) isSelectedForManagement(v1node *v1.Node) (bool, error) {
	os := utils.GetNodeOS(v1node)
	instanceID := utils.GetNodeInstanceID(v1node)

	if os == "" || instanceID == "" {
		m.Log.V(1).Info("node doesn't have os/instance id", v1node.Name,
			"os", os, "instance ID", instanceID)
		return false, nil
	}

	if isWindowsNode(v1node) && m.conditions.IsWindowsIPAMEnabled() {
		return true, nil
	} else {
		return m.canAttachTrunk(v1node)
	}
}

// isWindowsNode returns true if the "kubernetes.io/os" or "beta.kubernetes.io/os" is set to windows
func isWindowsNode(node *v1.Node) bool {
	labels := node.GetLabels()

	nodeOS, ok := labels[config.NodeLabelOS]
	if !ok {
		nodeOS, ok = labels[config.NodeLabelOSBeta]
		if !ok {
			return false
		}
	}

	return nodeOS == config.OSWindows
}

// canAttachTrunk returns true if the node has capability to attach a Trunk ENI
func (m *manager) canAttachTrunk(node *v1.Node) (bool, error) {
	if _, ok := node.Labels[config.HasTrunkAttachedLabel]; ok == true {
		return true, nil
	}
	enabled, err := m.trunkEnabledInCNINode(node)
	return enabled, err
}

func (m *manager) trunkEnabledInCNINode(node *v1.Node) (bool, error) {
	var err error
	if cniNode, err := m.wrapper.K8sAPI.GetCNINode(
		types.NamespacedName{Name: node.Name},
	); err == nil {
		if lo.ContainsBy(cniNode.Spec.Features, func(addedFeature v1alpha1.Feature) bool {
			return addedFeature.Name == v1alpha1.SecurityGroupsForPods
		}) {
			return true, nil
		}
	}
	return false, err
}

func (m *manager) customNetworkEnabledInCNINode(node *v1.Node) (bool, error) {
	var err error
	if cniNode, err := m.wrapper.K8sAPI.GetCNINode(
		types.NamespacedName{Name: node.Name},
	); err == nil {
		if lo.ContainsBy(cniNode.Spec.Features, func(addedFeature v1alpha1.Feature) bool {
			return addedFeature.Name == v1alpha1.CustomNetworking
		}) {
			return true, err
		}
	}
	return false, err
}

// countNodeNameReuse counts, once per new instance, a node name reuse found from state an earlier instance left. It
// is called with the manager lock held.
func (m *manager) countNodeNameReuse(nodeName, instanceID, foundIn string) {
	if instanceID == "" || m.reusedNodes[nodeName] == instanceID {
		return
	}
	if m.reusedNodes == nil {
		m.reusedNodes = make(map[string]string)
	}
	m.reusedNodes[nodeName] = instanceID
	nodeNameReuseDetected.Inc()
	m.Log.Info("detected node name reuse by a new instance", "node name", nodeName, "instance", instanceID,
		"found in", foundIn)
}

// countNodeNameReuseInitialized counts the initialization of a new instance whose node name reuse was counted.
func (m *manager) countNodeNameReuseInitialized(nodeName string, initialized node.Node) {
	m.lock.Lock()
	defer m.lock.Unlock()

	reusedBy, found := m.reusedNodes[nodeName]
	if !found || initialized.GetNodeInstanceID() != reusedBy {
		return
	}
	delete(m.reusedNodes, nodeName)
	nodeNameReuseInitialized.Inc()
}

// recordRemovedNode records a managed node removed from the data store, and drops records older than the retention.
func (m *manager) recordRemovedNode(nodeName, instanceID string) {
	if m.removedNodes == nil {
		m.removedNodes = make(map[string]removedNode)
	}
	for name, removed := range m.removedNodes {
		if time.Since(removed.removedAt) >= removedNodeRetention {
			delete(m.removedNodes, name)
		}
	}
	m.removedNodes[nodeName] = removedNode{instanceID: instanceID, removedAt: time.Now()}
}

// removeNodeSafe removes the given node only if it is still the current Node
// for the name. A replacement must remain cached if an older Init fails.
func (m *manager) removeNodeSafe(nodeName string, cachedNode node.Node) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.dataStore[nodeName] == cachedNode {
		delete(m.dataStore, nodeName)
	}
}

func (m *manager) check() healthz.Checker {
	// instead of using SimplePing, testing the node cache from manager makes the test more accurate
	return func(req *http.Request) error {
		err := rcHealthz.PingWithTimeout(func(c chan<- error) {
			randomName := uuid.New().String()
			m.Log.V(1).Info("starting health check call to acquire read lock of node manager through get node function")
			_, found := m.GetNode(randomName)
			m.Log.V(1).Info("health check tested ping GetNode to check on datastore cache in node manager successfully", "TesedNodeName", randomName, "NodeFound", found)
			if m.SkipHealthCheck() {
				m.Log.Info("due to EC2 error, node manager skips node worker queue health check for now")
			} else {
				var ping interface{}
				m.Log.V(1).Info("starting health check to acquire lock on work queue of node manager")
				m.worker.SubmitJob(ping)
				m.Log.V(1).Info("health check tested ping SubmitJob with a nil job to check on worker queue in node manager successfully")
			}
			c <- nil
		}, m.Log)

		return err
	}
}

func (m *manager) SkipHealthCheck() bool {
	m.lock.RLock()
	defer m.lock.RUnlock()

	return time.Since(m.stopHealthCheckAt) < pausingHealthCheckDuration
}

func (m *manager) setStopHealthCheck() {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.stopHealthCheckAt = time.Now()
}

func pauseHealthCheckOnError(err error) bool {
	return lo.ContainsBy(utils.PauseHealthCheckErrors, func(e string) bool {
		return strings.Contains(err.Error(), e)
	})
}
