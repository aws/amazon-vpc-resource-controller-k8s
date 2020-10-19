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
	"testing"

	"github.com/stretchr/testify/assert"
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

}
