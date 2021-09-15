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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/handler"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/ip"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	ctrl "sigs.k8s.io/controller-runtime"
)

type Manager struct {
	handlers  map[string]handler.Handler
	providers []provider.ResourceProvider
}

type ResourceManager interface {
	GetResourceHandlers() []handler.Handler
	GetResourceProviders() []provider.ResourceProvider
	GetResourceHandler(resourceName string) (handler.Handler, bool)
}

func NewResourceManager(ctx context.Context, resourceNames []string, wrapper api.Wrapper) (ResourceManager, error) {
	// Load that static configuration of the resource
	resourceConfig := config.LoadResourceConfig()
	// Resource Handler supported by the controller
	resourceHandlers := make(map[string]handler.Handler)

	var resourceProviders []provider.ResourceProvider

	// For each supported resource, initialize the resource provider and handler
	for _, resourceName := range resourceNames {

		resourceConfig, ok := resourceConfig[resourceName]
		if !ok {
			return nil, fmt.Errorf("failed to find resource configuration %s", resourceName)
		}

		ctrl.Log.Info("initializing resource", "resource name",
			resourceName, "resource count", resourceConfig.WorkerCount)
		workers := worker.NewDefaultWorkerPool(
			resourceConfig.Name,
			resourceConfig.WorkerCount,
			config.WorkQueueDefaultMaxRetries,
			ctrl.Log.WithName(fmt.Sprintf("%s-%s", resourceName, "worker")), ctx)

		var resourceHandler handler.Handler
		var resourceProvider provider.ResourceProvider

		if resourceName == config.ResourceNameIPAddress {
			resourceProvider = ip.NewIPv4Provider(ctrl.Log.WithName("ipv4 provider"),
				wrapper, workers, resourceConfig)
			resourceHandler = handler.NewWarmResourceHandler(ctrl.Log.WithName(resourceName), wrapper,
				resourceName, resourceProvider)
		} else if resourceName == config.ResourceNamePodENI {
			resourceProvider = branch.NewBranchENIProvider(ctrl.Log.WithName("branch eni provider"),
				wrapper, workers, resourceConfig, ctx)
			resourceHandler = handler.NewOnDemandHandler(ctrl.Log.WithName(resourceName),
				resourceName, resourceProvider)
		} else {
			return nil, fmt.Errorf("resource type is not defnied %s", resourceName)
		}

		err := workers.StartWorkerPool(resourceProvider.ProcessAsyncJob)
		if err != nil {
			return nil, fmt.Errorf("unable to start the workers for resource %s", resourceName)
		}

		resourceHandlers[resourceName] = resourceHandler
		resourceProviders = append(resourceProviders, resourceProvider)

		ctrl.Log.Info("successfully initialized resource handler and provider",
			"resource name", resourceName)
	}

	return &Manager{
		handlers:  resourceHandlers,
		providers: resourceProviders,
	}, nil
}

func (m *Manager) GetResourceHandlers() []handler.Handler {
	var handlers []handler.Handler
	for _, handler := range m.handlers {
		handlers = append(handlers, handler)
	}
	return handlers
}

func (m *Manager) GetResourceProviders() []provider.ResourceProvider {
	return m.providers
}

func (m *Manager) GetResourceHandler(resourceName string) (handler.Handler, bool) {
	handler, found := m.handlers[resourceName]
	return handler, found
}
