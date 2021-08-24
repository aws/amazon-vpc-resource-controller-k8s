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

package verify

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

type PodVerification struct {
	frameWork *framework.Framework
	ctx       context.Context
}

func NewPodVerification(framework *framework.Framework, ctx context.Context) *PodVerification {
	return &PodVerification{
		frameWork: framework,
		ctx:       ctx,
	}
}

func (v *PodVerification) verifyPodIPEqualsENIIP(pod v1.Pod) {
	By("getting the branch ENI from the pod's annotation")
	eniDetails, err := v.frameWork.PodManager.GetENIDetailsFromPodAnnotation(pod.Annotations)
	Expect(err).NotTo(HaveOccurred())

	By(fmt.Sprintf("Verifying for ENI ID: %s ENI IP: %s Pod Name: %s/%s Pod's IP: %s",
		eniDetails[0].ID, eniDetails[0].IPV4Addr, pod.Namespace, pod.Name, pod.Status.PodIP))

	Expect(eniDetails[0].IPV4Addr).To(Equal(pod.Status.PodIP))
}

func (v *PodVerification) VerifyNetworkingOfPodUsingENI(pod v1.Pod, expectedSecurityGroup []string) []*trunk.ENIDetails {
	v.verifyPodIPEqualsENIIP(pod)

	eniDetails, err := v.frameWork.PodManager.GetENIDetailsFromPodAnnotation(pod.Annotations)
	Expect(err).NotTo(HaveOccurred())

	By("getting the security group for the ENI from AWS EC2")
	actualSG, err := v.frameWork.EC2Manager.GetENISecurityGroups(eniDetails[0].ID)
	Expect(err).NotTo(HaveOccurred())
	Expect(expectedSecurityGroup).Should(ConsistOf(actualSG))

	return eniDetails
}

func (v *PodVerification) VerifyNetworkingOfAllPodUsingENI(namespace string, podLabelKey string,
	podLabelVal string, expectedSecurityGroup []string) {

	By("getting the pod belonging to the deployment")
	pods, err := v.frameWork.PodManager.GetPodsWithLabel(v.ctx, namespace, podLabelKey, podLabelVal)
	Expect(err).ToNot(HaveOccurred())

	if len(pods) == 0 {
		panic(fmt.Errorf("failed to find any pod with label %s:%s in ns %s",
			podLabelKey, podLabelVal, namespace))
	}

	for _, pod := range pods {
		v.VerifyNetworkingOfPodUsingENI(pod, expectedSecurityGroup)
	}
}

func (v *PodVerification) VerifyPodENIDeletedForAllPods(namespace string,
	podLabelKey string, podLabelVal string) {

	By("getting the pod belonging to the deployment")
	pods, err := v.frameWork.PodManager.GetPodsWithLabel(v.ctx, namespace, podLabelKey, podLabelVal)
	Expect(err).ToNot(HaveOccurred())

	if len(pods) == 0 {
		panic(fmt.Errorf("failed to find any pod with label %s:%s in ns %s",
			podLabelKey, podLabelVal, namespace))
	}

	for _, pod := range pods {
		v.VerifyPodENIDeleted(pod)
	}
}

func (v *PodVerification) VerifyPodENIDeleted(pod v1.Pod) {
	v.verifyPodIPEqualsENIIP(pod)

	eniDetails, err := v.frameWork.PodManager.GetENIDetailsFromPodAnnotation(pod.Annotations)
	Expect(err).NotTo(HaveOccurred())

	By("verifying the ENI is deleted")
	_, err = v.frameWork.EC2Manager.GetENISecurityGroups(eniDetails[0].ID)
	Expect(err).To(HaveOccurred())
}

func (v *PodVerification) PodHasNoBranchENIAnnotationInjected(pod *v1.Pod) {
	By("getting the branch ENI from the pod's annotation")
	_, hasNoAnnotation := pod.Annotations[config.ResourceNamePodENI]
	Expect(hasNoAnnotation).To(BeFalse(), "Pod shouldn't have branch ENI annotations.")
}

func (v *PodVerification) WindowsPodsHaveExpectedIPv4Address(namespace string,
	podLabelKey string, podLabelVal string) {

	pods, err := v.frameWork.PodManager.GetPodsWithLabel(v.ctx, namespace, podLabelKey, podLabelVal)
	Expect(err).ToNot(HaveOccurred())

	for _, pod := range pods {
		v.WindowsPodHaveExpectedIPv4Address(&pod)
	}
}

func (v *PodVerification) WindowsPodHaveExpectedIPv4Address(pod *v1.Pod) {
	By("matching the IPv4 from annotation to the pod IP")
	ipAddWithCidr, found := pod.Annotations["vpc.amazonaws.com/PrivateIPv4Address"]
	Expect(found).To(BeTrue())
	// Remove the CIDR and compare pod IP with the IP annotated from VPC Controller
	Expect(pod.Status.PodIP).To(Equal(strings.Split(ipAddWithCidr, "/")[0]))
}
