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
	// TODO: Should we always do this max retry no matter why it fails
	// such deleted pods will also be retried 5 times, which could be an issue for large pods loads and high churning rate.
	WorkQueueDefaultMaxRetries = 5

	// Default Configuration for Pod ENI resource type
	PodENIDefaultWorker = 7

	// Default Configuration for IPv4 resource type
	IPv4DefaultWorker  = 2
	IPv4DefaultWPSize  = 3
	IPv4DefaultMaxDev  = 1
	IPv4DefaultResSize = 0

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
