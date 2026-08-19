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

/*
==============================================================================
新增集成测试用例说明（审阅用，确认无误后提交前请删除本注释块）
用例：Context("when a branch ENI pod is deleted and a replacement is created on a trunk at full capacity")
对应：M1 机制（design-cn.md §2.2）/ requirements.md 场景 S2

【为什么加这个用例】
S2 要验证的两条核心断言，此前只有单元测试（TestTrunkENI_U1_VlanReuseCooldown、
TestTrunkENI_RegressionE5）在 mock 层面覆盖。手动在大集群上跑靠日志/metric 间接推断，
不确定、噪音大、分不清是代码问题还是环境问题。这个集成用例在真实 EC2 + 真实 K8s 上，
用行为层面的断言（不看日志、不解析 metric）确定性地验证同一逻辑。

【关键设计点：必须先把节点填到满容量，否则 slot 释放这条断言测不出问题】
初版设计只有 1 个测试 pod，删除后立即建第二个——但节点远没到容量上限（m5.xlarge 上限 18），
这种情况下第二个 pod 能不能起来，跟第一个 pod 的 slot 有没有"立即"释放完全无关：不管释放
与否，节点上都还有大把空闲名额。哪怕 M1 的立即释放逻辑整个坏掉（slot 要等满 60 秒 cooldown
才放），这个测试依然会 PASS——是假阳性，测不出真正的问题。

修正：先用 filler pod 把目标节点填到 (branch-ENI 上限 - 1)，让测试 pod 成为刚好把节点填满
的第 N 个。这样删除测试 pod 后，第二个 pod 能否 Running 才真正依赖"那个 slot 是否被立即释
放"——若释放逻辑坏了，节点在 controller 眼里仍然是满的，第二个 pod 会卡在 Pending
（ErrCurrentlyAtMaxCapacity），断言会失败，测试才有意义。这个做法与单元测试
TestTrunkENI_RegressionE5（先填 limit-1 个 filler，再测第 18 个的删除/替换）是同一个思路，
只是从内存态搬到了真实 EC2/K8s。

【场景步骤】
1. 建 SGP。查询目标节点（nodeList 第一个节点）的 instance type，算出它的 branch-ENI 容量
   上限 branchLimit（vpc.Limits[instanceType].BranchInterface）。
2. 用 branchLimit-1 个 filler pod（NodeName 钉在目标节点）把该节点填到只差一个名额就满，
   逐个等待 Running 并验证拿到了 branch ENI。
3. 创建"测试 pod”（同样钉在该节点），此时节点达到满容量。记录它拿到的 branch ENI 的
   vlanID（freedVlan）和 eniID（freedENIID）。
4. 删除测试 pod，释放它占用的 trunk slot——此时节点从"满容量"变成"差一个测试 pod”。
5. 立刻（在 VLAN reuse cooldown 窗口内，默认 60 秒）在同一节点创建"替换 pod”，等待 Running。
6. 记录替换 pod 拿到的 branch ENI 的 vlanID / eniID。

【预期结果】
- slot 立即释放（M1 断言一）：替换 pod 能在 cooldown 未过、且节点刚好是"满容量减一"的
  情况下成功 Running。这是本用例修正后才具备的确定性：若 slot 不是"一个 reconcile pass
  内立即释放”、而是要等满 cooldown，替换 pod 会因为 controller 仍然认为节点满容量而卡在
  Pending，测试会在这一步失败，不会有假阳性。
- VLAN cooldown 内不复用（M1 断言二）：替换 pod 拿到的 vlanID ≠ freedVlan，
  且 eniID ≠ freedENIID。证明刚释放的那个 VLAN 号在冷却期内没有被过早重新分配。
- 最后 WaitTillTheENIIsDeleted(freedENIID) 成功：确认测试 pod 的 branch ENI 最终被清理，
  不泄漏。

【注意】
- 默认 reuseCooldown = 60 秒（cooldown.DefaultCoolDownPeriod）。本用例的替换 pod 是在
  冷却期"内"创建的，靠"拿到不同 VLAN"来验证不复用，因此不需要真的等满 60 秒，用例耗时
  主要花在 filler pod 的创建/等待上（branchLimit-1 个，视目标节点 instance type 而定）。
- 若未来想额外验证"冷却期过后该 VLAN 能被重新分配"，需要 sleep > 60 秒后再建第三个 pod
  并断言它这次可以拿到 freedVlan——但那样单个用例会明显变慢，且该点已被单元测试
  TestTrunkENI_U1_VlanReuseCooldown 精确覆盖（31 秒边界），故这里不重复。
- filler pod 数量取决于目标节点的真实 instance type（读 EC2 DescribeInstances 得到），
  不是硬编码 18——这样测试在不同 instance type 的节点上跑都成立。
==============================================================================
*/
