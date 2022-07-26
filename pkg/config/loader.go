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

const (
	WorkQueueDefaultMaxRetries = 5

	// Default Configuration for Pod ENI resource type
	PodENIDefaultWorker = 2

	// Default Configuration for IPv4 resource type
	IPv4DefaultWorker  = 2
	IPv4DefaultWPSize  = 3
	IPv4DefaultMaxDev  = 1
	IPv4DefaultResSize = 0

	// Default Configuration for IPv4 prefix resource type
	IPv4PrefixDefaultWorker  = 2
	IPv4PrefixDefaultWPSize  = 16
	IPv4PrefixDefaultMaxDev  = 13
	IPv4PrefixDefaultResSize = 0

	// EC2 API QPS for user service client
	UserServiceClientQPS      = 6
	UserServiceClientQPSBurst = 8

	// EC2 API QPS for instance service client
	InstanceServiceClientQPS   = 2
	InstanceServiceClientBurst = 3

	// API Server QPS
	DefaultAPIServerQPS   = 10
	DefaultAPIServerBurst = 15
)

// LoadResourceConfig returns the Resource Configuration for all resources managed by the VPC Resource Controller. Currently
// returns the default resource configuration and later can return the configuration from a ConfigMap.
func LoadResourceConfig() map[string]ResourceConfig {
	return getIpamResourceConfig()
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

	return config
}


func getIpamResourceConfig() map[string]ResourceConfig {
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
		DesiredSize:  IPv4PrefixDefaultWPSize,
		MaxDeviation: IPv4PrefixDefaultMaxDev,
		ReservedSize: IPv4PrefixDefaultResSize,
	}
	ipV4Config := ResourceConfig{
		Name:           ResourceNameIPAddress,
		WorkerCount:    IPv4DefaultWorker,
		SupportedOS:    map[string]bool{OSWindows: true, OSLinux: false},
		WarmPoolConfig: &ipV4WarmPoolConfig,
	}
	config[ResourceNameIPAddress] = ipV4Config

	return config
}