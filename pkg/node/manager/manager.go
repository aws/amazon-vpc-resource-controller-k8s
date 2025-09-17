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
	v1 "k8s.io/api/core/v1"
)

type manager struct {
	// Log is the logger for node manager
	Log logr.Logger
	// lock to prevent multiple routines to write/update to data store concurrently
	lock sync.RWMutex
	// dataStore is the in memory data store of all the managed/un-managed nodes in the cluster
	dataStore map[string]node.Node
	//FQDN to instanceId mapping
	fqdnToInstanceID map[string]string
	// InstanceId to FQDN mapping
	instanceIDToFQDN map[string]string
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
	op             AsyncOperation
	node           node.Node
	nodeName       string
	nodeInstanceID string
}

const pausingHealthCheckDuration = 10 * time.Minute

// NewNodeManager returns a new node manager
func NewNodeManager(logger logr.Logger, resourceManager resource.ResourceManager,
	wrapper api.Wrapper, worker asyncWorker.Worker, conditions condition.Conditions, clusterName string, controllerVersion string, healthzHandler *rcHealthz.HealthzHandler) (Manager, error) {

	manager := &manager{
		resourceManager:   resourceManager,
		Log:               logger,
		dataStore:         make(map[string]node.Node),
		wrapper:           wrapper,
		worker:            worker,
		conditions:        conditions,
		controllerVersion: controllerVersion,
		clusterName:       clusterName,
		fqdnToInstanceID:  make(map[string]string),
		instanceIDToFQDN:  make(map[string]string),
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
	id := m.fqdnToInstanceID[nodeName]
	node, found = m.dataStore[id]
	return
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

	log := m.Log.WithValues("node name", k8sNode.Name, "request", "add")

	var newNode node.Node

	instanceID := GetNodeInstanceID(k8sNode)
	if instanceID == "" {
		return errors.New("instanceID is empty")
	}
	if oldId, ok := m.fqdnToInstanceID[nodeName]; ok && oldId != "" && oldId != instanceID {
		if oldNode, exists := m.dataStore[oldId]; exists {
			if oldNode.IsManaged() {
				m.worker.SubmitJob(AsyncOperationJob{
					op:             Delete,
					node:           oldNode,
					nodeName:       nodeName,
					nodeInstanceID: oldId,
				})

			}
			delete(m.dataStore, oldId)
			delete(m.instanceIDToFQDN, oldId)
		}
		delete(m.fqdnToInstanceID, nodeName)
	}

	m.fqdnToInstanceID[nodeName] = instanceID
	m.instanceIDToFQDN[instanceID] = nodeName

	if _, exists := m.dataStore[instanceID]; exists {
		log.Info("instance already processed, skipping add", "instanceID", instanceID)
		return nil
	}

	if err = m.CreateCNINodeIfNotExisting(k8sNode); err != nil {
		m.Log.Error(err, "Failed to create CNINode for k8sNode", "NodeName", k8sNode.Name)
		return err
	}

	shouldManage, err := m.isSelectedForManagement(k8sNode)
	if err != nil {
		return err
	}

	var op AsyncOperation

	if shouldManage {
		newNode = node.NewManagedNode(m.Log, k8sNode.Name, GetNodeInstanceID(k8sNode),
			GetNodeOS(k8sNode), m.wrapper.K8sAPI, m.wrapper.EC2API)
		err := m.updateSubnetIfUsingENIConfig(newNode, k8sNode)
		if err != nil {
			return err
		}
		m.dataStore[instanceID] = newNode
		log.Info("node added as a managed node")
		op = Init
	} else {
		newNode = node.NewUnManagedNode(m.Log, k8sNode.Name, GetNodeInstanceID(k8sNode),
			GetNodeOS(k8sNode))
		m.dataStore[instanceID] = newNode
		log.V(1).Info("node added as an un-managed node")
		return nil
	}

	m.worker.SubmitJob(AsyncOperationJob{
		op:             op,
		node:           newNode,
		nodeName:       nodeName,
		nodeInstanceID: instanceID,
	})
	return nil
}

func (m *manager) CreateCNINodeIfNotExisting(node *v1.Node) error {
	if cniNode, err := m.wrapper.K8sAPI.GetCNINode(
		types.NamespacedName{Name: node.Name},
	); err != nil {
		if apierrors.IsNotFound(err) {
			m.Log.Info("Will create a new CNINode", "CNINodeName", node.Name)
			return m.wrapper.K8sAPI.CreateCNINode(node, m.clusterName)
		}
		return err
	} else {
		m.Log.Info("The CNINode is already existing", "cninode", cniNode.Name, "features", cniNode.Spec.Features)
		return nil
	}
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

	instanceID := GetNodeInstanceID(k8sNode)
	if instanceID == "" {
		return errors.New("instanceID is empty")
	}
	if oldId, ok := m.fqdnToInstanceID[nodeName]; ok && oldId != "" && oldId != instanceID {
		if oldNode, exists := m.dataStore[oldId]; exists {
			if oldNode.IsManaged() {
				m.worker.SubmitJob(AsyncOperationJob{
					op:             Delete,
					node:           oldNode,
					nodeName:       nodeName,
					nodeInstanceID: oldId,
				})

			}
			delete(m.dataStore, oldId)
			delete(m.instanceIDToFQDN, oldId)
		}
		delete(m.fqdnToInstanceID, nodeName)
	}
	log := m.Log.WithValues("node name", nodeName, "request", "update")

	cachedNode, found := m.dataStore[instanceID]
	if !found {
		m.Log.Info("the node doesn't exist in cache anymore, it might have been deleted")
		return nil
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
			GetNodeInstanceID(k8sNode), GetNodeOS(k8sNode), m.wrapper.K8sAPI, m.wrapper.EC2API)
		// Update the Subnet if the node has custom networking configured
		err = m.updateSubnetIfUsingENIConfig(cachedNode, k8sNode)
		if err != nil {
			return err
		}
		m.dataStore[instanceID] = cachedNode
		op = Init
	case ManagedToUnManaged:
		log.Info("node was being managed earlier, will be added as un-managed node now")
		// Change the node in cache, but for de initializing all resource providers
		// pass the async job the older cached value instead
		m.dataStore[instanceID] = node.NewUnManagedNode(m.Log, k8sNode.Name,
			GetNodeInstanceID(k8sNode), GetNodeOS(k8sNode))
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
		op:             op,
		node:           cachedNode,
		nodeName:       nodeName,
		nodeInstanceID: instanceID,
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

	instanceID, _ := m.fqdnToInstanceID[nodeName]
	if instanceID == "" {
		log.Info("node not found in the data store, skipping delete", "instanceID", instanceID)
		return nil
	}

	cachedNode, nodeFound := m.dataStore[instanceID]
	delete(m.fqdnToInstanceID, nodeName)

	if !nodeFound {
		log.Info("node not found in the data store, ignoring the event")
		return nil
	}

	delete(m.dataStore, instanceID)
	delete(m.instanceIDToFQDN, instanceID)

	if !cachedNode.IsManaged() {
		log.V(1).Info("un managed node removed from data store")
		return nil
	}

	m.worker.SubmitJob(AsyncOperationJob{
		op:             Delete,
		node:           cachedNode,
		nodeName:       nodeName,
		nodeInstanceID: instanceID,
	})

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

	log := m.Log.WithValues("node", asyncJob.nodeName, "operation", asyncJob.op, "instanceID", asyncJob.nodeInstanceID)

	var err error
	switch asyncJob.op {
	case Init:
		utils.SendNodeEventWithNodeName(m.wrapper.K8sAPI, asyncJob.nodeName, utils.VersionNotice, fmt.Sprintf("The node is managed by VPC resource controller version %s", m.controllerVersion), v1.EventTypeNormal, m.Log)
		err = asyncJob.node.InitResources(m.resourceManager)
		if err != nil {
			if pauseHealthCheckOnError(err) && !m.SkipHealthCheck() {
				m.setStopHealthCheck()
				log.Info("node manager sets a pause on health check due to observing a EC2 error", "error", err.Error())
			}
			log.Error(err, "removing the node from cache as it failed to initialize")
			m.removeNodeSafe(asyncJob.nodeInstanceID)
			// if initializing node failed, we want to make this visible although the manager will retry
			// the trunk label will stay as false until retry succeed

			// Node will be retried for init on next event
			return ctrl.Result{}, nil
		}

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
	os := GetNodeOS(v1node)
	instanceID := GetNodeInstanceID(v1node)

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

// GetNodeInstanceID returns the EC2 instance ID of a node
func GetNodeInstanceID(node *v1.Node) string {
	var instanceID string

	if node.Spec.ProviderID != "" {
		// ProviderID is preferred when available.
		// aws:///us-west-2c/i-01234567890abcdef
		id := strings.Split(node.Spec.ProviderID, "/")
		instanceID = id[len(id)-1]
	}

	return instanceID
}

// getNodeOS returns the operating system of a node.
func GetNodeOS(node *v1.Node) string {
	labels := node.GetLabels()
	os := labels[config.NodeLabelOS]
	if os == "" {
		// For older k8s version.
		os = labels[config.NodeLabelOSBeta]
	}
	return os
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

func (m *manager) removeNodeSafe(nodeID string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	delete(m.dataStore, nodeID)
	if fqdn, ok := m.instanceIDToFQDN[nodeID]; ok {
		delete(m.fqdnToInstanceID, fqdn)
		delete(m.instanceIDToFQDN, nodeID)
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
