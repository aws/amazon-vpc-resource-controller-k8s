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
	"fmt"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TODO: Add integration test for Windows when ConfigMap feature is implemented.
var _ = Describe("validating webhook test cases", func() {
	Context("[CANARY] when modifying existing pod", func() {
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

	Context("[CANARY] when modifying existing node", func() {
		var existingNodeNameToMutate string
		var k8sClientToMutateNode client.Client
		BeforeEach(func() {
			nodes, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes.Items).ToNot(BeEmpty())
			existingNodeNameToMutate = nodes.Items[0].Name
		})

		When("with aws-node credentials", func() {
			BeforeEach(func() {
				k8sSchema := runtime.NewScheme()
				clientgoscheme.AddToScheme(k8sSchema)
				restCfgWithAWSNode, err := frameWork.SAManager.BuildRestConfigWithServiceAccount(ctx, types.NamespacedName{Namespace: config.KubeSystemNamespace, Name: "aws-node"})
				Expect(err).NotTo(HaveOccurred())
				k8sClientToMutateNode, err = client.New(restCfgWithAWSNode, client.Options{Scheme: k8sSchema})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should succeed update node with vpc.amazonaws.com/eniConfig label", func() {
				var originalNodeLabels map[string]string
				By(fmt.Sprintf("update node %s with %s label", existingNodeNameToMutate, config.CustomNetworkingLabel), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						originalNodeLabels = existingNodeToMutate.Labels
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Labels[config.CustomNetworkingLabel] = "dummy-value"
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})

				By(fmt.Sprintf("restore node %s with original labels", existingNodeNameToMutate), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Labels = originalNodeLabels
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})
			})

			It("should fail update node with dummy-label label", func() {
				By(fmt.Sprintf("update node %s with %s label", existingNodeNameToMutate, "dummy-label"), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Labels["dummy-label"] = "dummy-value"
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).To(HaveOccurred())
					Expect(err).Should(MatchError(errors.New("admission webhook \"vnode.vpc.k8s.aws\" denied the request: aws-node can only update limited fields on the Node Object")))
				})
			})

			It("should fail update node with dummy-key taint", func() {
				By(fmt.Sprintf("update node %s with %s taint", existingNodeNameToMutate, "dummy-key"), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Spec.Taints = append(nodeCopy.Spec.Taints, corev1.Taint{
							Key:    "dummy-key",
							Value:  "dummy-value",
							Effect: corev1.TaintEffectPreferNoSchedule,
						})
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).To(HaveOccurred())
					Expect(err).Should(MatchError(errors.New("admission webhook \"vnode.vpc.k8s.aws\" denied the request: aws-node can only update limited fields on the Node Object")))
				})
			})
		})

		When("with framework credentials", func() {
			BeforeEach(func() {
				k8sClientToMutateNode = frameWork.K8sClient
			})

			It("should succeed update node with vpc.amazonaws.com/eniConfig label", func() {
				var originalNodeLabels map[string]string
				By(fmt.Sprintf("update node %s with %s label", existingNodeNameToMutate, config.CustomNetworkingLabel), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						originalNodeLabels = existingNodeToMutate.Labels
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Labels[config.CustomNetworkingLabel] = "dummy-value"
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})

				By(fmt.Sprintf("restore node %s with original labels", existingNodeNameToMutate), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Labels = originalNodeLabels
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})
			})

			It("should succeed update node with dummy-label label", func() {
				var originalNodeLabels map[string]string
				By(fmt.Sprintf("update node %s with %s label", existingNodeNameToMutate, "dummy-label"), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						originalNodeLabels = existingNodeToMutate.Labels
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Labels["dummy-label"] = "dummy-value"
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})

				By(fmt.Sprintf("restore node %s with original labels", existingNodeNameToMutate), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Labels = originalNodeLabels
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})
			})

			It("should succeed update node with dummy-key taint", func() {
				var originalNodeTaints []corev1.Taint
				By(fmt.Sprintf("update node %s with %s taint", existingNodeNameToMutate, "dummy-key"), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						originalNodeTaints = existingNodeToMutate.Spec.Taints
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Spec.Taints = append(nodeCopy.Spec.Taints, corev1.Taint{
							Key:    "dummy-key",
							Value:  "dummy-value",
							Effect: corev1.TaintEffectPreferNoSchedule,
						})
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})

				By(fmt.Sprintf("restore node %s with original taints", existingNodeNameToMutate), func() {
					err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
						existingNodeToMutate := &corev1.Node{}
						if err := k8sClientToMutateNode.Get(ctx, types.NamespacedName{Name: existingNodeNameToMutate}, existingNodeToMutate); err != nil {
							return err
						}
						nodeCopy := existingNodeToMutate.DeepCopy()
						nodeCopy.Spec.Taints = originalNodeTaints
						return k8sClientToMutateNode.Update(ctx, nodeCopy)
					})
					Expect(err).NotTo(HaveOccurred())
				})
			})
		})
	})
})
