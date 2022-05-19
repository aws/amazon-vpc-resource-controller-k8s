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

package webhook

import (
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/rbac/v1"
)

// TODO: Add integration test for Windows when ConfigMap feature is implemented.
var _ = Describe("when doing pod operations from non vpc-resource-controller user", func() {
	Context("[CANARY] when updating annotations", func() {
		It("should fail on updating pod sgp annotation", func() {
			newPod := pod.DeepCopy()
			newPod.Annotations[config.ResourceNamePodENI] = "updated-annotation"

			err := frameWork.PodManager.PatchPod(ctx, pod, newPod)
			Expect(err).To(HaveOccurred())
		})

		It("should fail on deleting the pod sgp annotation", func() {
			newPod := pod.DeepCopy()
			delete(newPod.Annotations, config.ResourceNamePodENI)

			err := frameWork.PodManager.PatchPod(ctx, pod, newPod)
			Expect(err).To(HaveOccurred())
		})

		It("should not fail when modifying other annotations", func() {
			newPod := pod.DeepCopy()
			newPod.Annotations["some-other-annotation"] = "new-annotation"

			err := frameWork.PodManager.PatchPod(ctx, pod, newPod)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("[CANARY] when creating new pod", func() {
		It("should fail on creating pod with sgp annotation", func() {
			newPod, err := manifest.NewDefaultPodBuilder().
				Annotations(map[string]string{config.ResourceNamePodENI: "new-annotation"}).
				Namespace(namespace).
				Name("new-pod").
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, newPod, utils.ResourceCreationTimeout)
			Expect(err).To(HaveOccurred())
		})

		It("should not fail on creating new pod without sgp annotation", func() {
			newPod, err := manifest.NewDefaultPodBuilder().
				Annotations(map[string]string{"some-other-annotation": "new-annotation"}).
				Namespace(namespace).
				Name("new-pod").
				Build()
			Expect(err).ToNot(HaveOccurred())

			_, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, newPod, utils.ResourceCreationTimeout)
			Expect(err).ToNot(HaveOccurred())

			// Allow the cache to sync
			time.Sleep(utils.PollIntervalShort)

			err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, newPod)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("[CANARY] when updating node", func() {
		labelKey := "k8s-app"
		labelCNIValue := "aws-node"
		labelProxyValue := "kube-proxy"
		cmdCurl := []string{
			"curl",
			"-o",
			"kubectl",
			"https://s3.us-west-2.amazonaws.com/amazon-eks/1.22.6/2022-03-09/bin/linux/amd64/kubectl",
		}
		cmdChMod := []string{
			"chmod", "+x", "./kubectl",
		}

		var awsNodeOldRules []v1.PolicyRule
		var kubeProxyOldRules []v1.PolicyRule
		It("should successfully update clusterroles", func() {
			awsNodeOldRules = PatchClusterRole("aws-node", nil, "nodes", []string{"get", "list", "watch", "update", "patch"})
			kubeProxyOldRules = PatchClusterRole("system:node-proxier", nil, "nodes", []string{"get", "list", "watch", "update", "patch"})
		})

		It("should fail on updating node with unauthorized taint from aws-node", func() {
			awsNodePods, err := frameWork.PodManager.GetPodsWithLabel(ctx, config.KubeSystemNamespace, labelKey, labelCNIValue)
			Expect(err).ToNot(HaveOccurred())
			Expect(awsNodePods).ToNot(BeEmpty())

			nodes, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes.Items).ToNot(BeEmpty())

			awsNodePodName := awsNodePods[0].ObjectMeta.Name
			targetNode := awsNodePods[1].Spec.NodeName
			cmdTaint := []string{
				"./kubectl",
				"taint",
				"no",
				targetNode,
				"key1=value1:NoSchedule",
			}

			cmdLabel := []string{
				"./kubectl",
				"label",
				"no",
				targetNode,
				"test=true",
			}

			for i, cmd := range [][]string{cmdCurl, cmdChMod, cmdTaint, cmdLabel} {
				stdout, stderr, err := frameWork.PodManager.PodExec(config.KubeSystemNamespace, awsNodePodName, cmd)
				if i > 1 {
					Expect(err).To(HaveOccurred())
					Expect(stdout).To(BeEmpty())
					Expect(stderr).To(ContainSubstring("denied the request"))
				} else {
					Expect(err).ToNot(HaveOccurred())
				}
			}
		})

		It("Should succeed on updating node with labels from non aws-node", func() {
			kubeProxyPods, err := frameWork.PodManager.GetPodsWithLabel(ctx, config.KubeSystemNamespace, labelKey, labelProxyValue)
			Expect(err).ToNot(HaveOccurred())
			Expect(kubeProxyPods).ToNot(BeEmpty())

			nodes, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes.Items).ToNot(BeEmpty())
			kubeProxyPodName := kubeProxyPods[0].ObjectMeta.Name
			cmdUpdateApt := []string{
				"apt", "update",
			}
			cmdInstallCurl := []string{
				"apt", "install", "curl", "-y",
			}
			targetNode := kubeProxyPods[1].Spec.NodeName
			cmdLabel := []string{
				"./kubectl",
				"label",
				"no",
				targetNode,
				"test=true",
			}

			for i, cmd := range [][]string{cmdUpdateApt, cmdInstallCurl, cmdCurl, cmdChMod, cmdLabel} {
				stdout, stderr, err := frameWork.PodManager.PodExec(config.KubeSystemNamespace, kubeProxyPodName, cmd)
				Expect(err).ToNot(HaveOccurred())
				if i == 4 {
					Expect(stdout).To(ContainSubstring("labeled"))
					Expect(stderr).To(BeEmpty())
				}
			}

			// remove the test label from the node
			cmdLabel = []string{
				"./kubectl",
				"label",
				"no",
				targetNode,
				"test-",
			}
			stdout, stderr, err := frameWork.PodManager.PodExec(config.KubeSystemNamespace, kubeProxyPodName, cmdLabel)
			Expect(err).ToNot(HaveOccurred())
			Expect(stdout).To(ContainSubstring("labeled"))
			Expect(stderr).To(BeEmpty())
		})

		It("Should succeed on restoring clusterroles", func() {
			awsNodeOldRules = PatchClusterRole("aws-node", awsNodeOldRules, "nodes", []string{"get", "list", "watch", "update", "patch"})
			kubeProxyOldRules = PatchClusterRole("system:node-proxier", kubeProxyOldRules, "nodes", []string{"get", "list", "watch", "update", "patch"})
		})
	})
})

func PatchClusterRole(roleName string, rules []v1.PolicyRule, resourceName string, verbs []string) []v1.PolicyRule {
	role, err := frameWork.RBACManager.GetClusterRole(roleName)
	Expect(err).ToNot(HaveOccurred())
	newRole := role.DeepCopy()

	if rules != nil {
		newRole.Rules = rules
	} else {
		for i, rule := range newRole.Rules {
			for _, resource := range rule.Resources {
				if resource == resourceName {
					rule.Verbs = verbs
				}
			}
			newRole.Rules[i] = rule
		}
	}
	err = frameWork.RBACManager.PatchClusterRole(newRole)
	Expect(err).ToNot(HaveOccurred())
	return role.Rules
}
