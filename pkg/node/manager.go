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
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"

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
	// resourceProviders is the list of resource providers
	resourceProviders []provider.ResourceProvider
	// ec2APIHelper is the helper function to get instance details from EC2 API
	ec2APIHelper api.EC2APIHelper
	// k8sWrapper is the wrapper to get k8s object
	k8sWrapper k8s.K8sWrapper
}

type Manager interface {
	AddOrUpdateNode(v1Node *v1.Node) error
	DeleteNode(nodeName string) error
	GetNode(nodeName string) (node Node, managed bool)
}

// NewNodeManager returns a new node manager
func NewNodeManager(logger logr.Logger, provider []provider.ResourceProvider, ec2APIHelper api.EC2APIHelper, k8sWrapper k8s.K8sWrapper) Manager {
	return &manager{
		resourceProviders: provider,
		Log:               logger,
		dataStore:         make(map[string]Node),
		ec2APIHelper:      ec2APIHelper,
		k8sWrapper:        k8sWrapper,
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
func (m *manager) DeleteNode(nodeName string) error {
	// postUnlockOperation is any operation that involves making network call. It must be done after
	// releasing the node manager lock to allow concurrent processing of multiple nodes and not blocking
	// the GetNode call in the critical path of pod processing.
	postUnlockOperation, err := m.deleteNode(nodeName)

	if err != nil {
		return err
	}

	return m.performPostUnlockOperation(nodeName, postUnlockOperation)
}

// addOrUpdateNode adds eligible nodes to the cache. If the node was previously managed and
// is not eligible for management currently, the node is removed
func (m *manager) addOrUpdateNode(v1Node *v1.Node) (postUnlockOperation func([]provider.ResourceProvider, api.EC2APIHelper) error, err error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	log := m.Log.WithValues("node name", v1Node.Name, "request", "add/update")

	node, managed := m.dataStore[v1Node.Name]

	if managed { // Cache hit
		shouldManageNode := m.isSelectedForManagement(v1Node)
		if shouldManageNode {
			log.V(1).Info("no updates on the managed status of the node")
			err = m.updateSubnetIfUsingENIConfig(node, v1Node)
			postUnlockOperation = node.UpdateResources
			return
		}

		delete(m.dataStore, v1Node.Name)
		postUnlockOperation = node.DeleteResources

		log.Info("node removed from the list of managed node as it's not eligible for management anymore")

	} else { // Cache miss
		isSelected := m.isSelectedForManagement(v1Node)
		if !isSelected {
			log.V(1).Info("skipping as node is not eligible for management by controller")
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
			v1Node.Name), v1Node.Name, instanceId, os)

		err = m.updateSubnetIfUsingENIConfig(node, v1Node)
		if err != nil {
			return
		}

		m.dataStore[v1Node.Name] = node
		postUnlockOperation = node.InitResources

		log.Info("node added to list of managed node")
	}
	return
}

// deleteNode deletes the nodes from the node manager cache
func (m *manager) deleteNode(nodeName string) (postUnlockOperation func([]provider.ResourceProvider, api.EC2APIHelper) error, err error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	log := m.Log.WithValues("node name", nodeName, "request", "delete")

	node, managed := m.dataStore[nodeName]

	if !managed {
		log.Info("node is not managed by controller, not processing the request")
		return
	}

	delete(m.dataStore, nodeName)
	postUnlockOperation = node.DeleteResources

	log.Info("node removed from list of managed node")

	return
}

// updateSubnetIfUsingENIConfig updates the subnet id for the node to the subnet specified in ENIConfig if the node is
// using custom networking
func (m *manager) updateSubnetIfUsingENIConfig(node Node, k8sNode *v1.Node) error {
	eniConfigName, isPresent := k8sNode.Labels[config.CustomNetworkingLabel]
	if isPresent {
		eniConfig, err := m.k8sWrapper.GetENIConfig(eniConfigName)
		if err != nil {
			return fmt.Errorf("failed to find the ENIConfig %s: %v", eniConfigName, err)
		}
		if eniConfig.Spec.Subnet != "" {
			m.Log.V(1).Info("node is using custom networking, updating the subnet", "node", k8sNode.Name,
				"subnet", eniConfig.Spec.Subnet)
			node.UpdateCustomNetworkingSpecs(eniConfig.Spec.Subnet, eniConfig.Spec.SecurityGroups)
			return nil
		}
		return fmt.Errorf("failed to find subnet in eniconfig spec %s", eniConfigName)
	} else {
		node.UpdateCustomNetworkingSpecs("", nil)
	}
	return nil
}

// performPostUnlockOperation performs the operation on a node without taking the node manager lock
func (m *manager) performPostUnlockOperation(nodeName string, postUnlockOperation func([]provider.ResourceProvider, api.EC2APIHelper) error) error {
	log := m.Log.WithValues("node", nodeName)
	if postUnlockOperation == nil {
		return nil
	}

	err := postUnlockOperation(m.resourceProviders, m.ec2APIHelper)
	operationName := runtime.FuncForPC(reflect.ValueOf(postUnlockOperation).Pointer()).Name()
	if err == nil {
		log.V(1).Info("successfully performed node operation", "operation", operationName)
		return nil
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	log.Error(err, "failed to performed node operation", "operation", operationName)

	if err == ErrInitResources {
		// Remove entry from the cache, so it's initialized again
		log.Info("removing the node from cache as it failed to initialize")
		delete(m.dataStore, nodeName)
	}

	return err
}

// isSelectedForManagement returns true if the node should be managed by the controller
func (m *manager) isSelectedForManagement(v1node *v1.Node) bool {
	//return isWindowsNode(v1node) || canAttachTrunk(v1node)
	return canAttachTrunk(v1node)
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
