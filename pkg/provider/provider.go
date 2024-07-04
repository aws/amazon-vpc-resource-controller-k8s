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

package provider

import (
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/pool"
)

// ResourceProvider is the provider interface that each resource managed by the controller has to implement
type ResourceProvider interface {
	// InitResource initializes the resource provider
	InitResource(instance ec2.EC2Instance) error
	// DeInitResources de initializes the resource provider
	DeInitResource(instance ec2.EC2Instance) error
	// UpdateResourceCapacity updates the resource capacity
	UpdateResourceCapacity(instance ec2.EC2Instance) error
	// SubmitAsyncJob submits a job to the worker
	SubmitAsyncJob(job interface{})
	// ProcessAsyncJob processes a job form the worker queue
	ProcessAsyncJob(job interface{}) (ctrl.Result, error)
	// GetPool returns the warm pool for resources that support warm pool
	GetPool(nodeName string) (pool.Pool, bool)
	// IsInstanceSupported returns true if an instance type is supported by the provider
	IsInstanceSupported(instance ec2.EC2Instance) bool
	// Introspect allows introspection of all nodes for the given resource
	Introspect() interface{}
	// IntrospectNode allows introspection of a node for the given resource
	IntrospectNode(node string) interface{}
	// GetHealthChecker provider a health check subpath for pinging provider
	GetHealthChecker() healthz.Checker
	// IntrospectSummary allows introspection of resources summary per node
	IntrospectSummary() interface{}
	ReconcileNode(nodeName string) bool
}

// GetWinWarmPoolConfig retrieves Windows warmpool configuration from ConfigMap, falls back to using default values on failure
func GetWinWarmPoolConfig(log logr.Logger, w api.Wrapper, isPDEnabled bool) *config.WarmPoolConfig {
	var resourceConfig map[string]config.ResourceConfig
	vpcCniConfigMap, err := w.K8sAPI.GetConfigMap(config.VpcCniConfigMapName, config.KubeSystemNamespace)
	if err == nil {
		resourceConfig = config.LoadResourceConfigFromConfigMap(log, vpcCniConfigMap)
	} else {
		log.Error(err, "failed to read from config map, will use default resource config")
		resourceConfig = config.LoadResourceConfig()
	}

	if isPDEnabled {
		return resourceConfig[config.ResourceNameIPAddressFromPrefix].WarmPoolConfig
	} else {
		return resourceConfig[config.ResourceNameIPAddress].WarmPoolConfig
	}
}
