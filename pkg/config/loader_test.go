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
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
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
	assert.Equal(t, IPv4DefaultWinWorkerCount, ipV4Config.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: false, OSWindows: true}, ipV4Config.SupportedOS)

	// Verify default Warm pool configuration for IPv4 Address
	ipV4WPConfig := ipV4Config.WarmPoolConfig
	assert.Equal(t, IPv4DefaultWinWarmIPTarget, ipV4WPConfig.DesiredSize)
	assert.Equal(t, IPv4DefaultWinMinIPTarget, ipV4WPConfig.MinIPTarget)
	assert.Equal(t, IPv4DefaultWinMaxDev, ipV4WPConfig.MaxDeviation)
	assert.Equal(t, IPv4DefaultWinResSize, ipV4WPConfig.ReservedSize)

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

// TestParseWinIPTargetConfigs_PDEnabledWithDefaultTargets parses prefix delegation configurations from a vpc cni config map
func TestParseWinIPTargetConfigs_PDEnabledWithDefaultTargets(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     strconv.Itoa(IPv4PDDefaultWarmIPTargetSize),
			MinimumIPTarget:                  strconv.Itoa(IPv4PDDefaultMinIPTargetSize),
			WarmPrefixTarget:                 strconv.Itoa(IPv4PDDefaultWarmPrefixTargetSize),
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, IPv4PDDefaultWarmIPTargetSize, warmIPTarget)
	assert.Equal(t, IPv4PDDefaultMinIPTargetSize, minIPTarget)
	assert.Equal(t, IPv4PDDefaultWarmPrefixTargetSize, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

func TestParseWinIPTargetConfigs_PDDisabledWithAllZeroTargets(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "false",
			WarmIPTarget:                     strconv.Itoa(0),
			MinimumIPTarget:                  strconv.Itoa(0),
		},
	}
	warmIPTarget, minIPTarget, _, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, 1, warmIPTarget)
	assert.Equal(t, 0, minIPTarget)
	assert.Equal(t, false, isPDEnabled)
}

func TestParseWinIPTargetConfigs_PDEnabledWithAllZeroTargets(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     strconv.Itoa(0),
			MinimumIPTarget:                  strconv.Itoa(0),
			WarmPrefixTarget:                 strconv.Itoa(0),
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, IPv4PDDefaultWarmIPTargetSize, warmIPTarget)
	assert.Equal(t, IPv4PDDefaultMinIPTargetSize, minIPTarget)
	assert.Equal(t, IPv4PDDefaultWarmPrefixTargetSize, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

func TestParseWinIPTargetConfigs_PDDisabledWithDefaultTargets(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	expectedWarmIPTarget := IPv4DefaultWinWarmIPTarget
	expectedMinIPTarget := IPv4DefaultWinMinIPTarget
	expectedWarmPrefixTarget := 0
	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "false",
			WarmIPTarget:                     strconv.Itoa(expectedWarmIPTarget),
			MinimumIPTarget:                  strconv.Itoa(expectedMinIPTarget),
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, expectedWarmIPTarget, warmIPTarget)
	assert.Equal(t, expectedMinIPTarget, minIPTarget)
	assert.Equal(t, expectedWarmPrefixTarget, warmPrefixTarget)
	assert.Equal(t, false, isPDEnabled)
}

func TestParseWinIPTargetConfigs_PDDisabledAndInvalidConfig_ReturnsDefault(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "false",
			WarmIPTarget:                     "Invalid string",
			MinimumIPTarget:                  "Invalid string",
		},
	}

	warmIPTarget, minIPTarget, _, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.False(t, isPDEnabled)
	assert.Equal(t, IPv4DefaultWinWarmIPTarget, warmIPTarget)
	assert.Equal(t, IPv4DefaultWinMinIPTarget, minIPTarget)
}

// negative values are still read in but processed accordingly when it's used in the warm pool
func TestParseWinIPTargetConfigs_PDDisabledAndNegativeConfig_ReturnsOriginal(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "false",
			WarmIPTarget:                     strconv.Itoa(-5),
			MinimumIPTarget:                  strconv.Itoa(-5),
		},
	}

	warmIPTarget, minIPTarget, _, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.False(t, isPDEnabled)
	assert.Equal(t, -5, warmIPTarget)
	assert.Equal(t, -5, minIPTarget)
}

// TestParseWinPDTargets parses prefix delegation configurations with negative values and returns the same
func TestParseWinIPTargetConfigs_PDEnabled_Negative(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     strconv.Itoa(-10),
			MinimumIPTarget:                  strconv.Itoa(-100),
			WarmPrefixTarget:                 strconv.Itoa(0),
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	// negative values are still read in but processed when it's used in the warm pool
	assert.Equal(t, -10, warmIPTarget)
	assert.Equal(t, -100, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

func TestParseWinIPTargetConfigs_OnlyWindowsIPAM_EffectsDefaults(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey: "true",
		},
	}

	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, IPv4DefaultWinWarmIPTarget, warmIPTarget)
	assert.Equal(t, IPv4DefaultWinMinIPTarget, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
	assert.Equal(t, false, isPDEnabled)
}

func TestParseWinIPTargetConfigs_PartiallyEmptyTargets_EffectsSpecified(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			WinWarmIPTarget: "1",
		},
	}

	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, 1, warmIPTarget)
	assert.Equal(t, 0, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
	assert.Equal(t, false, isPDEnabled)
}

// TestParseWinIPTargetConfigs_EmptyConfigMap_ReturnsDefaultsWithSecondaryIP parses configurations with empty configmap
func TestParseWinIPTargetConfigs_EmptyConfigMap_ReturnsDefaultsWithSecondaryIP(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, IPv4DefaultWinWarmIPTarget, warmIPTarget)
	assert.Equal(t, IPv4DefaultWinMinIPTarget, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
	assert.Equal(t, false, isPDEnabled)
}

// TestParseWinIPTargetConfigs_PDEnabled_Invalid parses prefix delegation configurations with invalid values and returns 0s as targets
func TestParseWinIPTargetConfigs_PDEnabled_Invalid(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     "random",
			MinimumIPTarget:                  "string val",
			WarmPrefixTarget:                 "can't parse",
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, IPv4PDDefaultWarmIPTargetSize, warmIPTarget)
	assert.Equal(t, IPv4PDDefaultMinIPTargetSize, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

// TestParseWinIPTargetConfigs_PDEnabledAndWarmPrefixInvalid parses prefix delegation configurations with warm prefix being invalid
func TestParseWinIPTargetConfigs_PDEnabledAndWarmPrefixInvalid(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     "2",
			MinimumIPTarget:                  "2",
			WarmPrefixTarget:                 "invalid value",
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, 2, warmIPTarget)
	assert.Equal(t, 2, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

// TestParseWinIPTargetConfigs_PDEnabledAndWarmAndMinimumIPInvalid parses prefix delegation configurations with only warm prefix being valid
func TestParseWinIPTargetConfigs_PDEnabledAndWarmAndMinimumIPInvalid(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     "invalid value",
			MinimumIPTarget:                  "invalid value",
			WarmPrefixTarget:                 "1",
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, 0, warmIPTarget)
	assert.Equal(t, 0, minIPTarget)
	assert.Equal(t, 1, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

// TestParseWinIPTargetConfigs_PDEnabledAndWarmAndOnlyWarmPrefixSet parses prefix delegation configurations with only warm prefix set
func TestParseWinIPTargetConfigs_PDEnabledAndWarmAndOnlyWarmPrefixSet(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmPrefixTarget:                 "1",
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, 0, warmIPTarget)
	assert.Equal(t, 0, minIPTarget)
	assert.Equal(t, 1, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

// TestParseWinIPTargetConfigs_PDEnabledAndWarmAndOnlyWarmPrefixNotSet parses prefix delegation configurations with only warm prefix not set
func TestParseWinIPTargetConfigs_PDEnabledAndWarmAndOnlyWarmPrefixNotSet(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

	vpcCNIConfig := &v1.ConfigMap{
		Data: map[string]string{
			EnableWindowsIPAMKey:             "true",
			EnableWindowsPrefixDelegationKey: "true",
			WarmIPTarget:                     "2",
			MinimumIPTarget:                  "2",
		},
	}
	warmIPTarget, minIPTarget, warmPrefixTarget, isPDEnabled := ParseWinIPTargetConfigs(log, vpcCNIConfig)
	assert.Equal(t, 2, warmIPTarget)
	assert.Equal(t, 2, minIPTarget)
	assert.Equal(t, 0, warmPrefixTarget)
	assert.Equal(t, true, isPDEnabled)
}

// TestLoadResourceConfigFromConfigMap tests the custom configuration for PD is loaded correctly from config map
func TestLoadResourceConfigFromConfigMap(t *testing.T) {
	log := zap.New(zap.UseDevMode(true)).WithName("loader test")

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

	resourceConfig := LoadResourceConfigFromConfigMap(log, vpcCNIConfig)

	// Verify default resource configuration for resource Pod ENI
	podENIConfig := resourceConfig[ResourceNamePodENI]
	assert.Equal(t, ResourceNamePodENI, podENIConfig.Name)
	assert.Equal(t, PodENIDefaultWorker, podENIConfig.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: true, OSWindows: false}, podENIConfig.SupportedOS)
	assert.Nil(t, podENIConfig.WarmPoolConfig)

	// Verify default resource configuration for resource IPv4 Address
	ipV4Config := resourceConfig[ResourceNameIPAddress]
	assert.Equal(t, ResourceNameIPAddress, ipV4Config.Name)
	assert.Equal(t, IPv4DefaultWinWorkerCount, ipV4Config.WorkerCount)
	assert.Equal(t, map[string]bool{OSLinux: false, OSWindows: true}, ipV4Config.SupportedOS)

	// Verify default Warm pool configuration for IPv4 Address
	ipV4WPConfig := ipV4Config.WarmPoolConfig
	assert.Equal(t, IPv4DefaultWinWarmIPTarget, ipV4WPConfig.DesiredSize)
	assert.Equal(t, IPv4DefaultWinMaxDev, ipV4WPConfig.MaxDeviation)
	assert.Equal(t, IPv4DefaultWinResSize, ipV4WPConfig.ReservedSize)

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
