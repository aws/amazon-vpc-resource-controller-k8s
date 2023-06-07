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

package resource

import (
	"context"
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/ip"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/prefix"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"
	"github.com/go-logr/logr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

var (
	managerHealthCheckSubpath            = "health-resource-manager"
	branchProviderHealthCheckSubpath     = "health-branch-provider"
	ipv4ProviderHealthCheckSubpath       = "health-ipv4-provider"
	ipv4PrefixProviderHealthCheckSubpath = "health-ipv4-prefix-provider"
)

type Manager struct {
	resource map[string]Resource
	log      logr.Logger
}

type Resource struct {
	handler.Handler
	provider.ResourceProvider
}

type ResourceManager interface {
	GetResourceProviders() map[string]provider.ResourceProvider
	GetResourceProvider(resourceName string) (provider.ResourceProvider, bool)
	GetResourceHandler(resourceName string) (handler.Handler, bool)
}

func NewResourceManager(ctx context.Context, resourceNames []string, wrapper api.Wrapper, log logr.Logger,
	healthzHandler *rcHealthz.HealthzHandler, conditions condition.Conditions) (ResourceManager, error) {
	// Load that static configuration of the resource
	resourceConfig := config.LoadResourceConfig()

	resources := make(map[string]Resource)

	healthCheckers := make(map[string]healthz.Checker)

	// add manager subpath into health checker map first
	healthCheckers[managerHealthCheckSubpath] = rcHealthz.SimplePing("resource manager", log)

	// For each supported resource, initialize the resource provider and handler
	for _, resourceName := range resourceNames {

		resourceConfig, ok := resourceConfig[resourceName]
		if !ok {
			return nil, fmt.Errorf("failed to find resource configuration %s", resourceName)
		}

		log.Info("initializing resource", "resource name",
			resourceName, "resource count", resourceConfig.WorkerCount)

		workers := worker.NewDefaultWorkerPool(
			resourceConfig.Name,
			resourceConfig.WorkerCount,
			config.WorkQueueDefaultMaxRetries,
			log.WithName(fmt.Sprintf("%s-%s", resourceName, "worker")), ctx)

		var resourceHandler handler.Handler
		var resourceProvider provider.ResourceProvider

		if resourceName == config.ResourceNameIPAddress {
			resourceProvider = ip.NewIPv4Provider(ctrl.Log.WithName("ipv4 provider"),
				wrapper, workers, resourceConfig, conditions)
			healthCheckers[ipv4ProviderHealthCheckSubpath] = resourceProvider.GetHealthChecker()
			resourceHandler = handler.NewWarmResourceHandler(ctrl.Log.WithName(resourceName), wrapper,
				resourceName, resourceProvider, ctx)
		} else if resourceName == config.ResourceNameIPAddressFromPrefix {
			resourceProvider = prefix.NewIPv4PrefixProvider(ctrl.Log.WithName("ipv4 prefix provider"),
				wrapper, workers, resourceConfig, conditions)
			healthCheckers[ipv4PrefixProviderHealthCheckSubpath] = resourceProvider.GetHealthChecker()
			resourceHandler = handler.NewWarmResourceHandler(ctrl.Log.WithName(resourceName), wrapper,
				config.ResourceNameIPAddress, resourceProvider, ctx)
		} else if resourceName == config.ResourceNamePodENI {
			resourceProvider = branch.NewBranchENIProvider(ctrl.Log.WithName("branch eni provider"),
				wrapper, workers, resourceConfig, ctx)
			healthCheckers[branchProviderHealthCheckSubpath] = resourceProvider.GetHealthChecker()
			resourceHandler = handler.NewOnDemandHandler(ctrl.Log.WithName(resourceName),
				resourceName, resourceProvider)
		} else {
			return nil, fmt.Errorf("resource type is not defnied %s", resourceName)
		}

		err := workers.StartWorkerPool(resourceProvider.ProcessAsyncJob)
		if err != nil {
			return nil, fmt.Errorf("unable to start the workers for resource %s", resourceName)
		}

		resources[resourceName] = Resource{
			Handler:          resourceHandler,
			ResourceProvider: resourceProvider,
		}

		log.Info("successfully initialized resource handler and provider",
			"resource name", resourceName)
	}

	// add health check on subpath for resource manager which includes providers as well
	healthzHandler.AddControllersHealthCheckers(healthCheckers)

	return &Manager{
		resource: resources,
		log:      log,
	}, nil
}

func (m *Manager) GetResourceProviders() map[string]provider.ResourceProvider {
	providers := make(map[string]provider.ResourceProvider)
	for resourceName, provider := range m.resource {
		providers[resourceName] = provider
	}
	return providers
}

func (m *Manager) GetResourceProvider(resourceName string) (provider.ResourceProvider, bool) {
	resource, found := m.resource[resourceName]
	if !found {
		return nil, found
	}
	return resource.ResourceProvider, found
}

func (m *Manager) GetResourceHandler(resourceName string) (handler.Handler, bool) {
	resource, found := m.resource[resourceName]
	if !found {
		return nil, found
	}
	return resource.Handler, found
}
