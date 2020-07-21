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

import "time"

// TODO: Find more appropriate package for the labels and annotation constants.

// K8s Pod Annotations
const (
	// VPCResourcePrefix is the common prefix for all VPC extended resources
	VPCResourcePrefix = "vpc.amazonaws.com/"
	// ResourceNamePodENI is the extended resource name for Branch ENIs
	ResourceNamePodENI = VPCResourcePrefix + "pod-eni"
	// ResourceNameIPAddress is the extended resource name for private IP addresses
	ResourceNameIPAddress = VPCResourcePrefix + "PrivateIPv4Address"
)

// K8s Pod Labels
const (
	// VPCManager is the node label to identify the manager of VPC Resources
	VPCManagerLabel = "eks.amazonaws.com/vpc-resource-manager"
	// VPCManagedBy if set enables the controller to manage the VPC Resources
	VPCManagedBy = "controller"
	// HasTrunkAttachedLabel is the label denoting that the trunk ENI is attached to node or not
	HasTrunkAttachedLabel = "vpc.amazonaws.com/has-trunk-attached"
	// CustomNetworkingLabel is the label with the name of ENIConfig to be used by the node for custom networking
	CustomNetworkingLabel = "k8s.amazonaws.com/eniConfig"
	// NodeLabelOS is the Kubernetes Operating System label
	NodeLabelOS = "kubernetes.io/os"
	// NodeLabelOS is the Kubernetes Operating System label used before k8s version 1.16
	NodeLabelOSBeta = "beta.kubernetes.io/os"
	// OSWindows is the the windows Operating System
	OSWindows = "windows"
	// OSLinux is the the linux Operating System
	OSLinux = "linux"
)

// EC2 Tags
const (
	VLandIDTag    = "vpcresources.k8s.aws/vlan_id"
	TrunkENIIDTag = "vpcresources.k8s.aws/trunk_eni_id"
)

var (
	// CoolDownPeriod is the time to let kube-proxy propagates IP tables rules before assigning the resource back to new pod
	CoolDownPeriod = time.Second * 30
)

// ResourceConfig is the configuration for each resource type
type ResourceConfig struct {
	// Name is the unique name of the resource
	Name string
	// WorkerCount is the number of routines that will process items for the buffer
	WorkerCount int
	// SupportedOS is the map of operating system that supports the resource
	SupportedOS map[string]bool
	// WarmPoolConfig represents the configuration of warm pool for resources that support warm resources. Optional
	WarmPoolConfig *WarmPoolConfig
}

// WarmPoolConfig is the configuration of Warm Pool of a resource
type WarmPoolConfig struct {
	// Number of resources to keep in warm pool per node
	DesiredSize int
	// Number of resources not to use in the warm pool
	ReservedSize int
	// The maximum number by which the warm pool can deviate from the desired size
	MaxDeviation int
}
