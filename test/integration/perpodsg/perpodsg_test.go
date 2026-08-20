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

package perpodsg_test

import (
	"fmt"
	"time"

	cninode "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/controller"
	deploymentWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/deployment"
	podWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/pod"
	sgpWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Branch ENI Pods", func() {
	var (
		securityGroupPolicy *v1beta1.SecurityGroupPolicy

		namespace      string
		sgpLabelKey    string
		sgpLabelValue  string
		podLabelKey    string
		podLabelValue  string
		securityGroups []string

		err error
	)

	BeforeEach(func() {
		namespace = "per-pod-sg"
		sgpLabelKey = "role"
		sgpLabelValue = "db"
		podLabelKey = "role"
		podLabelValue = "db"
		securityGroups = []string{securityGroupID1}
	})

	JustBeforeEach(func() {
		By("creating the namespace if not exist")
		err := frameWork.NSManager.CreateNamespace(ctx, namespace)
		Expect(err).ToNot(HaveOccurred())

		securityGroupPolicy, err = manifest.NewSGPBuilder().
			Namespace(namespace).
			PodMatchLabel(sgpLabelKey, sgpLabelValue).
			SecurityGroup(securityGroups).Build()
		Expect(err).NotTo(HaveOccurred())
	})

	JustAfterEach(func() {
		By("deleting security group policy")
		err = frameWork.SGPManager.DeleteAndWaitTillSecurityGroupIsDeleted(ctx, securityGroupPolicy)
		Expect(err).NotTo(HaveOccurred())

		By("deleting the namespace")
		err = frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("creating deployment", func() {
		var deployment *appsv1.Deployment

		JustBeforeEach(func() {
			deployment = manifest.NewDefaultDeploymentBuilder().
				Namespace(namespace).
				Replicas(10).
				PodLabel(podLabelKey, podLabelValue).Build()
		})

		JustAfterEach(func() {
			By("deleting the deployment")
			err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when the deployment is created", func() {
			It("should have all the pods running", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				deploymentWrapper.
					CreateAndWaitForDeploymentToStart(frameWork.DeploymentManager, ctx, deployment)
				verify.VerifyNetworkingOfAllPodUsingENI(namespace, podLabelKey, podLabelValue,
					securityGroups)
			})
		})
	})

	Describe("creating branch pods", func() {
		var pod *v1.Pod

		JustBeforeEach(func() {
			pod, err = manifest.NewDefaultPodBuilder().
				Namespace(namespace).
				Labels(map[string]string{podLabelKey: podLabelValue}).Build()
			Expect(err).NotTo(HaveOccurred())
		})

		JustAfterEach(func() {
			By("deleting the pod")
			err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, pod)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when a pod in a namespace is created", func() {
			It("should get the SG from SGP in pod's namespace", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
			})
		})

		Context("when a branch ENI pod is created", func() {
			It("should have connection tracking configuration matching the primary ENI", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)

				var podNode *v1.Node
				for i := range nodeList.Items {
					if nodeList.Items[i].Name == pod.Spec.NodeName {
						podNode = &nodeList.Items[i]
						break
					}
				}
				Expect(podNode).NotTo(BeNil())
				instanceID := frameWork.NodeManager.GetInstanceID(podNode)
				verify.VerifyConnectionTrackingOfBranchENI(*pod, instanceID)
			})
		})

		Context("when a pod in default namespace is created", func() {
			BeforeEach(func() {
				namespace = "default"
			})
			It("should get the SG from SGP in default namespace", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
			})
		})

		Context("when a pod is created and deleted", func() {
			var eniList []*trunk.ENIDetails

			AfterEach(func() {
				By("waiting for the branch ENI to be deleted")
				err := frameWork.EC2Manager.WaitTillTheENIIsDeleted(ctx, eniList[0].ID)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should run with the branch ENI and wait till the branch is deleted", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				eniList = verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
			})
		})

		// M1 (design doc section 2.2, requirements.md S2): after a branch-ENI pod is deleted, its
		// trunk slot is released in the same reconcile pass (a replacement pod on the SAME node comes
		// up without waiting a full cooldown), while the VLAN id it held is withheld from reuse until
		// reuseCooldown elapses (the replacement is assigned a DIFFERENT vlan id, never the one just
		// freed). Both pods are pinned to one node so they share a trunk - the cooldown is per-trunk.
		//
		// The node is filled to (branch-ENI limit - 1) with filler pods BEFORE the pod under test is
		// created, so the trunk sits at exactly full capacity when the pod under test is deleted. This
		// is required for the slot-release assertion to mean anything: on a trunk nowhere near its
		// limit, a replacement pod gets a fresh slot regardless of whether the old one was released
		// immediately or not at all - the assertion would pass even if M1's immediate-release path
		// were completely broken. At full capacity, the replacement can ONLY reach Running if the
		// just-deleted pod's slot was actually freed in time.
		Context("when a branch ENI pod is deleted and a replacement is created on a trunk at full capacity", func() {
			var (
				targetNode  string
				branchLimit int
				fillerPods  []*v1.Pod
				firstPod    *v1.Pod
				secondPod   *v1.Pod
				firstENIs   []*trunk.ENIDetails
				secondENIs  []*trunk.ENIDetails
			)

			JustBeforeEach(func() {
				Expect(nodeList.Items).NotTo(BeEmpty())
				targetNode = nodeList.Items[0].Name
				instanceID := frameWork.NodeManager.GetInstanceID(&nodeList.Items[0])
				instanceDetails, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
				Expect(err).NotTo(HaveOccurred())
				branchLimit = vpc.Limits[string(instanceDetails.InstanceType)].BranchInterface
				Expect(branchLimit).To(BeNumerically(">", 1),
					"target node's instance type must support at least 2 branch ENIs for this test to mean anything")
				fillerPods = nil
			})

			JustAfterEach(func() {
				// firstPod is deleted mid-test and set to nil on the happy path; clean it up
				// here so a failure between its creation and deletion does not leak a pod
				// that pins the node at full branch-ENI capacity.
				if firstPod != nil {
					_ = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, firstPod)
				}
				if secondPod != nil {
					_ = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, secondPod)
				}
				for _, filler := range fillerPods {
					_ = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, filler)
				}
			})

			It("releases the slot immediately but withholds the freed vlan until its reuse cooldown elapses", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)

				By(fmt.Sprintf("filling the target node to %d/%d branch-ENI capacity with filler pods",
					branchLimit-1, branchLimit))
				for i := 0; i < branchLimit-1; i++ {
					fillerPod, err := manifest.NewDefaultPodBuilder().
						Namespace(namespace).
						Name(fmt.Sprintf("%sfiller-%d", utils.ResourceNamePrefix, i)).
						Labels(map[string]string{podLabelKey: podLabelValue}).
						NodeName(targetNode).Build()
					Expect(err).NotTo(HaveOccurred())
					fillerPod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, fillerPod)
					verify.VerifyNetworkingOfPodUsingENI(*fillerPod, securityGroups)
					fillerPods = append(fillerPods, fillerPod)
				}

				By("creating the pod under test, bringing the trunk to full capacity")
				firstPod, err = manifest.NewDefaultPodBuilder().
					Namespace(namespace).
					Name(utils.ResourceNamePrefix + "first").
					Labels(map[string]string{podLabelKey: podLabelValue}).
					NodeName(targetNode).Build()
				Expect(err).NotTo(HaveOccurred())
				firstPod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, firstPod)
				firstENIs = verify.VerifyNetworkingOfPodUsingENI(*firstPod, securityGroups)
				Expect(firstENIs).NotTo(BeEmpty())
				freedVlan := firstENIs[0].VlanID
				freedENIID := firstENIs[0].ID

				By("deleting the pod under test, releasing its trunk slot on a now-at-capacity trunk")
				Expect(frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, firstPod)).To(Succeed())
				firstPod = nil

				By("creating a replacement pod on the same node immediately, within the vlan reuse cooldown")
				secondPod, err = manifest.NewDefaultPodBuilder().
					Namespace(namespace).
					Name(utils.ResourceNamePrefix + "second").
					Labels(map[string]string{podLabelKey: podLabelValue}).
					NodeName(targetNode).Build()
				Expect(err).NotTo(HaveOccurred())
				// M1 slot release assertion: with the trunk left at exactly branchLimit-1 used slots
				// (the fillers) plus this one pending create, the replacement can reach Running ONLY
				// if the deleted pod's slot was actually freed - a broken immediate-release path would
				// leave it stuck Pending on ErrCurrentlyAtMaxCapacity instead.
				secondPod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, secondPod)
				secondENIs = verify.VerifyNetworkingOfPodUsingENI(*secondPod, securityGroups)
				Expect(secondENIs).NotTo(BeEmpty())

				By("asserting the replacement did NOT reuse the just-freed vlan id while it is still cooling")
				Expect(secondENIs[0].VlanID).NotTo(Equal(freedVlan),
					"a vlan still inside its reuse cooldown must not be handed to the replacement pod")
				Expect(secondENIs[0].ID).NotTo(Equal(freedENIID),
					"the replacement must get a fresh branch ENI, not the one just released")

				By("waiting for the deleted pod's branch ENI to be fully deleted")
				Expect(frameWork.EC2Manager.WaitTillTheENIIsDeleted(ctx, freedENIID)).To(Succeed())
			})
		})

		Context("when a pod matches more than one SGPs", func() {
			var (
				securityGroups2      []string
				securityGroupPolicy2 *v1beta1.SecurityGroupPolicy
			)
			BeforeEach(func() {
				securityGroups2 = []string{securityGroupID2}
			})
			JustBeforeEach(func() {
				securityGroupPolicy2, err = manifest.NewSGPBuilder().Name(utils.ResourceNamePrefix+"sgp2").
					Namespace(namespace).
					PodMatchLabel(sgpLabelKey, sgpLabelValue).
					SecurityGroup(securityGroups2).Build()
				Expect(err).NotTo(HaveOccurred())
			})

			JustAfterEach(func() {
				By("deleting security group policy")
				err = frameWork.K8sClient.Delete(ctx, securityGroupPolicy2)
				Expect(err).NotTo(HaveOccurred())
			})

			Context("[CANARY] when these SGPs have different security groups", func() {
				It("should run with Branch ENI IP with all security groups from all SGPs", func() {
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy2)
					pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
					verify.VerifyNetworkingOfPodUsingENI(*pod, append(securityGroups, securityGroupID2))
				})
			})

			Context("when these SGPs have duplicated security groups", func() {
				BeforeEach(func() {
					//sg1 = [securityGroupID1]
					securityGroups2 = []string{securityGroupID1, securityGroupID1, securityGroupID2}
				})

				It("should run with Branch ENI IP with only one security group from all SGPs", func() {
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy2)
					pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
					verify.VerifyNetworkingOfPodUsingENI(*pod, []string{securityGroupID1, securityGroupID2})
				})
			})
		})

		Context("when a SGP has expression selector", func() {
			var (
				sgpExpressionKey   = "environment"
				sgpExpressionValue = []string{"qa", "production"}
			)
			Context("[CANARY] when the SGP has only expression selector and a pod matches the expression", func() {
				BeforeEach(func() {
					podLabelKey = sgpExpressionKey
					podLabelValue = sgpExpressionValue[0]
				})

				JustBeforeEach(func() {
					securityGroupPolicy, err = manifest.NewSGPBuilder().
						Namespace(namespace).
						PodMatchExpression(sgpExpressionKey, metav1.LabelSelectorOpIn, sgpExpressionValue...).
						SecurityGroup(securityGroups).Build()
					Expect(err).NotTo(HaveOccurred())
				})

				It("should run with Branch ENI IP with the SG from the matched SGP", func() {
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
					pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
					verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
				})
			})

			Context("[CANARY] when the SGP has label selector and expression selector", func() {
				JustBeforeEach(func() {
					securityGroupPolicy, err = manifest.NewSGPBuilder().
						Namespace(namespace).
						PodMatchExpression(sgpExpressionKey, metav1.LabelSelectorOpIn, sgpExpressionValue...).
						PodMatchLabel(sgpLabelKey, sgpLabelValue).
						SecurityGroup(securityGroups).Build()
					Expect(err).NotTo(HaveOccurred())
				})

				Context("when the pod only matches label selector, not expression selector", func() {
					It("should run without branch ENI annotation", func() {
						sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
						pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
						verify.PodHasNoBranchENIAnnotationInjected(pod)
					})
				})

				Context("when the pod only matches expression selector, not label selector", func() {
					BeforeEach(func() {
						podLabelKey = sgpExpressionKey
						podLabelValue = sgpExpressionValue[0]
					})

					It("should run without branch ENI annotation", func() {
						sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
						pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
						verify.PodHasNoBranchENIAnnotationInjected(pod)
					})
				})

				Context("when the pod matches both label and expression selectors", func() {
					JustBeforeEach(func() {
						pod, err = manifest.NewDefaultPodBuilder().
							Namespace(namespace).
							Labels(map[string]string{podLabelKey: podLabelValue, sgpExpressionKey: sgpExpressionValue[0]}).
							Build()
						Expect(err).NotTo(HaveOccurred())
					})

					It("should run with branch ENI annotation", func() {
						sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
						pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
						verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
					})
				})
			})
		})

		Context("[CANARY] when adding new security group to a existing SGP", func() {
			JustBeforeEach(func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
			})

			It("a new pod should run with all security groups", func() {
				sgpWrapper.UpdateSecurityGroupPolicy(
					frameWork.K8sClient, ctx, securityGroupPolicy, []string{securityGroupID1, securityGroupID2},
				)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				By("the pod has two sgs")
				verify.VerifyNetworkingOfPodUsingENI(*pod, []string{securityGroupID1, securityGroupID2})
			})
		})

		Context("[CANARY] when a pod without matching SGP is created", func() {
			BeforeEach(func() {
				podLabelValue = "dev"
			})

			It("should run without branch ENI annotation", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				verify.PodHasNoBranchENIAnnotationInjected(pod)
			})
		})

		Context("when only Service Account is used with SGP", func() {
			var sa *v1.ServiceAccount
			JustBeforeEach(func() {
				sa = manifest.NewServiceAccountBuilder().
					Namespace(namespace).
					Label(podLabelKey, podLabelValue).Build()

				pod, err = manifest.NewDefaultPodBuilder().
					Namespace(namespace).
					ServiceAccount(sa.Name).Build()
				Expect(err).NotTo(HaveOccurred())
			})

			JustAfterEach(func() {
				By("deleting a service account")
				err := frameWork.K8sClient.Delete(ctx, sa)
				Expect(err).NotTo(HaveOccurred())
			})

			Context("when only match label with Service account is used", func() {
				JustBeforeEach(func() {
					securityGroupPolicy, err = manifest.NewSGPBuilder().
						Namespace(namespace).
						ServiceAccountMatchLabel(sgpLabelKey, sgpLabelValue).
						SecurityGroup(securityGroups).Build()
					Expect(err).NotTo(HaveOccurred())
				})

				It("should get the SG from the SGP which match label", func() {
					CreateServiceAccount(sa)
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
					pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
					verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
				})
			})

			Context("when only match expression with Service account is used", func() {
				JustBeforeEach(func() {
					securityGroupPolicy, err = manifest.NewSGPBuilder().
						Namespace(namespace).
						ServiceAccountMatchExpression(sgpLabelKey, metav1.LabelSelectorOpIn, sgpLabelValue).
						SecurityGroup(securityGroups).Build()
					Expect(err).NotTo(HaveOccurred())
				})

				It("should get the SG from the SGP which match expression", func() {
					CreateServiceAccount(sa)
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
					pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
					verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
				})
			})

			Context("when both match label and match expression with Service account is used", func() {
				matchExpressionLabelKey := "environment"
				matchExpressionLabelVal := "test"

				JustBeforeEach(func() {
					sa = manifest.NewServiceAccountBuilder().
						Namespace(namespace).
						Label(podLabelKey, podLabelValue).
						Label(matchExpressionLabelKey, matchExpressionLabelVal).Build()

					securityGroupPolicy, err = manifest.NewSGPBuilder().
						Namespace(namespace).
						ServiceAccountMatchExpression(sgpLabelKey, metav1.LabelSelectorOpIn, sgpLabelValue).
						ServiceAccountMatchLabel(matchExpressionLabelKey, matchExpressionLabelVal).
						SecurityGroup(securityGroups).Build()
					Expect(err).NotTo(HaveOccurred())
				})

				It("should get the SG from the SGP which match expression and match label", func() {
					CreateServiceAccount(sa)
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
					pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
					verify.VerifyNetworkingOfPodUsingENI(*pod, securityGroups)
				})
			})
		})
	})

	Describe("Toggle Node between Managed/Un-Managed", func() {
		// targetedNodes is the list of node where the test will be run
		var targetedNodes []v1.Node
		// resourceMap is the list of resources to be allocated to the container
		var resourceMap map[v1.ResourceName]resource.Quantity
		var podTemplate *v1.Pod
		var container v1.Container
		var err error

		BeforeEach(func() {
			// for default use case we the pod-eni resource will be injected
			// by the WebHook, so create the container with empty resource limits
			resourceMap = map[v1.ResourceName]resource.Quantity{}
		})

		JustBeforeEach(func() {
			sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)

			targetedNodes = nodeList.Items[:1]

			container = manifest.NewBusyBoxContainerBuilder().
				Resources(v1.ResourceRequirements{
					Limits:   resourceMap,
					Requests: resourceMap,
				}).
				Build()

			podTemplate, err = manifest.NewDefaultPodBuilder().
				Labels(map[string]string{podLabelKey: podLabelValue}).
				Container(container).
				NodeName(targetedNodes[0].Name).
				Namespace(namespace).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when node is toggled from managed to un-managed and back to managed", func() {
			It("pod should not run when un-managed and run when managed", func() {
				node := targetedNodes[0]

				By("verifying node has CNINode present")
				cniNode, err := frameWork.NodeManager.GetCNINode(&node)
				Expect(err).ToNot(HaveOccurred())
				Expect(cniNode.Name).To(Equal(node.Name))

				// we don't support changing SGP managed node to unmanaged node
				// after using CNINode, no longer like node label the feature in CNINode Spec shouldn't be modified
				// only run this test for old label based mode
				if !lo.ContainsBy(cniNode.Spec.Features, func(addedFeature cninode.Feature) bool {
					return addedFeature.Name == cninode.SecurityGroupsForPods
				}) {
					if _, found := node.Labels[config.HasTrunkAttachedLabel]; found {
						// This should never happens as once the trunk is attached,
						// this label will not be removed again. This is for testing
						// purposes to make a managed node an un-managed node
						By("removing the has-trunk-attached label from the node")
						err = frameWork.NodeManager.RemoveLabels(targetedNodes,
							map[string]string{config.HasTrunkAttachedLabel: "true"})
						Expect(err).ToNot(HaveOccurred())

						firstPod := podTemplate.DeepCopy()
						By("creating a Pod on the un-managed node and verifying it fails")
						_, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, firstPod, utils.ResourceCreationTimeout)
						Expect(err).To(HaveOccurred())

						By("deleting the pod")
						err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, firstPod)
						Expect(err).ToNot(HaveOccurred())

						// Currently we wait for some time before removing the trunk from cache
						// to allow evicted Pods's event to be received and their Branch ENIs be
						// removed. In this period if we try to make the node managed again, it will
						// fail
						time.Sleep(branch.NodeDeleteRequeueRequestDelay)

						By("adding the has trunk ENI label")
						err = frameWork.NodeManager.AddLabels(targetedNodes,
							map[string]string{config.HasTrunkAttachedLabel: "true"})
						Expect(err).ToNot(HaveOccurred())

						By("creating the Pod on now managed node and verify it runs")
						secondPod := podTemplate.DeepCopy()
						secondPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, secondPod, utils.ResourceCreationTimeout)
						Expect(err).ToNot(HaveOccurred())

						verify.VerifyNetworkingOfPodUsingENI(*secondPod, []string{securityGroupID1})

					}
				}
			})
		})

		Context("[LOCAL] when pod is created when the controller is down", func() {

			BeforeEach(func() {
				// We are explicitly adding the limits for this test, because we are removing
				// both controllers (and WebHook), in production we would expect one of the
				// functional WebHook to inject this annotation in the HA setup
				resourceMap = map[v1.ResourceName]resource.Quantity{
					config.ResourceNamePodENI: resource.MustParse("1"),
				}
			})

			It("pod should be created on startup", func() {
				By("scaling the controller deployment to 0")
				controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 0)
				pod := podTemplate.DeepCopy()

				By("creating pod which should not run since controller is down")
				_, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, pod, time.Second*10)
				Expect(err).To(HaveOccurred())

				By("scaling the controller deployment to 2")
				controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 2)

				By("waiting for leader lease to be acquired")
				time.Sleep(ControllerInitWaitPeriod)

				By("verifying the Pod is running with Branch ENI")
				verify.VerifyNetworkingOfAllPodUsingENI(namespace, podLabelKey, podLabelValue,
					[]string{securityGroupID1})
			})
		})
	})
})

func CreateServiceAccount(serviceAccount *v1.ServiceAccount) {
	By("create a service account")
	err := frameWork.K8sClient.Create(ctx, serviceAccount)
	Expect(err).NotTo(HaveOccurred())
}
