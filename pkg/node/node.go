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
	"sync"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"

	"github.com/go-logr/logr"
)

type node struct {
	// lock to perform serial operations on a node
	lock sync.RWMutex
	// log is the logger setup with the key value pair set to node's name
	log logr.Logger
	// ready status indicates if the node is ready to process request or not
	ready bool
	// instance stores the ec2 instance details that is shared by all the providers
	instance ec2.EC2Instance
}

var (
	ErrInitResources = fmt.Errorf("failed to initalize resources")
)

type Node interface {
	InitResources(resourceProviders []provider.ResourceProvider, helper api.EC2APIHelper) error
	DeleteResources(resourceProviders []provider.ResourceProvider, helper api.EC2APIHelper) error
	UpdateResources(resourceProviders []provider.ResourceProvider, helper api.EC2APIHelper) error

	UpdateInstanceCustomSubnet(subnetID string)
	IsReady() bool
}

// NewNode returns a new node object
func NewNode(log logr.Logger, nodeName string, instanceId string, os string) Node {
	return &node{
		log:      log,
		instance: ec2.NewEC2Instance(nodeName, instanceId, os),
	}
}

// UpdateNode refreshes the capacity if it's reset to 0
func (n *node) UpdateResources(resourceProviders []provider.ResourceProvider, helper api.EC2APIHelper) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	var errUpdates []error
	for _, resourceProvider := range resourceProviders {
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

	n.instance.UpdateCurrentSubnetAndCidrBlock(helper)

	return nil
}

// InitResources initializes the resource pool and provider of all supported resources
func (n *node) InitResources(resourceProviders []provider.ResourceProvider, helper api.EC2APIHelper) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	err := n.instance.LoadDetails(helper)
	if err != nil {
		n.log.Error(err, "failed to load instance details")
		return err
	}

	var initializedProviders []provider.ResourceProvider
	var errInit error
	for _, resourceProvider := range resourceProviders {
		// Check if the instance is supported and then initialize the provider
		if resourceProvider.IsInstanceSupported(n.instance) {
			errInit = resourceProvider.InitResource(n.instance)
			if errInit != nil {
				break
			}
			initializedProviders = append(initializedProviders, resourceProvider)
		}
	}

	if errInit != nil && len(initializedProviders) > 0 {
		for _, resourceProvider := range initializedProviders {
			errDeInit := resourceProvider.DeInitResource(n.instance)
			n.log.Error(errDeInit, "failed to de initialize resource")
		}
	}

	n.ready = true
	return errInit
}

// DeleteResources performs clean up of all the resource pools and provider of the nodes
func (n *node) DeleteResources(resourceProviders []provider.ResourceProvider, _ api.EC2APIHelper) error {
	n.lock.Lock()
	defer n.lock.Unlock()

	// Mark the node as not ready to prevent processing for any further pod events
	n.ready = false

	var errDelete []error
	for _, resourceProvider := range resourceProviders {
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
func (n *node) UpdateInstanceCustomSubnet(subnetID string) {
	n.instance.SetNewCustomNetworkingSubnetID(subnetID)
}

// IsReady returns true if all the providers have been initialized
func (n *node) IsReady() bool {
	n.lock.RLock()
	defer n.lock.RUnlock()

	return n.ready
}
