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

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
)

// TestInclude tests if Include func works as expected.
func TestInclude(t *testing.T) {
	target := "sg-00001"
	offTarget := "sg-00007"
	list := []string{
		"sg-00001",
		"sg-00002",
		"sg-00003",
		"sg-00004",
		"sg-00005",
	}

	assert.True(t, Include(target, list))
	assert.False(t, Include(offTarget, list))
}

// TestRemoveDuplicatedSg tests if RemoveDuplicatedSg func works as expected.
func TestRemoveDuplicatedSg(t *testing.T) {
	duplicatedSGs := []string{
		"sg-00001",
		"sg-00002",
		"sg-00003",
		"sg-00001",
		"sg-00004",
		"sg-00005",
	}

	expectedSgs := []string{
		"sg-00001",
		"sg-00002",
		"sg-00003",
		"sg-00004",
		"sg-00005",
	}

	processedSgs := RemoveDuplicatedSg(duplicatedSGs)
	assert.Equal(t, len(expectedSgs), len(processedSgs))
	for _, sg := range processedSgs {
		assert.True(t, Include(sg, expectedSgs))
	}
}

// TestCanInjectENI_CombinedSelectors tests SGP with both Pod and SA selectors.
func TestCanInjectENI_CombinedSelectors(t *testing.T) {
	securityGroupPolicyCombined := NewSecurityGroupPolicyCombined(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyCombined},
	}

	// Combined SA selector and PodSelector
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_CombinedSelectors tests SGP with Pod selector.
func TestCanInjectENI_PodSelectors(t *testing.T) {
	// PodSelector alone
	securityGroupPolicyPod := NewSecurityGroupPolicyPodSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyPod},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_SASelectors tests SGP with SA selector.
func TestCanInjectENI_SASelectors(t *testing.T) {
	// SaSelector alone
	securityGroupPolicySa := NewSecurityGroupPolicySaSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicySa},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_Multi_SGPs tests two SGP objects.
func TestCanInjectENI_Multi_SGPs(t *testing.T) {
	securityGroupPolicySa := NewSecurityGroupPolicySaSelector(
		name, namespace, []string{"sg-00001"})
	securityGroupPolicyPod := NewSecurityGroupPolicyPodSelector(
		name, namespace, []string{"sg-00002"})
	sgsList := []vpcresourcesv1beta1.SecurityGroupPolicy{
		securityGroupPolicySa,
		securityGroupPolicyPod}
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    sgsList,
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_EmptyPodSelector tests empty pod selector in SGP.
func TestCanInjectENI_EmptyPodSelector(t *testing.T) {
	// Empty testPod selector in CRD
	securityGroupPolicyEmptyPodSelector := NewSecurityGroupPolicyEmptyPodSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyEmptyPodSelector},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_EmptySASelector tests empty SA selector in SGP.
func TestCanInjectENI_EmptySASelector(t *testing.T) {
	// Empty testSA selector in CRD
	securityGroupPolicyEmptySaSelector := NewSecurityGroupPolicyEmptySaSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyEmptySaSelector},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_MatchLabelSASelector tests empty SA selector in SGP.
func TestCanInjectENI_MatchLabelSASelector(t *testing.T) {
	// Empty testSA selector in CRD
	securityGroupPolicyEmptySaSelector := NewSecurityGroupPolicyMatchLabelSASelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyEmptySaSelector},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_MatchExpressionsSASelector tests empty SA selector in SGP.
func TestCanInjectENI_MatchExpressionsSASelector(t *testing.T) {
	// Empty testSA selector in CRD
	securityGroupPolicyEmptySaSelector := NewSecurityGroupPolicyMatchExpressionSASelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyEmptySaSelector},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_MatchLabelPodSelector tests empty SA selector in SGP.
func TestCanInjectENI_MatchLabelPodSelector(t *testing.T) {
	// Empty testSA selector in CRD
	securityGroupPolicyEmptySaSelector := NewSecurityGroupPolicyMatchLabelPodSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyEmptySaSelector},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_EmptySASelector tests empty SA selector in SGP.
func TestCanInjectENI_MatchExpressionsPodSelector(t *testing.T) {
	// Empty testSA selector in CRD
	securityGroupPolicyEmptySaSelector := NewSecurityGroupPolicyMatchExpressionPodSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyEmptySaSelector},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, isEverySecurityGroupIncluded(sgs))
}

// TestCanInjectENI_MismatchedSASelector tests mismatched SA selector in SGP.
func TestCanInjectENI_MismatchedSASelector(t *testing.T) {
	// Mismatched testPod testSA
	securityGroupPolicySa := NewSecurityGroupPolicySaSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicySa},
	}
	mismatchedSa := testSA.DeepCopy()
	mismatchedSa.Labels["environment"] = "dev"
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, mismatchedSa)
	assert.True(t, len(sgs) == 0)
}

// TestEmptySecurityGroupInSGP tests empty security group groupids in SGP.
func TestEmptySecurityGroupInSGP(t *testing.T) {
	securityGroupPolicyPod := NewSecurityGroupPolicyPodSelector(
		"test", "test_namespace", testSecurityGroupsOne)
	securityGroupPolicyPod.Spec.SecurityGroups.Groups = []string{}
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{
		TypeMeta: metav1.TypeMeta{},
		ListMeta: metav1.ListMeta{},
		Items:    []vpcresourcesv1beta1.SecurityGroupPolicy{securityGroupPolicyPod},
	}
	sgs := helper.filterPodSecurityGroups(sgpList, testPod, testSA)
	assert.True(t, len(sgs) == 0)
}

// TestShouldAddENILimits tests if pod is valid for SGP to inject ENI limits/requests.
func TestShouldAddENILimits(t *testing.T) {
	sgList, _ := helper.GetPodSecurityGroups(testPod)
	assert.True(t, sgList[0] == testSecurityGroupsOne[0])

	// Mismatched testPod namespace
	mismatchedPod := NewPod("test_pod", "test_sa", "test_namespace_1")
	list, err := helper.GetPodSecurityGroups(mismatchedPod)
	assert.True(t, list == nil)
	assert.Error(t, err)
}

func isEverySecurityGroupIncluded(retrievedSgs []string) bool {
	if len(retrievedSgs) != len(testSecurityGroupsOne) {
		return false
	}

	for _, s := range retrievedSgs {
		if !Include(s, testSecurityGroupsOne) {
			return false
		}
	}
	return true
}

// NewSecurityGroupPolicyCombined creates a test SGP with both pod and SA selectors.
func NewSecurityGroupPolicyCombined(
	name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "db"},
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "environment",
					Operator: "In",
					Values:   []string{"qa", "production"},
				}},
			},
			ServiceAccountSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "db"},
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "environment",
					Operator: "In",
					Values:   []string{"qa", "production"},
				}},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicyPodSelector creates a test SGP with only pod selector.
func NewSecurityGroupPolicyPodSelector(
	name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "db"},
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "environment",
					Operator: "In",
					Values:   []string{"qa", "production"},
				}},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicyEmptyPodSelector creates a test SGP with only empty pod selector.
func NewSecurityGroupPolicyEmptyPodSelector(name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			PodSelector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{},
				MatchExpressions: []metav1.LabelSelectorRequirement{},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicySaSelector creates a test SGP with only SA selector.
func NewSecurityGroupPolicySaSelector(name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			ServiceAccountSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "db"},
				MatchExpressions: []metav1.LabelSelectorRequirement{{
					Key:      "environment",
					Operator: "In",
					Values:   []string{"qa", "production"},
				}},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicyEmptySaSelector creates a test SGP with only empty SA selector.
func NewSecurityGroupPolicyEmptySaSelector(name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			ServiceAccountSelector: &metav1.LabelSelector{
				MatchLabels:      map[string]string{},
				MatchExpressions: []metav1.LabelSelectorRequirement{}},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicyMatchLabelPodSelector creates a test SGP with match label pod selector.
func NewSecurityGroupPolicyMatchLabelPodSelector(name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "db"},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicyMatchExpressionPodSelector creates a test SGP with match expression pod selector.
func NewSecurityGroupPolicyMatchExpressionPodSelector(name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			PodSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: "In",
						Values:   []string{"qa", "production"},
					},
				},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicyMatchLabelSASelector creates a test SGP with match label SA selector.
func NewSecurityGroupPolicyMatchLabelSASelector(name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			ServiceAccountSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "db"},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}

// NewSecurityGroupPolicyMatchExpressionSASelector creates a test SGP with match expression SA selector.
func NewSecurityGroupPolicyMatchExpressionSASelector(name string, namespace string, securityGroups []string) vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			ServiceAccountSelector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: "In",
						Values:   []string{"qa", "production"},
					},
				},
			},
			SecurityGroups: vpcresourcesv1beta1.GroupIds{
				Groups: securityGroups,
			},
		},
	}
	return sgp
}
