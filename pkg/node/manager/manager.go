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
	"fmt"
	"strings"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/resource"
	asyncWorker "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	ctrl "sigs.k8s.io/controller-runtime"

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
	// resourceManager provides the resource provider for all supported resources
	resourceManager resource.ResourceManager
	// wrapper around the clients for all APIs used by controller
	wrapper api.Wrapper
	// worker for performing async operation on node APIs
	worker     asyncWorker.Worker
	conditions condition.Conditions
}

// Manager to perform operation on list of managed/un-managed node
type Manager interface {
	GetNode(nodeName string) (node node.Node, found bool)
	AddNode(nodeName string) error
	UpdateNode(nodeName string) error
	DeleteNode(nodeName string) error
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
}

// NewNodeManager returns a new node manager
func NewNodeManager(logger logr.Logger, resourceManager resource.ResourceManager,
	wrapper api.Wrapper, worker asyncWorker.Worker, conditions condition.Conditions) (Manager, error) {

	manager := &manager{
		resourceManager: resourceManager,
		Log:             logger,
		dataStore:       make(map[string]node.Node),
		wrapper:         wrapper,
		worker:          worker,
		conditions:      conditions,
	}

	return manager, worker.StartWorkerPool(manager.performAsyncOperation)
}

// GetNode returns the node from in memory data store
func (m *manager) GetNode(nodeName string) (node node.Node, found bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	node, found = m.dataStore[nodeName]
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
	var nodeFound bool

	newNode, nodeFound = m.dataStore[k8sNode.Name]
	if nodeFound {
		log.Info("node is already processed, not processing add event again")
		return nil
	}

	shouldManage := m.isSelectedForManagement(k8sNode)
	var op AsyncOperation

	if shouldManage {
		newNode = node.NewManagedNode(m.Log, k8sNode.Name, GetNodeInstanceID(k8sNode),
			GetNodeOS(k8sNode))
		err := m.updateSubnetIfUsingENIConfig(newNode, k8sNode)
		if err != nil {
			return err
		}
		m.dataStore[k8sNode.Name] = newNode
		log.Info("node added as a managed node")
		op = Init
	} else {
		newNode = node.NewUnManagedNode(m.Log, k8sNode.Name, GetNodeInstanceID(k8sNode),
			GetNodeOS(k8sNode))
		m.dataStore[k8sNode.Name] = newNode
		log.V(1).Info("node added as an un-managed node")
		return nil
	}

	m.worker.SubmitJob(AsyncOperationJob{
		op:       op,
		node:     newNode,
		nodeName: nodeName,
	})
	return nil
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
		m.Log.Info("the node doesn't exist in cache anymore, it might have been deleted")
		return nil
	}

	var op AsyncOperation
	status := m.GetNodeUpdateStatus(k8sNode, cachedNode)

	switch status {
	case UnManagedToManaged:
		log.Info("node was previously un-managed, will be added as managed node now")
		cachedNode = node.NewManagedNode(m.Log, k8sNode.Name,
			GetNodeInstanceID(k8sNode), GetNodeOS(k8sNode))
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
		op:       op,
		node:     cachedNode,
		nodeName: nodeName,
	})
	return nil
}

func (m *manager) GetNodeUpdateStatus(k8sNode *v1.Node, cachedNode node.Node) NodeUpdateStatus {
	isSelectedForManagement := m.isSelectedForManagement(k8sNode)

	if isSelectedForManagement && !cachedNode.IsManaged() {
		return UnManagedToManaged
	} else if !isSelectedForManagement && cachedNode.IsManaged() {
		return ManagedToUnManaged
	} else if isSelectedForManagement {
		return StillManaged
	} else {
		return StillUnManaged
	}
}

// DeleteNode deletes the nodes from the cache and cleans up the resources used by all the resource providers
func (m *manager) DeleteNode(nodeName string) error {
	m.lock.Lock()
	defer m.lock.Unlock()

	log := m.Log.WithValues("node name", nodeName, "request", "delete")

	cachedNode, nodeFound := m.dataStore[nodeName]
	if !nodeFound {
		log.Info("node not found in the data store, ignoring the event")
		return nil
	}

	delete(m.dataStore, nodeName)

	if !cachedNode.IsManaged() {
		log.V(1).Info("un managed node removed from data store")
		return nil
	}

	m.worker.SubmitJob(AsyncOperationJob{
		op:       Delete,
		node:     cachedNode,
		nodeName: nodeName,
	})

	log.Info("node removed from data store")

	return nil
}

// updateSubnetIfUsingENIConfig updates the subnet id for the node to the subnet specified in ENIConfig if the node is
// using custom networking
func (m *manager) updateSubnetIfUsingENIConfig(cachedNode node.Node, k8sNode *v1.Node) error {
	eniConfigName, isPresent := k8sNode.Labels[config.CustomNetworkingLabel]
	if isPresent {
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
		return fmt.Errorf("failed to find subnet in eniconfig spec %s", eniConfigName)
	} else {
		cachedNode.UpdateCustomNetworkingSpecs("", nil)
	}
	return nil
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
		err = asyncJob.node.InitResources(m.resourceManager, m.wrapper.EC2API)
		if err != nil {
			log.Error(err, "removing the node from cache as it failed to initialize")
			m.removeNodeSafe(asyncJob.nodeName)
			// if initializing node failed, we want to make this visible although the manager will retry
			// the trunk label will stay as false until retry succeed

			// Node will be retried for init on next event
			return ctrl.Result{}, nil
		}

		// If there's no error, we need to update the node so the capacity is advertised
		asyncJob.op = Update
		return m.performAsyncOperation(asyncJob)
	case Update:
		err = asyncJob.node.UpdateResources(m.resourceManager, m.wrapper.EC2API)
	case Delete:
		err = asyncJob.node.DeleteResources(m.resourceManager, m.wrapper.EC2API)
	default:
		m.Log.V(1).Info("no operation operation requested",
			"node", asyncJob.nodeName)
		return ctrl.Result{}, nil
	}

	if err == nil {
		log.V(1).Info("successfully performed node operation")
		return ctrl.Result{}, nil
	}
	log.Error(err, "failed to performed node operation")

	return ctrl.Result{}, nil
}

// isSelectedForManagement returns true if the node should be managed by the controller
func (m *manager) isSelectedForManagement(v1node *v1.Node) bool {
	os := GetNodeOS(v1node)
	instanceID := GetNodeInstanceID(v1node)

	if os == "" || instanceID == "" {
		m.Log.V(1).Info("node doesn't have os/instance id", v1node.Name,
			"os", os, "instance ID", instanceID)
		return false
	}

	return (isWindowsNode(v1node) && m.conditions.IsWindowsIPAMEnabled()) || canAttachTrunk(v1node)
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
func canAttachTrunk(node *v1.Node) bool {
	_, ok := node.Labels[config.HasTrunkAttachedLabel]
	return ok
}

func (m *manager) removeNodeSafe(nodeName string) {
	m.lock.Lock()
	defer m.lock.Unlock()

	delete(m.dataStore, nodeName)
}
