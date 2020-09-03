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

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

		//Context("when a pod with more than one matching SGP is created", func() {q
		//	It("should run with Branch ENI IP with the SG from all SGP", func() {
		//	})
		//})

		//Context("when a pod with no matching SGP is created", func() {
		//	It("should run with secondary IP", func() {
		//	})
		//})

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
						ServiceAccountMatchExpression(sgpLabelKey, v12.LabelSelectorOpIn, sgpLabelValue).
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
						ServiceAccountMatchExpression(sgpLabelKey, v12.LabelSelectorOpIn, sgpLabelValue).
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
