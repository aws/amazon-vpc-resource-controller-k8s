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

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/deployment"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/pod"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/rbac"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/rbac/v1"
)

var lastKnowRuleState []v1.PolicyRule

func RestartController(ctx context.Context, manager Manager, depManager deployment.Manager) string {

	ScaleControllerDeployment(ctx, depManager, 0)
	ScaleControllerDeployment(ctx, depManager, 2)

	return GetNewPodWithLeaderLease(ctx, manager)
}

func ForcefullyKillControllerPods(ctx context.Context, manager Manager, podManager pod.Manager) string {
	err := podManager.DeleteAllPodsForcefully(ctx, PodLabelKey, PodLabelVal)
	Expect(err).ToNot(HaveOccurred())

	return GetNewPodWithLeaderLease(ctx, manager)
}

func GetNewPodWithLeaderLease(ctx context.Context, manager Manager) string {
	By(fmt.Sprintf("waiting for the leader to take over lease"))
	time.Sleep(time.Second * 60)

	By("waiting till the new controller has the leader lease")
	leaderPod, err := manager.WaitTillControllerHasLeaderLease(ctx)
	Expect(err).ToNot(HaveOccurred())

	By(fmt.Sprintf("waiting for the leader %s to initalize", leaderPod))
	time.Sleep(time.Second * 30)

	return leaderPod
}

func ScaleControllerDeployment(ctx context.Context, deploymentManager deployment.Manager, replica int) {
	By(fmt.Sprintf("scaling the controller deployment to %d", replica))
	err := deploymentManager.ScaleDeploymentAndWaitTillReady(ctx,
		Namespace,
		DeploymentName,
		int32(replica))
	Expect(err).ToNot(HaveOccurred())
}

func VerifyLeaseHolderIsSame(ctx context.Context, manager Manager,
	previousLeaseHolder string) {
	By("verifying the lease didn't switch")
	newLeaderName, err := manager.WaitTillControllerHasLeaderLease(ctx)
	Expect(err).ToNot(HaveOccurred())
	Expect(newLeaderName).To(Equal(previousLeaseHolder))
}

// PatchClusterRole patches the cluster Role with a given set of verbs and stores the
// existing state in variable, so the original state can be later recovered
func PatchClusterRole(rbac rbac.Manager, resourceName string, verbs []string) {
	By(fmt.Sprintf("patching the rbac rule with resource %s with verbs %v",
		resourceName, verbs))
	role, err := rbac.GetClusterRole(ClusterRoleName)
	Expect(err).ToNot(HaveOccurred())

	lastKnowRuleState = role.DeepCopy().Rules
	newRole := role.DeepCopy()

	for i, rule := range newRole.Rules {
		for _, resource := range rule.Resources {
			if resource == resourceName {
				rule.Verbs = verbs
			}
		}
		newRole.Rules[i] = rule
	}

	err = rbac.PatchClusterRole(newRole)
	Expect(err).ToNot(HaveOccurred())
}

// RevertClusterRoleChanges reverts the RBAC cluster role to the older known state
func RevertClusterRoleChanges(rbac rbac.Manager) {
	role, err := rbac.GetClusterRole(ClusterRoleName)
	Expect(err).ToNot(HaveOccurred())

	newRule := role.DeepCopy()
	newRule.Rules = lastKnowRuleState

	err = rbac.PatchClusterRole(newRule)
	Expect(err).ToNot(HaveOccurred())
}
