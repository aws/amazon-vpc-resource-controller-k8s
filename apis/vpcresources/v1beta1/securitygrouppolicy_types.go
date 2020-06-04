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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Important: Run "make" to regenerate code after modifying this file

// SecurityGroupPolicySpec defines the desired state of SecurityGroupPolicy
type SecurityGroupPolicySpec struct {
	PodSelector            LabelSelector          `json:"podSelector,omitempty"`
	ServiceAccountSelector ServiceAccountSelector `json:"serviceAccountSelector,omitempty"`
	SecurityGroups         GroupIds               `json:"securityGroups,omitempty"`
}

// +kubebuilder:validation:Pattern="^In|NotIn|Exists|DoesNotExist$"
type LabelSelectorOperator string

type GroupIds struct {
	// Groups is the list of EC2 Security Groups Ids that need to be applied to the ENI of a Pod.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=5
	Groups []string `json:"groupIds,omitempty"`
}

// Copied from https://github.com/kubernetes/apimachinery/blob/master/pkg/apis/meta/v1/types.go instead of directly
// using it as we need to inject kubebuilder annotations else our CRD will be re generated without fields like Pattern
// every time we run make manifests.

// A label selector is a label query over a set of resources. The result of matchLabels and
// matchExpressions are ANDed. An empty label selector matches all objects. A null
// label selector matches no objects.
type LabelSelector struct {
	// matchLabels is a map of {key,value} pairs. A single {key,value} in the matchLabels
	// map is equivalent to an element of matchExpressions, whose key field is "key", the
	// operator is "In", and the values array contains only "value". The requirements are ANDed.
	// +optional
	MatchLabels map[string]string `json:"matchLabels,omitempty" protobuf:"bytes,1,rep,name=matchLabels"`
	// matchExpressions is a list of label selector requirements. The requirements are ANDed.
	// +optional
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty" protobuf:"bytes,2,rep,name=matchExpressions"`
}

// A label selector requirement is a selector that contains values, a key, and an operator that
// relates the key and values.
type LabelSelectorRequirement struct {
	// key is the label key that the selector applies to.
	// +patchMergeKey=key
	// +patchStrategy=merge
	Key string `json:"key" patchStrategy:"merge" patchMergeKey:"key" protobuf:"bytes,1,opt,name=key"`
	// operator represents a key's relationship to a set of values.
	// Valid operators are In, NotIn, Exists and DoesNotExist.
	Operator LabelSelectorOperator `json:"operator" protobuf:"bytes,2,opt,name=operator,casttype=LabelSelectorOperator"`
	// values is an array of string values. If the operator is In or NotIn,
	// the values array must be non-empty. If the operator is Exists or DoesNotExist,
	// the values array must be empty. This array is replaced during a strategic
	// merge patch.
	// +optional
	Values []string `json:"values,omitempty" protobuf:"bytes,3,rep,name=values"`
}

type ServiceAccountSelector struct {
	LabelSelector `json:",omitempty"`
	// matchNames is the list of service account names. The requirements are ANDed
	// +kubebuilder:validation:MinItems=1
	MatchNames []string `json:"matchNames,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Security-Group-Ids",type=string,JSONPath=`.spec.securityGroups.groupIds`,description="The security group IDs to apply to the elastic network interface of pods that match this policy"
// +kubebuilder:resource:shortName=sgp

// Custom Resource Definition for applying security groups to pods
type SecurityGroupPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec SecurityGroupPolicySpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

// SecurityGroupPolicyList contains a list of SecurityGroupPolicy
type SecurityGroupPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SecurityGroupPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SecurityGroupPolicy{}, &SecurityGroupPolicyList{})
}
