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
	PodENIDefaultWorker = 30

	// Default Windows Configuration for IPv4 resource type
	IPv4DefaultWinWorkerCount  = 2
	IPv4DefaultWinWarmIPTarget = 3
	IPv4DefaultWinMinIPTarget  = 3
	IPv4DefaultWinMaxDev       = 0
	IPv4DefaultWinResSize      = 0

	// Default Windows Configuration for IPv4 prefix resource type
	IPv4PDDefaultWorker               = 2
	IPv4PDDefaultWPSize               = 1
	IPv4PDDefaultMaxDev               = 0
	IPv4PDDefaultResSize              = 0
	IPv4PDDefaultWarmIPTargetSize     = 1
	IPv4PDDefaultMinIPTargetSize      = 3
	IPv4PDDefaultWarmPrefixTargetSize = 0
)

// LoadResourceConfig returns the Resource Configuration for all resources managed by the VPC Resource Controller. Currently
// returns the default resource configuration and later can return the configuration from a ConfigMap.
func LoadResourceConfig() map[string]ResourceConfig {
	return getDefaultResourceConfig()
}

func LoadResourceConfigFromConfigMap(log logr.Logger, vpcCniConfigMap *v1.ConfigMap) map[string]ResourceConfig {
	resourceConfig := getDefaultResourceConfig()

	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCniConfigMap)

	// If no PD configuration is set in configMap or none is valid, return default resource config
	if warmIPTarget == 0 && minIPTarget == 0 && warmPrefixTarget == 0 {
		return resourceConfig
	}

	if isPDEnabled {
		resourceConfig[ResourceNameIPAddressFromPrefix].WarmPoolConfig.WarmIPTarget = warmIPTarget
		resourceConfig[ResourceNameIPAddressFromPrefix].WarmPoolConfig.MinIPTarget = minIPTarget
		resourceConfig[ResourceNameIPAddressFromPrefix].WarmPoolConfig.WarmPrefixTarget = warmPrefixTarget
	} else {
		resourceConfig[ResourceNameIPAddress].WarmPoolConfig.WarmIPTarget = warmIPTarget
		resourceConfig[ResourceNameIPAddress].WarmPoolConfig.MinIPTarget = minIPTarget
	}

	return resourceConfig
}

// ParseWinIPTargetConfigs parses Windows IP target configuration parameters in the amazon-vpc-cni ConfigMap
// If all three config parameter values (warm-ip-target, min-ip-target, warm-prefix-target) are 0 or unset, or config map does not exist,
// then default values for warm-ip-target and min-ip-target will be set.
func ParseWinIPTargetConfigs(log logr.Logger, vpcCniConfigMap *v1.ConfigMap) (warmIPTarget int, minIPTarget int, warmPrefixTarget int, isPDEnabled bool) {
	if vpcCniConfigMap.Data == nil {
		warmIPTarget = IPv4DefaultWinWarmIPTarget
		minIPTarget = IPv4DefaultWinMinIPTarget
		log.Info(
			"No ConfigMap data found, falling back to using default values",
			"minIPTarget", minIPTarget,
			"warmIPTarget", warmIPTarget,
		)
		return warmIPTarget, minIPTarget, 0, false
	}

	isPDEnabled, err := strconv.ParseBool(vpcCniConfigMap.Data[EnableWindowsPrefixDelegationKey])
	if err != nil {
		log.Info("Could not parse prefix delegation flag from ConfigMap, falling back to using secondary IP mode")
		isPDEnabled = false
	}

	warmIPTargetStr, foundWarmIP := vpcCniConfigMap.Data[WarmIPTarget]
	if !foundWarmIP {
		warmIPTargetStr, foundWarmIP = vpcCniConfigMap.Data[WinWarmIPTarget]
	}
	minIPTargetStr, foundMinIP := vpcCniConfigMap.Data[MinimumIPTarget]
	if !foundMinIP {
		minIPTargetStr, foundMinIP = vpcCniConfigMap.Data[WinMinimumIPTarget]
	}
	warmPrefixTargetStr, foundWarmPrefix := vpcCniConfigMap.Data[WarmPrefixTarget]
	if !foundWarmPrefix {
		warmPrefixTargetStr, foundWarmPrefix = vpcCniConfigMap.Data[WinWarmPrefixTarget]
	}

	// If warm IP target config value is not found, or there is an error parsing it, the value will be set to zero
	if foundWarmIP {
		warmIPTarget, err = strconv.Atoi(warmIPTargetStr)
		if err != nil {
			log.Info("Could not parse warm ip target, defaulting to zero", "warm ip target", warmIPTargetStr)
		} else if !isPDEnabled && warmIPTarget == 0 {
			// Handle secondary IP mode scenario where WarmIPTarget is explicitly configured to zero
			// In such a case there must always be 1 warm IP to ensure that the warmpool is never empty
			log.Info("Explicitly setting WarmIPTarget zero value not supported in secondary IP mode, will override with 1")
			warmIPTarget = 1
		}
	} else {
		log.Info("could not find warm ip target in ConfigMap, defaulting to zero")
		warmIPTarget = 0
	}

	// If min IP target config value is not found, or there is an error parsing it, the value will be set to zero
	if foundMinIP {
		minIPTarget, err = strconv.Atoi(minIPTargetStr)
		if err != nil {
			log.Info("Could not parse minimum ip target, defaulting to zero", "minimum ip target", minIPTargetStr)
		}
	} else {
		log.Info("could not find minimum ip target in ConfigMap, defaulting to zero")
		minIPTarget = 0
	}

	warmPrefixTarget = 0
	if isPDEnabled && foundWarmPrefix {
		warmPrefixTarget, err = strconv.Atoi(warmPrefixTargetStr)
		if err != nil {
			log.Info("Could not parse warm prefix target, defaulting to zero", "warm prefix target", warmPrefixTargetStr)
		}
	}

	if warmIPTarget == 0 && minIPTarget == 0 {
		if isPDEnabled && warmPrefixTarget == 0 {
			minIPTarget = IPv4PDDefaultMinIPTargetSize
			warmIPTarget = IPv4PDDefaultWarmIPTargetSize
			warmPrefixTarget = IPv4PDDefaultWarmPrefixTargetSize
		} else if !isPDEnabled {
			minIPTarget = IPv4DefaultWinMinIPTarget
			warmIPTarget = IPv4DefaultWinWarmIPTarget
		}
		log.Info(
			"Encountered zero values for warm-ip-target, min-ip-target and warm-prefix-target in ConfigMap data, falling back to using default values since on demand IP allocation is not supported",
			"minIPTarget", minIPTarget,
			"warmIPTarget", warmIPTarget,
			"warmPrefixTarget", warmPrefixTarget,
			"isPDEnabled", isPDEnabled,
		)
	}

	return warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled
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
		DesiredSize:  IPv4DefaultWinWarmIPTarget,
		WarmIPTarget: IPv4DefaultWinWarmIPTarget,
		MinIPTarget:  IPv4DefaultWinMinIPTarget,
		MaxDeviation: IPv4DefaultWinMaxDev,
		ReservedSize: IPv4DefaultWinResSize,
	}
	ipV4Config := ResourceConfig{
		Name:           ResourceNameIPAddress,
		WorkerCount:    IPv4DefaultWinWorkerCount,
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
