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

package manifest

import (
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SGPBuilder struct {
	name          string
	namespace     string
	securityGroup []string
	podSelector   *v1.LabelSelector
	saSelector    *v1.LabelSelector
}

func NewSGPBuilder() *SGPBuilder {
	return &SGPBuilder{
		name:          utils.ResourceNamePrefix + "sgp",
		namespace:     "default",
		securityGroup: nil,
		podSelector:   nil,
		saSelector:    nil,
	}
}

func (s *SGPBuilder) Name(name string) *SGPBuilder {
	s.name = name
	return s
}

func (s *SGPBuilder) Namespace(namespace string) *SGPBuilder {
	s.namespace = namespace
	return s
}

func (s *SGPBuilder) SecurityGroup(securityGroup []string) *SGPBuilder {
	s.securityGroup = securityGroup
	return s
}

func (s *SGPBuilder) PodMatchLabel(key string, value string) *SGPBuilder {
	if s.podSelector == nil {
		s.podSelector = &v1.LabelSelector{}
	}
	if s.podSelector.MatchLabels == nil {
		s.podSelector.MatchLabels = map[string]string{}
	}
	s.podSelector.MatchLabels[key] = value
	return s
}

func (s *SGPBuilder) ServiceAccountMatchLabel(key string, value string) *SGPBuilder {
	if s.saSelector == nil {
		s.saSelector = &v1.LabelSelector{}
	}
	if s.saSelector.MatchLabels == nil {
		s.saSelector.MatchLabels = map[string]string{}
	}
	s.saSelector.MatchLabels[key] = value
	return s
}

func (s *SGPBuilder) ServiceAccountMatchExpression(key string, operator v1.LabelSelectorOperator, values ...string) *SGPBuilder {
	if s.saSelector == nil {
		s.saSelector = &v1.LabelSelector{}
	}
	if s.saSelector.MatchExpressions == nil {
		s.saSelector.MatchExpressions = []v1.LabelSelectorRequirement{}
	}
	s.saSelector.MatchExpressions = append(s.saSelector.MatchExpressions, v1.LabelSelectorRequirement{
		Key:      key,
		Operator: operator,
		Values:   values,
	})
	return s
}

func (s *SGPBuilder) Build() (*v1beta1.SecurityGroupPolicy, error) {
	if s.securityGroup == nil {
		return nil, fmt.Errorf("security group is required field")
	}
	if s.podSelector == nil && s.saSelector == nil {
		return nil, fmt.Errorf("either pod selector or service account selector is required field")
	}

	return &v1beta1.SecurityGroupPolicy{
		ObjectMeta: v1.ObjectMeta{
			Name:      s.name,
			Namespace: s.namespace,
		},
		Spec: v1beta1.SecurityGroupPolicySpec{
			PodSelector:            s.podSelector,
			ServiceAccountSelector: s.saSelector,
			SecurityGroups:         v1beta1.GroupIds{Groups: s.securityGroup},
		},
	}, nil
}

func (s *SGPBuilder) PodMatchExpression(key string, operator v1.LabelSelectorOperator, values ...string) *SGPBuilder {
	if s.podSelector == nil {
		s.podSelector = &v1.LabelSelector{}
	}
	if s.podSelector.MatchExpressions == nil {
		s.podSelector.MatchExpressions = []v1.LabelSelectorRequirement{}
	}
	s.podSelector.MatchExpressions = append(s.podSelector.MatchExpressions, v1.LabelSelectorRequirement{
		Key:      key,
		Operator: operator,
		Values:   values,
	})
	return s
}
