// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//	http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package scale_test

import (
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	deploymentWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/deployment"
	sgpWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/apps/v1"
)

var _ = Describe("Security group per pod scale test", func() {
	var (
		sgpLabelKey         string
		sgpLabelValue       string
		securityGroups      []string
		securityGroupPolicy *v1beta1.SecurityGroupPolicy
		err                 error
	)

	BeforeEach(func() {
		sgpLabelKey = "role"
		sgpLabelValue = "db"
		securityGroups = []string{securityGroupID}
	})

	JustBeforeEach(func() {
		// create SGP
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
	})

	Describe("creating deployment", func() {
		var deployment *v1.Deployment

		JustBeforeEach(func() {
			deployment = manifest.NewDefaultDeploymentBuilder().
				Namespace(namespace).
				Replicas(1000).
				PodLabel(sgpLabelKey, sgpLabelValue).Build()
		})

		JustAfterEach(func() {
			By("deleting the deployment")
			err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)
			Expect(err).ToNot(HaveOccurred())
			time.Sleep(time.Minute) // allow time for pods to terminate
		})

		Context("when deployment is created", func() {
			It("should have all the pods running", MustPassRepeatedly(3), func() {
				start := time.Now()
				sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, securityGroupPolicy)
				deploymentWrapper.
					CreateAndWaitForDeploymentToStart(frameWork.DeploymentManager, ctx, deployment)
				duration := time.Since(start)
				verify.VerifyNetworkingOfAllPodUsingENI(namespace, sgpLabelKey, sgpLabelValue,
					securityGroups)
				Expect(duration.Minutes()).To(BeNumerically("<", 5.5))
			})
		})
	})

})
