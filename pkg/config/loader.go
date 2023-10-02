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

package config

import (
	"strconv"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
)

const (
	// TODO: Should we always do this max retry no matter why it fails
	// such deleted pods will also be retried 5 times, which could be an issue for large pods loads and high churning rate.
	WorkQueueDefaultMaxRetries = 5

	// Default Configuration for Pod ENI resource type
	PodENIDefaultWorker = 20

	// Default Configuration for IPv4 resource type
	IPv4DefaultWorker  = 2
	IPv4DefaultWPSize  = 3
	IPv4DefaultMaxDev  = 1
	IPv4DefaultResSize = 0

	// Default Configuration for IPv4 prefix resource type
	IPv4PDDefaultWorker               = 2
	IPv4PDDefaultWPSize               = 1
	IPv4PDDefaultMaxDev               = 0
	IPv4PDDefaultResSize              = 0
	IPv4PDDefaultWarmIPTargetSize     = 1
	IPv4PDDefaultMinIPTargetSize      = 3
	IPv4PDDefaultWarmPrefixTargetSize = 0

	// EC2 API QPS for user service client
	// Tested: 15 + 20 limits
	// Tested: 15 + 8 limits (not seeing significant degradation from 15+20)
	// Tested: 12 + 8 limits (not seeing significant degradation from 15+8)
	// Larger number seems not make latency better than 12+8
	UserServiceClientQPS      = 12
	UserServiceClientQPSBurst = 8

	// EC2 API QPS for instance service client
	InstanceServiceClientQPS   = 5
	InstanceServiceClientBurst = 7

	// API Server QPS
	DefaultAPIServerQPS   = 10
	DefaultAPIServerBurst = 15
)

// LoadResourceConfig returns the Resource Configuration for all resources managed by the VPC Resource Controller. Currently
// returns the default resource configuration and later can return the configuration from a ConfigMap.
func LoadResourceConfig() map[string]ResourceConfig {
	return getDefaultResourceConfig()
}

func LoadResourceConfigFromConfigMap(log logr.Logger, vpcCniConfigMap *v1.ConfigMap) map[string]ResourceConfig {
	resourceConfig := getDefaultResourceConfig()

	warmIPTarget, minIPTarget, warmPrefixTarget := ParseWinPDTargets(log, vpcCniConfigMap)

	// If no PD configuration is set in configMap or none is valid, return default resource config
	if warmIPTarget == 0 && minIPTarget == 0 && warmPrefixTarget == 0 {
		return resourceConfig
	}

	resourceConfig[ResourceNameIPAddressFromPrefix].WarmPoolConfig.WarmIPTarget = warmIPTarget
	resourceConfig[ResourceNameIPAddressFromPrefix].WarmPoolConfig.MinIPTarget = minIPTarget
	resourceConfig[ResourceNameIPAddressFromPrefix].WarmPoolConfig.WarmPrefixTarget = warmPrefixTarget

	return resourceConfig
}

// ParseWinPDTargets parses config map for Windows prefix delegation configurations set by users
func ParseWinPDTargets(log logr.Logger, vpcCniConfigMap *v1.ConfigMap) (warmIPTarget int, minIPTarget int, warmPrefixTarget int) {
	warmIPTarget, minIPTarget, warmPrefixTarget = 0, 0, 0

	if vpcCniConfigMap.Data == nil {
		return warmIPTarget, minIPTarget, warmPrefixTarget
	}

	warmIPTargetStr, foundWarmIP := vpcCniConfigMap.Data[WarmIPTarget]
	minIPTargetStr, foundMinIP := vpcCniConfigMap.Data[MinimumIPTarget]
	warmPrefixTargetStr, foundWarmPrefix := vpcCniConfigMap.Data[WarmPrefixTarget]

	// If no configuration is found, return 0
	if !foundWarmIP && !foundMinIP && !foundWarmPrefix {
		return warmIPTarget, minIPTarget, warmPrefixTarget
	}

	if foundWarmIP {
		warmIPTargetInt, err := strconv.Atoi(warmIPTargetStr)
		if err != nil {
			log.Error(err, "failed to parse warm ip target", "warm ip target", warmIPTargetStr)
		} else {
			warmIPTarget = warmIPTargetInt
		}
	}
	if foundMinIP {
		minIPTargetInt, err := strconv.Atoi(minIPTargetStr)
		if err != nil {
			log.Error(err, "failed to parse minimum ip target", "minimum ip target", minIPTargetStr)
		} else {
			minIPTarget = minIPTargetInt
		}
	}
	if foundWarmPrefix {
		warmPrefixTargetInt, err := strconv.Atoi(warmPrefixTargetStr)
		if err != nil {
			log.Error(err, "failed to parse warm prefix target", "warm prefix target", warmPrefixTargetStr)
		} else {
			warmPrefixTarget = warmPrefixTargetInt
		}
	}
	return warmIPTarget, minIPTarget, warmPrefixTarget
}

// getDefaultResourceConfig returns the default Resource Configuration.
func getDefaultResourceConfig() map[string]ResourceConfig {

	config := make(map[string]ResourceConfig)

	// Create default configuration for Pod ENI Resource
	podENIConfig := ResourceConfig{
		Name:           ResourceNamePodENI,
		WorkerCount:    PodENIDefaultWorker,
		SupportedOS:    map[string]bool{OSWindows: false, OSLinux: true},
		WarmPoolConfig: nil,
	}
	config[ResourceNamePodENI] = podENIConfig

	// Create default configuration for IPv4 Resource
	ipV4WarmPoolConfig := WarmPoolConfig{
		DesiredSize:  IPv4DefaultWPSize,
		MaxDeviation: IPv4DefaultMaxDev,
		ReservedSize: IPv4DefaultResSize,
	}
	ipV4Config := ResourceConfig{
		Name:           ResourceNameIPAddress,
		WorkerCount:    IPv4DefaultWorker,
		SupportedOS:    map[string]bool{OSWindows: true, OSLinux: false},
		WarmPoolConfig: &ipV4WarmPoolConfig,
	}
	config[ResourceNameIPAddress] = ipV4Config

	// Create default configuration for prefix-deconstructed IPv4 resource pool
	prefixIPv4WarmPoolConfig := WarmPoolConfig{
		DesiredSize:      IPv4PDDefaultWPSize,
		MaxDeviation:     IPv4PDDefaultMaxDev,
		ReservedSize:     IPv4PDDefaultResSize,
		WarmIPTarget:     IPv4PDDefaultWarmIPTargetSize,
		MinIPTarget:      IPv4PDDefaultMinIPTargetSize,
		WarmPrefixTarget: IPv4PDDefaultWarmPrefixTargetSize,
	}
	prefixIPv4Config := ResourceConfig{
		Name:           ResourceNameIPAddressFromPrefix,
		WorkerCount:    IPv4PDDefaultWorker,
		SupportedOS:    map[string]bool{OSWindows: true, OSLinux: false},
		WarmPoolConfig: &prefixIPv4WarmPoolConfig,
	}
	config[ResourceNameIPAddressFromPrefix] = prefixIPv4Config

	return config
}
