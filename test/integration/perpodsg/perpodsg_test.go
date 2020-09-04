/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package perpodsg_test

import (
	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	deploymentWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/deployment"
	podWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/pod"
	sgpWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
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
				deploymentWrapper.CreateAndWaitForDeploymentToStart(frameWork.DeploymentManager, ctx, deployment)
				verify.PodsHaveExpectedSG(namespace, podLabelKey, podLabelValue, securityGroups)
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
				verify.PodHasExpectedSG(pod, securityGroups)
			})
		})

		Context("when a pod in default namespace is created", func() {
			BeforeEach(func() {
				namespace = "default"
			})
			It("should get the SG from SGP in default namespace", func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				verify.PodHasExpectedSG(pod, securityGroups)
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
				eniList = verify.PodHasExpectedSG(pod, securityGroups)
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

			Context("when these SGPs have different security groups", func() {
				It("should run with Branch ENI IP with all security groups from all SGPs", func() {
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
					sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy2)
					pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
					verify.PodHasExpectedSG(pod, append(securityGroups, securityGroupID2))
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
					verify.PodHasExpectedSG(pod, []string{securityGroupID1, securityGroupID2})
				})
			})
		})

		Context("when a SGP has expression selector", func() {
			var (
				sgpExpressionKey   = "environment"
				sgpExpressionValue = []string{"qa", "production"}
			)
			Context("when the SGP has only expression selector and a pod matches the expression", func() {
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
					verify.PodHasExpectedSG(pod, securityGroups)
				})
			})

			Context("when the SGP has label selector and expression selector", func() {
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
						verify.PodHasExpectedSG(pod, securityGroups)
					})
				})
			})
		})

		Context("when adding new security group to a existing SGP", func() {
			JustBeforeEach(func() {
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
			})

			It("a new pod should run with all security groups", func() {
				sgpWrapper.UpdateSecurityGroupPolicy(
					frameWork.K8sClient, ctx, securityGroupPolicy, []string{securityGroupID1, securityGroupID2},
				)
				pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
				By("the pod has two sgs")
				verify.PodHasExpectedSG(pod, []string{securityGroupID1, securityGroupID2})
			})
		})

		Context("when a pod without matching SGP is created", func() {
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
					verify.PodHasExpectedSG(pod, securityGroups)
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
					verify.PodHasExpectedSG(pod, securityGroups)
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
					verify.PodHasExpectedSG(pod, securityGroups)
				})
			})
		})
	})
})

func CreateServiceAccount(serviceAccount *v1.ServiceAccount) {
	By("create a service account")
	err := frameWork.K8sClient.Create(ctx, serviceAccount)
	Expect(err).NotTo(HaveOccurred())
}
