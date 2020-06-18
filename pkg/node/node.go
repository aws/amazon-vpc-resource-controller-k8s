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

	"github.com/go-logr/logr"
)

type node struct {
	// lock to perform serial operations on a node
	lock sync.RWMutex
	// os is the operating system of the worker node
	os string
	// instanceId of the worker node
	instanceId string
	// log is the logger setup with the key value pair set to node's name
	log logr.Logger
	// ready status indicates if the node is ready to process request or not
	ready bool
}

var (
	ErrInitResources   = fmt.Errorf("failed to initalize resources")
	ErrDeleteResources = fmt.Errorf("failed to delete resources")
	ErrUpdateResources = fmt.Errorf("failed to update resources")
)

type Node interface {
	InitResources() error
	DeleteResources() error
	UpdateResources() error
}

// NewNode returns a new node object
func NewNode(log logr.Logger, instanceId string, os string) Node {
	return &node{
		log:        log,
		os:         os,
		instanceId: instanceId,
	}
}

// UpdateNode refreshes the capacity if it's reset to 0
func (n *node) UpdateResources() error {
	n.lock.Lock()
	defer n.lock.Unlock()

	// TODO: Check for windows pod if capacity has been reset to zero.
	return nil
}

// InitResources initializes the resource pool and provider of all supported resources
func (n *node) InitResources() error {
	n.lock.Lock()
	defer n.lock.Unlock()

	// TODO: Initialize all the providers.

	n.ready = true
	return nil
}

// DeleteResources performs clean up of all the resource pools and provider of the nodes
func (n *node) DeleteResources() error {
	n.lock.Lock()
	defer n.lock.Unlock()

	// TODO: De initialize all providers.
	return nil
}
