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

package pool

import (
	"github.com/go-logr/logr"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
)

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
