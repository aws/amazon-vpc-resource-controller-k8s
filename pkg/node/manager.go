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

package node

import (
	"fmt"
	"strings"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
)

type manager struct {
	// Log is the logger for node manager
	Log logr.Logger
	// lock to prevent multiple routines to write/update to data store concurrently
	lock sync.RWMutex
	// dataStore is the in memory data store of all the managed nodes in the cluster
	dataStore map[string]Node
}

type Manager interface {
	AddOrUpdateNode(v1Node *v1.Node) error
	DeleteNode(v1node *v1.Node) error
	GetNode(nodeName string) (node Node, managed bool)
}

// NewNodeManager returns a new node manager
func NewNodeManager(logger logr.Logger) Manager {
	return &manager{
		Log:       logger,
		dataStore: make(map[string]Node),
	}
}

// GetNode returns the node from in memory store
func (m *manager) GetNode(nodeName string) (node Node, managed bool) {
	m.lock.RLock()
	defer m.lock.RUnlock()

	node, managed = m.dataStore[nodeName]
	return
}

// AddNode adds or updates the node to the node manager cache and performs resource initialization/updates based
// on the managed status of the node.
func (m *manager) AddOrUpdateNode(v1Node *v1.Node) error {
	// postUnlockOperation is any operation that involves making network call. It must be done after
	// releasing the node manager lock to allow concurrent processing of multiple nodes and not blocking
	// the GetNode call in the critical path of pod processing.
	postUnlockOperation, err := m.addOrUpdateNode(v1Node)

	if err != nil {
		return err
	}

	return m.performPostUnlockOperation(v1Node.Name, postUnlockOperation)
}

// DeleteNode deletes the nodes from the cache and cleans up the resources used by all the resource providers
func (m *manager) DeleteNode(v1Node *v1.Node) error {
	// postUnlockOperation is any operation that involves making network call. It must be done after
	// releasing the node manager lock to allow concurrent processing of multiple nodes and not blocking
	// the GetNode call in the critical path of pod processing.
	postUnlockOperation, err := m.deleteNode(v1Node)

	if err != nil {
		return err
	}

	return m.performPostUnlockOperation(v1Node.Name, postUnlockOperation)
}

// addOrUpdateNode adds eligible nodes to the cache. If the node was previously managed and
// is not eligible for management currently, the node is removed
func (m *manager) addOrUpdateNode(v1Node *v1.Node) (postUnlockOperation func() error, err error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	log := m.Log.WithValues("node name", v1Node.Name, "request", "add/update")

	node, managed := m.dataStore[v1Node.Name]

	if managed { // Cache hit
		shouldManageNode := m.isSelectedForManagement(v1Node)
		if !shouldManageNode {
			log.Info("no updates on the managed status of the node")
			postUnlockOperation = node.UpdateResources
			return
		}

		delete(m.dataStore, v1Node.Name)
		postUnlockOperation = node.DeleteResources

		log.Info("node removed from the list of managed node as it's not eligible for management anymore")

	} else { // Cache miss
		isSelected := m.isSelectedForManagement(v1Node)
		if !isSelected {
			log.Info("skipping as node is not eligible for management by controller")
			return
		}

		// Node is eligible for management.
		instanceId := getNodeInstanceID(v1Node)
		os := getNodeOS(v1Node)

		if instanceId == "" || os == "" {
			err = fmt.Errorf("instance id %s or os %s  not found in the label", instanceId, os)
			log.Error(err, "not adding node to list of managed node")
			return
		}

		node := NewNode(m.Log.WithName("node initializer").WithValues("name",
			v1Node.Name), instanceId, os)

		m.dataStore[v1Node.Name] = node
		postUnlockOperation = node.InitResources

		log.Info("node added to list of managed node")
	}
	return
}

// deleteNode deletes the nodes from the node manager cache
func (m *manager) deleteNode(v1Node *v1.Node) (postUnlockOperation func() error, err error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	log := m.Log.WithValues("node name", v1Node.Name, "request", "delete")

	node, managed := m.dataStore[v1Node.Name]

	if !managed {
		log.Info("node is not managed by controller, not processing the request")
		return
	}

	delete(m.dataStore, v1Node.Name)
	postUnlockOperation = node.DeleteResources

	log.Info("node removed from list of managed node")

	return
}

// performPostUnlockOperation performs the operation on a node without taking the node manager lock
func (m *manager) performPostUnlockOperation(nodeName string, postUnlockOperation func() error) error {
	log := m.Log.WithValues("node", nodeName)
	if postUnlockOperation == nil {
		return nil
	}

	err := postUnlockOperation()
	if err == nil {
		log.Info("successfully performed node operation", "operation", postUnlockOperation)
		return nil
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	log.Error(err, "failed to performed node operation", "operation", postUnlockOperation)

	if err == ErrInitResources {
		// Remove entry from the cache, so it's initialized again
		delete(m.dataStore, nodeName)
	}

	return err
}

// isSelectedForManagement returns true if the node should be managed by the controller
func (m *manager) isSelectedForManagement(v1node *v1.Node) bool {
	labelSelector := isManagedLabelSet(v1node)
	capacitySelector := isPodENICapacitySet(v1node)

	return labelSelector || capacitySelector
}

// getNodeInstanceID returns the EC2 instance ID of a node
func getNodeInstanceID(node *v1.Node) string {
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
func getNodeOS(node *v1.Node) string {
	labels := node.GetLabels()
	os := labels[config.NodeLabelOS]
	if os == "" {
		// For older k8s version.
		os = labels[config.NodeLabelOSBeta]
	}
	return os
}

// isManagedLabelSet returns true if the node has the vpc controller's key value pair set
func isManagedLabelSet(node *v1.Node) bool {
	labels := node.GetLabels()

	nodeValue, ok := labels[config.VPCManagerLabel]
	if ok && nodeValue == config.VPCManagedBy {
		return true
	}

	return false
}

// isPodENICapacitySet returns true if the node has greater than 0 capacity for resource pod-eni
func isPodENICapacitySet(node *v1.Node) bool {
	cap := node.Status.Capacity[config.ResourceNamePodENI]
	return !cap.IsZero()
}
