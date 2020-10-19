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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
)

var (
	testSA                *corev1.ServiceAccount
	testPod               *corev1.Pod
	testScheme            *runtime.Scheme
	testClient            client.Client
	testSecurityGroupsOne []string
	testSecurityGroupsTwo []string
	helper                k8sCacheHelper
	name                  string
	namespace             string
	saName                string
)

func init() {
	name = "test"
	namespace = "test_namespace"
	saName = "test_sa"
	testSA = NewServiceAccount(saName, namespace)
	testPod = NewPod(name, saName, namespace)
	testScheme = runtime.NewScheme()
	clientgoscheme.AddToScheme(testScheme)
	vpcresourcesv1beta1.AddToScheme(testScheme)

	testSecurityGroupsOne = []string{"sg-00001", "sg-00002"}
	testSecurityGroupsTwo = []string{"sg-00003", "sg-00004"}
	testClient = fake.NewFakeClientWithScheme(
		testScheme,
		NewPod(name, saName, namespace),
		NewPodNotForENI(name+"_NoENI", saName, namespace),
		NewPodForMultiENI(name+"_ENIs", saName, namespace),
		NewServiceAccount(saName, namespace),
		NewSecurityGroupPolicyOne(name+"_1", namespace, testSecurityGroupsOne),
		NewSecurityGroupPolicyTwo(name+"_2", namespace, append(testSecurityGroupsOne, testSecurityGroupsTwo...)),
	)

	helper = k8sCacheHelper{
		Client: testClient,
		Log:    ctrl.Log.WithName("testLog"),
	}
}

// NewSecurityGroupPolicyOne creates a test security group policy's pointer.
func NewSecurityGroupPolicyOne(name string, namespace string, securityGroups []string) *vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := &vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"role": "db"},
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

// NewSecurityGroupPolicyTwo creates a test security group policy's pointer.
func NewSecurityGroupPolicyTwo(name string, namespace string, securityGroups []string) *vpcresourcesv1beta1.SecurityGroupPolicy {
	sgp := &vpcresourcesv1beta1.SecurityGroupPolicy{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: vpcresourcesv1beta1.SecurityGroupPolicySpec{
			PodSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "vpc-controller"},
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

// NewPod creates a regular pod for test
func NewPod(name string, sa string, namespace string) *corev1.Pod {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"role":        "db",
				"environment": "qa",
				"app":         "test_app",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Resources: corev1.ResourceRequirements{},
				},
			},
			ServiceAccountName: sa,
		},
		Status: corev1.PodStatus{
			Conditions: []corev1.PodCondition{
				{
					Type:   corev1.PodReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	return pod
}

// NewPodNotForENI creates a regular pod no need for ENI for test.
func NewPodNotForENI(name string, sa string, namespace string) *corev1.Pod {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"app": "test_app",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: sa,
		},
	}
	return pod
}

// NewPodForMultiENI creates a regular pod for ENIs for test.
func NewPodForMultiENI(name string, sa string, namespace string) *corev1.Pod {
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"app":         "vpc-controller",
				"role":        "db",
				"environment": "qa",
			},
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: sa,
		},
	}
	return pod
}

// NewServiceAccount creates a test service account.
func NewServiceAccount(name string, namespace string) *corev1.ServiceAccount {
	sa := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"role":        "db",
				"environment": "qa",
			},
		},
		Secrets:                      nil,
		ImagePullSecrets:             nil,
		AutomountServiceAccountToken: nil,
	}
	return sa
}
