package utils

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
)

// Include checks if a string existing in a string slice and returns true or false.
func Include(target string, values []string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// RemoveDuplicatedSg removes duplicated items from a string slice.
// It returns a no duplicates string slice.
func RemoveDuplicatedSg(list []string) []string {
	set := make(map[string]bool)
	var processedList []string
	for _, sg := range list {
		if _, ok := set[sg]; !ok {
			processedList = append(processedList, sg)
			set[sg] = true
		}
	}
	return processedList
}

type K8sCacheHelper interface {
	GetPodSecurityGroups(pod *corev1.Pod) ([]string, error)
	GetSecurityGroupsFromPod(podId types.NamespacedName) ([]string, error)
}

// GetPodSecurityGroups decide if the testPod can be mutated to inject ENI annotation for security groups.
// The function returns security group list and true or false for mutating testPod.
func (kch *k8sCacheHelper) GetPodSecurityGroups(pod *corev1.Pod) ([]string, error) {
	helperLog := kch.Log.WithValues("Pod name", pod.Name, "Pod namespace", pod.Namespace)

	// Build SGP list from cache.
	ctx := context.Background()
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{}
	if err := kch.Client.List(ctx, sgpList, &client.ListOptions{Namespace: pod.Namespace}); err != nil {
		return nil, err
	}

	sa := &corev1.ServiceAccount{}
	key := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Spec.ServiceAccountName}

	// Get metadata of SA associated with Pod from cache
	if err := kch.Client.Get(ctx, key, sa); err != nil {
		return nil, err
	}

	sgList := kch.filterPodSecurityGroups(sgpList, pod, sa)
	if len(sgList) > 0 {
		helperLog.Info("Pod matched a SecurityGroupPolicy and will get the following Security Groups:",
			"Security Groups", sgList)
	}
	return sgList, nil
}

func (kch *k8sCacheHelper) filterPodSecurityGroups(
	sgpList *vpcresourcesv1beta1.SecurityGroupPolicyList,
	pod *corev1.Pod,
	sa *corev1.ServiceAccount) []string {
	var sgList []string
	sgpLogger := kch.Log.WithValues("Pod name", pod.Name, "Pod namespace", pod.Namespace)
	for _, sgp := range sgpList.Items {
		hasPodSelector := sgp.Spec.PodSelector != nil
		hasSASelector := sgp.Spec.ServiceAccountSelector != nil
		hasSecurityGroup := sgp.Spec.SecurityGroups.Groups != nil && len(sgp.Spec.SecurityGroups.Groups) > 0

		if (!hasPodSelector && !hasSASelector) || !hasSecurityGroup {
			sgpLogger.Info(
				"Found an invalid SecurityGroupPolicy due to either both of podSelector and saSelector are null, "+
					"or security groups is nil or empty.",
				"Invalid SGP", types.NamespacedName{Name: sgp.Name, Namespace: sgp.Namespace},
				"Security Groups", sgp.Spec.SecurityGroups)
			continue
		}

		podMatched, saMatched := false, false
		if podSelector, podSelectorError :=
			metav1.LabelSelectorAsSelector(sgp.Spec.PodSelector); podSelectorError == nil {
			if podSelector.Matches(labels.Set(pod.Labels)) {
				podMatched = true
			}
		} else {
			sgpLogger.Error(podSelectorError, "Failed converting SGP pod selector to match pod labels.",
				"SGP name", sgp.Name, "SGP namespace", sgp.Namespace)
		}

		if saSelector, saSelectorError :=
			metav1.LabelSelectorAsSelector(sgp.Spec.ServiceAccountSelector); saSelectorError == nil {
			if saSelector.Matches(labels.Set(sa.Labels)) {
				saMatched = true
			}
		} else {
			sgpLogger.Error(saSelectorError, "Failed converting SGP SA selector to match pod labels.",
				"SGP name", sgp.Name, "SGP namespace", sgp.Namespace)
		}

		if (hasPodSelector && !podMatched) || (hasSASelector && !saMatched) {
			continue
		}

		sgList = append(sgList, sgp.Spec.SecurityGroups.Groups...)
	}

	sgList = RemoveDuplicatedSg(sgList)
	return sgList
}
