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

const (
	// CNINodeStatusSnapshotVersion is the version of the controller-owned status snapshot.
	CNINodeStatusSnapshotVersion = "v1"
)

// Feature is a type of feature being supported by VPC resource controller and other AWS Services
type Feature struct {
	Name  FeatureName `json:"name,omitempty"`
	Value string      `json:"value,omitempty"`
}

// Important: Run "make" to regenerate code after modifying this file
// CNINodeSpec defines the desired state of CNINode
type CNINodeSpec struct {
	Features []Feature `json:"features,omitempty"`
	// Additional tag key/value added to all network interfaces provisioned by the vpc-resource-controller and VPC-CNI
	Tags map[string]string `json:"tags,omitempty"`
}

// CNINodeStatus defines the managed VPC resources.
type CNINodeStatus struct {
	SnapshotVersion string         `json:"snapshotVersion,omitempty"`
	LastUpdated     metav1.Time    `json:"lastUpdated,omitempty"`
	Instance        InstanceStatus `json:"instance,omitempty"`
	TrunkENI        TrunkENIStatus `json:"trunkENI,omitempty"`
}

// InstanceStatus stores the EC2 instance fields needed to reinitialize
// resource providers without synchronously describing the instance on restart.
type InstanceStatus struct {
	InstanceID                            string                    `json:"instanceID,omitempty"`
	InstanceType                          string                    `json:"instanceType,omitempty"`
	InstanceSubnetID                      string                    `json:"instanceSubnetID,omitempty"`
	InstanceSubnetCIDRBlock               string                    `json:"instanceSubnetCIDRBlock,omitempty"`
	InstanceSubnetV6CIDRBlock             string                    `json:"instanceSubnetV6CIDRBlock,omitempty"`
	CurrentSubnetID                       string                    `json:"currentSubnetID,omitempty"`
	CurrentSubnetCIDRBlock                string                    `json:"currentSubnetCIDRBlock,omitempty"`
	CurrentSubnetV6CIDRBlock              string                    `json:"currentSubnetV6CIDRBlock,omitempty"`
	CurrentInstanceSecurityGroups         []string                  `json:"currentInstanceSecurityGroups,omitempty"`
	SubnetMask                            string                    `json:"subnetMask,omitempty"`
	SubnetV6Mask                          string                    `json:"subnetV6Mask,omitempty"`
	PrimaryNetworkInterfaceID             string                    `json:"primaryNetworkInterfaceID,omitempty"`
	PrimaryNetworkInterfaceSecurityGroups []string                  `json:"primaryNetworkInterfaceSecurityGroups,omitempty"`
	ConnectionTracking                    *ConnectionTrackingStatus `json:"connectionTracking,omitempty"`
}

// ConnectionTrackingStatus stores primary ENI connection tracking settings.
type ConnectionTrackingStatus struct {
	TCPEstablishedTimeout *int32 `json:"tcpEstablishedTimeout,omitempty"`
	UDPStreamTimeout      *int32 `json:"udpStreamTimeout,omitempty"`
	UDPTimeout            *int32 `json:"udpTimeout,omitempty"`
}

// TrunkENIStatus stores the trunk ENI fields needed to rebuild the in-memory trunk cache.
type TrunkENIStatus struct {
	ID             string   `json:"id,omitempty"`
	SubnetID       string   `json:"subnetID,omitempty"`
	SecurityGroups []string `json:"securityGroups,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Features",type=string,JSONPath=`.spec.features`,description="The features delegated to VPC resource controller"
// +kubebuilder:resource:shortName=cnd,scope=Cluster
// +kubebuilder:subresource:status

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
