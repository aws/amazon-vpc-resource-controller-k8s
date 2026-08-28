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
	// This is the layer shared with other CNINode status writers (e.g. the
	// EKS Auto Mode networking controllers, which additionally populate
	// Branches); it retains its original observed-resource semantics.
	// +optional
	TrunkInterface *TrunkInterface `json:"trunkInterface,omitempty"`
	// ReinitCheckpoint is the vpc-resource-controller-private restart-recovery
	// checkpoint: the instance fields needed to rebuild in-memory state on a
	// controller restart or leader change without synchronous EC2 describes.
	// +optional
	ReinitCheckpoint *ReinitCheckpoint `json:"reinitCheckpoint,omitempty"`
}

// ReinitCheckpoint is the controller-private snapshot used by the zero-EC2
// re-init (hydrate) path. It is self-contained: it carries the trunk ENI id
// plus the instance's source-of-truth values (its own subnet, primary ENI
// security groups) and the derived effective (current*) values, so a restart
// restores the full in-memory state with no EC2 call, including for custom
// networking nodes. EC2 remains the source of truth: any missing or invalid
// field is a checkpoint miss and the controller falls back to describing the
// instance from EC2.
type ReinitCheckpoint struct {
	// TrunkENIID is the trunk ENI the branch ledger is rebuilt against.
	// +optional
	TrunkENIID string `json:"trunkENIID,omitempty"`
	// InstanceID is the EC2 instance id this checkpoint belongs to; hydrate
	// rejects the checkpoint if it does not match the live node (a reused name).
	// +optional
	// +kubebuilder:validation:MaxLength=19
	// +kubebuilder:validation:Pattern=`^i-([0-9a-f]{8}|[0-9a-f]{17})$`
	InstanceID string `json:"instanceID,omitempty"`
	// InstanceType is the EC2 instance type of the node, used to size branch ENI capacity.
	// +optional
	InstanceType string `json:"instanceType,omitempty"`
	// InstanceSubnetID is the id of the instance's own subnet.
	// +optional
	InstanceSubnetID string `json:"instanceSubnetID,omitempty"`
	// InstanceSubnetCIDRBlock is the IPv4 CIDR block of the instance's own subnet.
	// +optional
	InstanceSubnetCIDRBlock string `json:"instanceSubnetCIDRBlock,omitempty"`
	// InstanceSubnetV6CIDRBlock is the IPv6 CIDR block of the instance's own subnet, if present.
	// +optional
	InstanceSubnetV6CIDRBlock string `json:"instanceSubnetV6CIDRBlock,omitempty"`
	// CurrentSubnetID is the effective subnet id used for branch ENIs (the
	// instance subnet, or the ENIConfig subnet under custom networking).
	// +optional
	CurrentSubnetID string `json:"currentSubnetID,omitempty"`
	// CurrentSubnetCIDRBlock is the effective IPv4 CIDR block for branch ENIs.
	// +optional
	CurrentSubnetCIDRBlock string `json:"currentSubnetCIDRBlock,omitempty"`
	// CurrentSubnetV6CIDRBlock is the effective IPv6 CIDR block for branch ENIs.
	// +optional
	CurrentSubnetV6CIDRBlock string `json:"currentSubnetV6CIDRBlock,omitempty"`
	// CurrentInstanceSecurityGroups are the effective default security groups
	// applied to a branch ENI when the pod does not specify its own.
	// +optional
	// +listType=atomic
	CurrentInstanceSecurityGroups []string `json:"currentInstanceSecurityGroups,omitempty"`
	// PrimaryNetworkInterfaceSecurityGroups are the security groups of the
	// instance's primary ENI (the source-of-truth value).
	// +optional
	// +listType=atomic
	PrimaryNetworkInterfaceSecurityGroups []string `json:"primaryNetworkInterfaceSecurityGroups,omitempty"`
	// ConnectionTracking holds the connection tracking timeouts inherited from
	// the instance's primary ENI and applied to branch ENIs. A nil value means
	// branch ENIs use the EC2 default connection tracking.
	// +optional
	ConnectionTracking *ConnectionTrackingStatus `json:"connectionTracking,omitempty"`
}

// ConnectionTrackingStatus holds the idle timeouts (in seconds) EC2 uses for
// connection tracking on an ENI, mirrored from the instance's primary ENI so
// branch ENIs inherit the same behavior. A nil pointer field means the value
// was not configured and the EC2 default applies.
type ConnectionTrackingStatus struct {
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
	// SubnetID is the id of the subnet the trunk ENI belongs to. The
	// vpc-resource-controller's hydrate path validates it against the
	// checkpoint's effective subnet to reject a stale snapshot.
	// +optional
	// +kubebuilder:validation:MaxLength=24
	// +kubebuilder:validation:Pattern=`^subnet-([0-9a-f]{8}|[0-9a-f]{17})$`
	SubnetID string `json:"subnetID,omitempty"`
	// MacAddress is the MAC address of the trunk ENI.
	// +optional
	MacAddress string `json:"macAddress,omitempty"`
	// DeviceIndex is the attachment device index of the trunk ENI on the instance.
	// +optional
	DeviceIndex int32 `json:"deviceIndex,omitempty"`
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

// IsManagedByVPCResourceController reports whether this CNINode, including its
// status, is owned by the vpc-resource-controller. An empty managedBy means yes,
// for objects created before the field existed. Exactly one controller owns an
// object's status, so anything else must be left alone.
func (c *CNINode) IsManagedByVPCResourceController() bool {
	return c.Spec.ManagedBy == "" || c.Spec.ManagedBy == ManagedByVPCResourceController
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
