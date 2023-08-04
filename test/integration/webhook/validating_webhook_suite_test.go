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
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	sgpWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

var frameWork *framework.Framework
var securityGroupID string
var ctx context.Context
var err error

var namespace = "per-pod-sg"
var podMatchLabelKey = "role"
var podMatchLabelVal = "test"
var pod *v1.Pod
var sgp *v1beta1.SecurityGroupPolicy

func TestValidatingWebHook(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Validating WebHook Test Suite")
}

var _ = BeforeSuite(func() {
	frameWork = framework.New(framework.GlobalOptions)
	ctx = context.Background()

	securityGroupID, err = frameWork.EC2Manager.CreateSecurityGroup(utils.ResourceNamePrefix + "sg")
	Expect(err).ToNot(HaveOccurred())

	By("creating the namespace")
	err := frameWork.NSManager.CreateNamespace(ctx, namespace)
	Expect(err).ToNot(HaveOccurred())

	sgp, err = manifest.NewSGPBuilder().
		Namespace(namespace).
		PodMatchLabel(podMatchLabelKey, podMatchLabelVal).
		SecurityGroup([]string{securityGroupID}).Build()
	Expect(err).NotTo(HaveOccurred())

	By("creating the security group policy")
	sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, sgp)

	By("creating a pod with branch ENI")
	pod, err = manifest.NewDefaultPodBuilder().
		Labels(map[string]string{podMatchLabelKey: podMatchLabelVal}).
		Namespace(namespace).
		Build()
	Expect(err).ToNot(HaveOccurred())

	pod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, pod, utils.ResourceCreationTimeout)
	Expect(err).ToNot(HaveOccurred())

	By("verifying the pod eni annotation is present on branch pod")
	Expect(pod.Annotations).To(HaveKey(config.ResourceNamePodENI))
})

var _ = AfterSuite(func() {
	By("deleting the pod")
	if pod != nil {
		Expect(frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, pod)).To(Succeed())
	}

	By("deleting the security group policy")
	if sgp != nil {
		Expect(frameWork.SGPManager.DeleteAndWaitTillSecurityGroupIsDeleted(ctx, sgp)).To(Succeed())
	}

	By("deleting the namespace")
	Expect(frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)).To(Succeed())

	By("deleting the security group from ec2")
	if securityGroupID != "" {
		Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID)).To(Succeed())
	}
})
