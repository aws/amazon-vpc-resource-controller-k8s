/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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

	DefaultEC2APIQPS   = 6
	DefaultEC2APIBurst = 8
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
