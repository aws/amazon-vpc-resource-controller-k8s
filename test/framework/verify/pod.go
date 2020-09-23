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

package verify

import (
	"context"

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

func (v *PodVerification) PodHasExpectedSG(pod *v1.Pod, expectedSecurityGroup []string) []*trunk.ENIDetails {
	By("getting the branch ENI from the pod's annotation")
	eniDetails, err := v.frameWork.PodManager.GetENIDetailsFromPodAnnotation(pod.Annotations)
	Expect(err).NotTo(HaveOccurred())

	By("getting the security group for the ENI from AWS EC2 ")
	actualSG, err := v.frameWork.EC2Manager.GetENISecurityGroups(eniDetails[0].ID)
	Expect(err).NotTo(HaveOccurred())

	Expect(expectedSecurityGroup).Should(ConsistOf(actualSG))

	By("getting the same IP address as the branch ENI IP")
	Expect(eniDetails[0].IPV4Addr).To(Equal(pod.Status.PodIP))

	return eniDetails
}

func (v *PodVerification) PodsHaveExpectedSG(namespace string, podLabelKey string, podLabelVal string,
	expectedSecurityGroup []string) {

	By("getting the pod belonging to the deployment")
	pods, err := v.frameWork.PodManager.GetPodsWithLabel(v.ctx, namespace, podLabelKey, podLabelVal)
	Expect(err).ToNot(HaveOccurred())

	for _, pod := range pods {
		v.PodHasExpectedSG(&pod, expectedSecurityGroup)
	}
}

func (v *PodVerification) PodHasNoBranchENIAnnotationInjected(pod *v1.Pod) {
	By("getting the branch ENI from the pod's annotation")
	_, hasNoAnnotation := pod.Annotations[config.ResourceNamePodENI]
	Expect(hasNoAnnotation).To(BeFalse(), "Pod shouldn't have branch ENI annotations.")
}
