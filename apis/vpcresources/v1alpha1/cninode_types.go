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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// FeatureName is a type of feature name supported by AWS VPC CNI. It can be Security Group for Pods, custom networking, or others
type FeatureName string

const (
	SecurityGroupsForPods FeatureName = "SecurityGroupsForPods"
	CustomNetworking      FeatureName = "CustomNetworking"
)

// Feature is a type of feature being supported by VPC resource controller and other AWS Services
type Feature struct {
	Name  FeatureName `json:"name,omitempty"`
	Value string      `json:"value,omitempty"`
}

// CNINodeManager identifies the controller responsible for a CNINode object.
type CNINodeManager string

const (
	// ManagedByVPCResourceController is the default manager: the
	// vpc-resource-controller creates and reconciles CNINodes for
	// standard (non-auto) nodes. An empty managedBy field means the same.
	ManagedByVPCResourceController CNINodeManager = "vpc-resource-controller"
	// ManagedByEKSAutoMode marks CNINodes created and reconciled by the
	// EKS Auto Mode networking controllers. The vpc-resource-controller
	// must not reconcile or garbage-collect these objects.
	ManagedByEKSAutoMode CNINodeManager = "eks-auto-mode"
)

// Important: Run "make" to regenerate code after modifying this file
// CNINodeSpec defines the desired state of CNINode
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.managedBy) || has(self.managedBy)",message="managedBy cannot be removed once set"
type CNINodeSpec struct {
	Features []Feature `json:"features,omitempty"`
	// Additional tag key/value added to all network interfaces provisioned by the vpc-resource-controller and VPC-CNI
	Tags map[string]string `json:"tags,omitempty"`
	// ManagedBy identifies the controller that owns this CNINode and its
	// status. Empty is equivalent to "vpc-resource-controller" for backward
	// compatibility with objects created before this field existed.
	// Controllers must ignore CNINodes managed by another controller.
	// Immutable once set: a node can never legitimately change between
	// compute types, so a managedBy flip is always a bug or tampering.
	// Setting it on an existing object that has never had a value remains
	// allowed (adoption of pre-existing objects).
	// +optional
	// +kubebuilder:validation:Enum=vpc-resource-controller;eks-auto-mode
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="managedBy is immutable once set"
	ManagedBy CNINodeManager `json:"managedBy,omitempty"`
}

// CNINodeStatus defines the managed VPC resources.
type CNINodeStatus struct {
	// TrunkInterface describes the trunk network interface attached to the
	// node and the branch interfaces associated with it, persisted by the
	// managing controller (see spec.managedBy) for visibility and recovery.
	// +optional
	TrunkInterface *TrunkInterface `json:"trunkInterface,omitempty"`
	// InstanceID is the EC2 instance id of the node this CNINode corresponds to.
	// +optional
	// +kubebuilder:validation:MaxLength=19
	// +kubebuilder:validation:Pattern=`^i-([0-9a-f]{8}|[0-9a-f]{17})$`
	InstanceID string `json:"instanceID,omitempty"`
	// InstanceType is the EC2 instance type of the node, used to size branch ENI capacity.
	// +optional
	InstanceType string `json:"instanceType,omitempty"`
	// SecurityGroups are the instance's default security group ids, applied to a
	// branch ENI when the pod does not specify its own via a SecurityGroupPolicy.
	// +optional
	// +listType=atomic
	SecurityGroups []string `json:"securityGroups,omitempty"`
	// ConnectionTracking holds the connection tracking timeouts inherited from
	// the instance's primary ENI and applied to branch ENIs. Persisted so the
	// value survives a controller restart without a DescribeInstances call; a
	// nil value means branch ENIs use the EC2 default connection tracking.
	// +optional
	ConnectionTracking *ConnectionTrackingConfig `json:"connectionTracking,omitempty"`
}

// ConnectionTrackingConfig holds the idle timeouts (in seconds) EC2 uses for
// connection tracking on an ENI, mirrored from the instance's primary ENI so
// branch ENIs inherit the same behavior. A nil pointer field means the value
// was not configured and the EC2 default applies.
type ConnectionTrackingConfig struct {
	// TCPEstablishedTimeout is the timeout for established TCP connections.
	// +optional
	TCPEstablishedTimeout *int32 `json:"tcpEstablishedTimeout,omitempty"`
	// UDPStreamTimeout is the timeout for UDP flows classified as streams.
	// +optional
	UDPStreamTimeout *int32 `json:"udpStreamTimeout,omitempty"`
	// UDPTimeout is the timeout for idle UDP flows.
	// +optional
	UDPTimeout *int32 `json:"udpTimeout,omitempty"`
}

// TrunkInterface describes a trunk ENI and its associated branch ENIs.
type TrunkInterface struct {
	// ID is the EC2 network interface id of the trunk ENI.
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
	// MacAddress is the MAC address of the trunk ENI.
	// +optional
	MacAddress string `json:"macAddress,omitempty"`
	// DeviceIndex is the attachment device index of the trunk ENI on the instance.
	// +optional
	DeviceIndex int32 `json:"deviceIndex,omitempty"`
	// SubnetID is the id of the EC2 subnet the trunk ENI resides in, which is
	// also the subnet its branch ENIs are created in. Persisted with the trunk
	// so a controller can create a branch ENI straight from the CNINode,
	// without a DescribeNetworkInterfaces call on the pod path, and so the
	// value survives a controller restart that drops in-memory node state.
	// +optional
	// +kubebuilder:validation:MaxLength=24
	// +kubebuilder:validation:Pattern=`^subnet-([0-9a-f]{8}|[0-9a-f]{17})$`
	SubnetID string `json:"subnetID,omitempty"`
	// SubnetCIDR is the IPv4 CIDR block of the trunk ENI's subnet.
	// +optional
	SubnetCIDR string `json:"subnetCIDR,omitempty"`
	// SubnetV6CIDR is the IPv6 CIDR block of the trunk ENI's subnet, if present.
	// +optional
	SubnetV6CIDR string `json:"subnetV6CIDR,omitempty"`
	// Branches are the branch ENIs associated with this trunk ENI.
	// Listed as a map keyed by id so distinct field managers can own
	// individual entries under Server-Side Apply without conflicting.
	// +optional
	// +listType=map
	// +listMapKey=id
	Branches []BranchInterface `json:"branches,omitempty"`
}

// BranchInterface describes a branch ENI associated with a trunk ENI.
type BranchInterface struct {
	// ID is the EC2 network interface id of the branch ENI.
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
	// VlanID is the vlan id of the trunk-to-branch association.
	VlanID int32 `json:"vlanID"`
	// AssociationID is the id of the association between the branch and trunk ENI.
	// +optional
	AssociationID string `json:"associationID,omitempty"`
	// MacAddress is the MAC address of the branch ENI.
	// +optional
	MacAddress string `json:"macAddress,omitempty"`
	// SubnetCIDR is the IPv4 CIDR block of the subnet the branch ENI belongs to.
	// +optional
	SubnetCIDR string `json:"subnetCIDR,omitempty"`
	// SubnetV6CIDR is the IPv6 CIDR block of the subnet the branch ENI belongs to.
	// +optional
	SubnetV6CIDR string `json:"subnetV6CIDR,omitempty"`
	// SecurityGroups are the security group ids attached to the branch ENI.
	// +optional
	// +listType=atomic
	SecurityGroups []string `json:"securityGroups,omitempty"`
	// IPv4CIDRs are the IPv4 addresses or prefixes assigned to the branch ENI,
	// in CIDR notation (a single address is expressed as a /32).
	// +optional
	// +listType=atomic
	IPv4CIDRs []string `json:"ipv4CIDRs,omitempty"`
	// IPv6CIDRs are the IPv6 addresses or prefixes assigned to the branch ENI,
	// in CIDR notation (a single address is expressed as a /128).
	// +optional
	// +listType=atomic
	IPv6CIDRs []string `json:"ipv6CIDRs,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:selectablefield:JSONPath=`.spec.managedBy`
// +kubebuilder:printcolumn:name="Features",type=string,JSONPath=`.spec.features`,description="The features delegated to VPC resource controller"
// +kubebuilder:resource:shortName=cnd,scope=Cluster

// +kubebuilder:object:root=true
type CNINode struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CNINodeSpec   `json:"spec,omitempty"`
	Status            CNINodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// CNINodeList contains a list of CNINodeList
type CNINodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CNINode `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CNINode{}, &CNINodeList{})
}
