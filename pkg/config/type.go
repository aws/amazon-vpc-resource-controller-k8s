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
	"time"
)

// TODO: Find more appropriate package for the labels and annotation constants.

// K8s Pod Annotations
const (
	// VPCResourcePrefix is the common prefix for all VPC extended resources
	VPCResourcePrefix = "vpc.amazonaws.com/"
	// ResourceNamePodENI is the extended resource name for Branch ENIs
	ResourceNamePodENI = VPCResourcePrefix + "pod-eni"
	// ResourceNameIPAddress is the extended resource name for private IP addresses
	ResourceNameIPAddress = VPCResourcePrefix + "PrivateIPv4Address"
	// ResourceNameIPAddressFromPrefix is the resource name for prefix-deconstructed IP addresses, not a pod annotation
	ResourceNameIPAddressFromPrefix = VPCResourcePrefix + "PrivateIPv4AddressFromPrefix"
)

// K8s Labels
const (
	// ControllerName is the name of the VPC Resource Controller
	ControllerName = "vpc-resource-controller"
	// HasTrunkAttachedLabel is the label denoting that the trunk ENI is attached to node or not
	HasTrunkAttachedLabel = "vpc.amazonaws.com/has-trunk-attached"
	// CustomNetworkingLabel is the label with the name of ENIConfig to be used by the node for custom networking
	CustomNetworkingLabel = "vpc.amazonaws.com/eniConfig"
	// Trunk attaching status value
	BooleanTrue         = "true"
	BooleanFalse        = "false"
	NotSupportedEc2Type = "not-supported"
	// NodeLabelOS is the Kubernetes Operating System label
	NodeLabelOS = "kubernetes.io/os"
	// NodeLabelOS is the Kubernetes Operating System label used before k8s version 1.16
	NodeLabelOSBeta = "beta.kubernetes.io/os"
	// OSWindows is the the windows Operating System
	OSWindows = "windows"
	// OSLinux is the the linux Operating System
	OSLinux = "linux"
	// Node termination finalizer on CNINode CRD
	NodeTerminationFinalizer = "networking.k8s.aws/resource-cleanup"
)

// EC2 Tags
const (
	ControllerTagPrefix                 = "vpcresources.k8s.aws/"
	VLandIDTag                          = ControllerTagPrefix + "vlan-id"
	TrunkENIIDTag                       = ControllerTagPrefix + "trunk-eni-id"
	VPCRCClusterNameTagKeyFormat        = "kubernetes.io/cluster/%s"
	VPCRCClusterNameTagValue            = "owned"
	NetworkInterfaceOwnerTagKey         = "eks:eni:owner"
	NetworkInterfaceOwnerTagValue       = "eks-vpc-resource-controller"
	NetworkInterfaceOwnerVPCCNITagValue = "amazon-vpc-cni"
	NetworkInterfaceNodeIDKey           = "node.k8s.amazonaws.com/instance_id"
	VPCCNIClusterNameKey                = "cluster.k8s.amazonaws.com/name"
)

const (
	LeaderElectionKey                = "cp-vpc-resource-controller"
	LeaderElectionNamespace          = "kube-system"
	VpcCniConfigMapName              = "amazon-vpc-cni"
	EnableWindowsIPAMKey             = "enable-windows-ipam"
	EnableWindowsPrefixDelegationKey = "enable-windows-prefix-delegation"
	// TODO: we will deprecate the confusing naming of Windows flags eventually
	WarmPrefixTarget = "warm-prefix-target"
	WarmIPTarget     = "warm-ip-target"
	MinimumIPTarget  = "minimum-ip-target"
	// these windows prefixed flags will be used for Windows support only eventully
	WinWarmPrefixTarget = "windows-warm-prefix-target"
	WinWarmIPTarget     = "windows-warm-ip-target"
	WinMinimumIPTarget  = "windows-minimum-ip-target"
	// Since LeaderElectionNamespace and VpcCniConfigMapName may be different in the future
	KubeSystemNamespace            = "kube-system"
	VpcCNIDaemonSetName            = "aws-node"
	OldVPCControllerDeploymentName = "vpc-resource-controller"
	BranchENICooldownPeriodKey     = "branch-eni-cooldown"
	// DescribeNetworkInterfacesMaxResults defines the max number of requests to return for DescribeNetworkInterfaces API call
	DescribeNetworkInterfacesMaxResults = int64(1000)
)

type ResourceType string

const (
	ResourceTypeIPv4Address ResourceType = "IPv4Address"
	ResourceTypeIPv4Prefix  ResourceType = "IPv4Prefix"
)

// IPResourceCount contains the arguments for number of IPv4 resources to request
type IPResourceCount struct {
	SecondaryIPv4Count int
	IPv4PrefixCount    int
	SecondaryIPv6Count int
	IPv6PrefixCount    int
}

// Events metadata
// They are used to identify valid events emitted from authorized agents
const (
	VpcCNINodeEventReason             = "AwsNodeNotificationToRc"
	VpcCNIReportingAgent              = "aws-node"
	VpcCNINodeEventActionForTrunk     = "NeedTrunk"
	VpcCNINodeEventActionForEniConfig = "NeedEniConfig"
	TrunkNotAttached                  = "vpc.amazonaws.com/has-trunk-attached=false"
	TrunkAttached                     = "vpc.amazonaws.com/has-trunk-attached=true"
)

// customized configurations for BigCache
const (
	InstancesCacheTTL      = 30 * time.Minute // scaling < 1k nodes should be under 20 minutes
	InstancesCacheShards   = 32               // must be power of 2
	InstancesCacheMaxSize  = 2                // in MB
	NodeTerminationTimeout = 3 * time.Minute
)

var (
	// CoolDownPeriod is the time to let kube-proxy propagates IP tables rules before assigning the resource back to new pod
	CoolDownPeriod = time.Second * 30
	// ENICleanUpInterval is the time interval between each dangling ENI clean up task
	ENICleanUpInterval = time.Minute * 30
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
	// TODO: Deprecate DesiredSize in favour of using WarmIPTarget since historically they served the same purpose
	// Number of resources to keep in warm pool per node; for prefix IP pool, this is used to check if pool is active
	DesiredSize int
	// Number of resources not to use in the warm pool
	ReservedSize int
	// The maximum number by which the warm pool can deviate from the desired size
	MaxDeviation int
	// The number of IPs to be available in prefix IP pool
	WarmIPTarget int
	// The floor of number of IPs to be stored in prefix IP pool
	MinIPTarget int
	// The number of prefixes to be available in prefix IP pool
	WarmPrefixTarget int
}
