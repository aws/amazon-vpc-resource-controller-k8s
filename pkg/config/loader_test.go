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
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
)

// TestLoadResourceConfig tests the default resource configurations
func TestLoadResourceConfig(t *testing.T) {
	defaultResourceConfig := getDefaultResourceConfig()

	// Verify default resource configuration for resource Pod ENI
	podENIConfig := defaultResourceConfig[ResourceNamePodENI]
	assert.Equal(t, ResourceNamePodENI, podENIConfig.Name)
	assert.Equal(t, PodENIDefaultWorker, podENIConfig.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: true, OSWindows: false}, podENIConfig.SupportedOS)
	assert.Nil(t, podENIConfig.WarmPoolConfig)

	// Verify default resource configuration for resource IPv4 Address
	ipV4Config := defaultResourceConfig[ResourceNameIPAddress]
	assert.Equal(t, ResourceNameIPAddress, ipV4Config.Name)
	assert.Equal(t, IPv4DefaultWorker, ipV4Config.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: false, OSWindows: true}, ipV4Config.SupportedOS)

	// Verify default Warm pool configuration for IPv4 Address
	ipV4WPConfig := ipV4Config.WarmPoolConfig
	assert.Equal(t, IPv4DefaultWPSize, ipV4WPConfig.DesiredSize)
	assert.Equal(t, IPv4DefaultMaxDev, ipV4WPConfig.MaxDeviation)
	assert.Equal(t, IPv4DefaultResSize, ipV4WPConfig.ReservedSize)

	// Verify default resource configuration for prefix-deconstructed IPv4 Address
	prefixIPv4Config := defaultResourceConfig[ResourceNameIPAddressFromPrefix]
	assert.Equal(t, ResourceNameIPAddressFromPrefix, prefixIPv4Config.Name)
	assert.Equal(t, IPv4PDDefaultWorker, prefixIPv4Config.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: false, OSWindows: true}, prefixIPv4Config.SupportedOS)

	// Verify default Warm pool configuration for prefix-deconstructed IPv4 Address
	prefixIPv4WPConfig := prefixIPv4Config.WarmPoolConfig
	assert.Equal(t, IPv4PDDefaultWPSize, prefixIPv4WPConfig.DesiredSize)
	assert.Equal(t, IPv4PDDefaultMaxDev, prefixIPv4WPConfig.MaxDeviation)
	assert.Equal(t, IPv4PDDefaultResSize, prefixIPv4WPConfig.ReservedSize)
	assert.Equal(t, IPv4PDDefaultWarmIPTargetSize, prefixIPv4WPConfig.WarmIPTarget)
	assert.Equal(t, IPv4PDDefaultMinIPTargetSize, prefixIPv4WPConfig.MinIPTarget)
	assert.Equal(t, IPv4PDDefaultWarmPrefixTargetSize, prefixIPv4WPConfig.WarmPrefixTarget)
}

// TestParseWinPDTargets parses prefix delegation configurations from a vpc cni config map
func TestParseWinPDTargets(t *testing.T) {
	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     strconv.Itoa(IPv4PDDefaultWarmIPTargetSize),
			MinimumIPTarget:                  strconv.Itoa(IPv4PDDefaultMinIPTargetSize),
			WarmPrefixTarget:                 strconv.Itoa(IPv4PDDefaultWarmPrefixTargetSize),
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget := ParseWinPDTargets(vpcCNIConfig)
	assert.Equal(t, IPv4PDDefaultWarmIPTargetSize, warmIPTarget)
	assert.Equal(t, IPv4PDDefaultMinIPTargetSize, minIPTarget)
	assert.Equal(t, IPv4PDDefaultWarmPrefixTargetSize, warmPrefixTarget)
}

// TestParseWinPDTargets parses prefix delegation configurations with negative values and returns the same
func TestParseWinPDTargets_Negative(t *testing.T) {
	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     strconv.Itoa(-10),
			MinimumIPTarget:                  strconv.Itoa(-100),
			WarmPrefixTarget:                 strconv.Itoa(0),
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget := ParseWinPDTargets(vpcCNIConfig)
	// negative values are still read in but processed when it's used in the warm pool
	assert.Equal(t, -10, warmIPTarget)
	assert.Equal(t, -100, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
}

// TestParseWinPDTargets_Invalid parses prefix delegation configurations with invalid values and returns 0s as targets
func TestParseWinPDTargets_Invalid(t *testing.T) {
	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     "random",
			MinimumIPTarget:                  "string val",
			WarmPrefixTarget:                 "can't parse",
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget := ParseWinPDTargets(vpcCNIConfig)
	assert.Equal(t, 0, warmIPTarget)
	assert.Equal(t, 0, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
}

// TestLoadResourceConfigFromConfigMap tests the custom configuration for PD is loaded correctly from config map
func TestLoadResourceConfigFromConfigMap(t *testing.T) {
	warmIPTarget := 2
	minimumIPTarget := 10
	warmPrefixTarget := 1
	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     strconv.Itoa(warmIPTarget),
			MinimumIPTarget:                  strconv.Itoa(minimumIPTarget),
			WarmPrefixTarget:                 strconv.Itoa(warmPrefixTarget),
		},
	}

	resourceConfig := LoadResourceConfigFromConfigMap(vpcCNIConfig)

	// Verify default resource configuration for resource Pod ENI
	podENIConfig := resourceConfig[ResourceNamePodENI]
	assert.Equal(t, ResourceNamePodENI, podENIConfig.Name)
	assert.Equal(t, PodENIDefaultWorker, podENIConfig.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: true, OSWindows: false}, podENIConfig.SupportedOS)
	assert.Nil(t, podENIConfig.WarmPoolConfig)

	// Verify default resource configuration for resource IPv4 Address
	ipV4Config := resourceConfig[ResourceNameIPAddress]
	assert.Equal(t, ResourceNameIPAddress, ipV4Config.Name)
	assert.Equal(t, IPv4DefaultWorker, ipV4Config.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: false, OSWindows: true}, ipV4Config.SupportedOS)

	// Verify default Warm pool configuration for IPv4 Address
	ipV4WPConfig := ipV4Config.WarmPoolConfig
	assert.Equal(t, IPv4DefaultWPSize, ipV4WPConfig.DesiredSize)
	assert.Equal(t, IPv4DefaultMaxDev, ipV4WPConfig.MaxDeviation)
	assert.Equal(t, IPv4DefaultResSize, ipV4WPConfig.ReservedSize)

	// Verify default resource configuration for prefix-deconstructed IPv4 Address
	prefixIPv4Config := resourceConfig[ResourceNameIPAddressFromPrefix]
	assert.Equal(t, ResourceNameIPAddressFromPrefix, prefixIPv4Config.Name)
	assert.Equal(t, IPv4PDDefaultWorker, prefixIPv4Config.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: false, OSWindows: true}, prefixIPv4Config.SupportedOS)

	// Verify default Warm pool configuration for prefix-deconstructed IPv4 Address
	prefixIPv4WPConfig := prefixIPv4Config.WarmPoolConfig
	assert.Equal(t, IPv4PDDefaultWPSize, prefixIPv4WPConfig.DesiredSize)
	assert.Equal(t, IPv4PDDefaultMaxDev, prefixIPv4WPConfig.MaxDeviation)
	assert.Equal(t, IPv4PDDefaultResSize, prefixIPv4WPConfig.ReservedSize)
	assert.Equal(t, warmIPTarget, prefixIPv4WPConfig.WarmIPTarget)
	assert.Equal(t, minimumIPTarget, prefixIPv4WPConfig.MinIPTarget)
	assert.Equal(t, warmPrefixTarget, prefixIPv4WPConfig.WarmPrefixTarget)
}
