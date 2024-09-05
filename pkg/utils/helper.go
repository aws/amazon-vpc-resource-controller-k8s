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
	"context"
	"fmt"
	"strconv"
	"strings"

	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/aws/aws-sdk-go/aws/arn"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	PartitionIndex = 1
	AccountIndex   = 4
)

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

type SecurityGroupForPodsAPI interface {
	GetMatchingSecurityGroupForPods(pod *corev1.Pod) ([]string, error)
}

type SecurityGroupForPods struct {
	Client client.Client
	Log    logr.Logger
}

// NewSecurityGroupForPodsAPI returns the SecurityGroupForPod APIs for common operations on objects
// Using Security Group Policy
func NewSecurityGroupForPodsAPI(client client.Client, log logr.Logger) SecurityGroupForPodsAPI {
	return &SecurityGroupForPods{
		Client: client,
		Log:    log,
	}
}

// GetMatchingSecurityGroupForPods returns the list of security groups that should be associated
// with the Pod by matching against all the SecurityGroupPolicy
func (s *SecurityGroupForPods) GetMatchingSecurityGroupForPods(pod *corev1.Pod) ([]string, error) {
	helperLog := s.Log.WithValues("Pod name", pod.Name, "Pod namespace", pod.Namespace)

	// Build SGP list from cache.
	ctx := context.Background()
	sgpList := &vpcresourcesv1beta1.SecurityGroupPolicyList{}

	if err := s.Client.List(ctx, sgpList, &client.ListOptions{Namespace: pod.Namespace}); err != nil {
		// If the CRD was removed intentionally or accidentally, we don't want to interrupt pods creation.
		// GroupVersionResource or GroupKind not matched check.
		if meta.IsNoMatchError(err) {
			helperLog.Error(err,
				"Webhook couldn't find SGP definition: "+
					"GroupVersionResource or GroupKind didn't match. Will allow regular pods creation.")
			return nil, nil
		}
		helperLog.Error(err, "Client Listing SGP failed in Webhook.")
		return nil, err
	}

	sa := &corev1.ServiceAccount{}
	key := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      pod.Spec.ServiceAccountName}

	// Get metadata of SA associated with Pod from cache
	if err := s.Client.Get(ctx, key, sa); err != nil {
		return nil, err
	}

	sgList := s.filterPodSecurityGroups(sgpList, pod, sa)
	if len(sgList) > 0 {
		helperLog.V(1).Info("Pod matched a SecurityGroupPolicy and will get the following Security Groups:",
			"Security Groups", sgList)
	}
	return sgList, nil
}

func (s *SecurityGroupForPods) filterPodSecurityGroups(
	sgpList *vpcresourcesv1beta1.SecurityGroupPolicyList,
	pod *corev1.Pod,
	sa *corev1.ServiceAccount) []string {
	var sgList []string
	sgpLogger := s.Log.WithValues("Pod name", pod.Name, "Pod namespace", pod.Namespace)
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

// DeconstructIPsFromPrefix deconstructs a IPv4 prefix into a list of /32 IPv4 addresses
func DeconstructIPsFromPrefix(prefix string) ([]string, error) {
	var deconstructedIPs []string

	// find the index of / in prefix
	index := strings.Index(prefix, "/")
	if index < 0 {
		return nil, fmt.Errorf("invalid IPv4 prefix %v", prefix)
	}

	// construct network address
	addr := strings.Split(prefix[:index], ".")
	if addr == nil || len(addr) != 4 {
		return nil, fmt.Errorf("invalid IPv4 prefix %v", prefix)
	}
	networkAddr := addr[0] + "." + addr[1] + "." + addr[2] + "."

	// get mask and calculate number of IPv4 addresses in the range
	mask, err := strconv.Atoi(prefix[index+1:])
	if err != nil {
		return nil, err
	}
	if mask < 0 || mask > 32 {
		return nil, fmt.Errorf("invalid IPv4 prefix %v", prefix)
	}
	numOfAddresses := IntPower(2, 32-mask)

	// concatenate network addr and host addr to get /32 IPv4 address
	for i := 0; i < numOfAddresses; i++ {
		hostAddr, err := strconv.Atoi(addr[3])
		if err != nil {
			return nil, err
		}
		ipAddr := networkAddr + strconv.Itoa(hostAddr+i) + "/32"
		deconstructedIPs = append(deconstructedIPs, ipAddr)
	}
	return deconstructedIPs, nil
}

func IsNitroInstance(instanceType string) (bool, error) {
	limits, found := vpc.Limits[instanceType]
	if !found {
		return false, ErrNotFound
	}
	if limits.IsBareMetal || limits.Hypervisor == "nitro" {
		return true, nil
	}
	return false, nil
}

// GetSourceAcctAndArn constructs source acct and arn and return them for use
func GetSourceAcctAndArn(roleARN, region, clusterName string) (string, string, error) {
	// ARN format (https://docs.aws.amazon.com/IAM/latest/UserGuide/reference-arns.html)
	// arn:partition:service:region:account-id:resource-type/resource-id
	// IAM format, region is always blank
	// arn:aws:iam::account:role/role-name-with-path
	if !arn.IsARN(roleARN) {
		return "", "", fmt.Errorf("incorrect ARN format for role %s", roleARN)
	} else if region == "" {
		return "", "", nil
	}

	parsedArn, err := arn.Parse(roleARN)
	if err != nil {
		return "", "", err
	}

	sourceArn := fmt.Sprintf("arn:%s:eks:%s:%s:cluster/%s", parsedArn.Partition, region, parsedArn.AccountID, clusterName)
	return parsedArn.AccountID, sourceArn, nil
}

// PodHasENIRequest will return true if first container of pod spec has request for eni indicating
// it needs trunk interface from vpc-rc
func PodHasENIRequest(pod *v1.Pod) bool {
	if len(pod.Spec.Containers) > 0 {
		_, hasEniRequest := pod.Spec.Containers[0].Resources.Requests[config.ResourceNamePodENI]
		return hasEniRequest
	}
	return false
}
