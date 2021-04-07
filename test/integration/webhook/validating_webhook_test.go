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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("when updating security group annotation from unauthorized user", func() {
	It("should fail on updating the annotation", func() {
		newPod := pod.DeepCopy()
		newPod.Annotations[config.ResourceNamePodENI] = "updated-annotation"

		err := frameWork.PodManager.PatchPod(ctx, pod, newPod)
		Expect(err).To(HaveOccurred())
	})

	It("should fail on deleting the annotation", func() {
		newPod := pod.DeepCopy()
		delete(newPod.Annotations, config.ResourceNamePodENI)

		err := frameWork.PodManager.PatchPod(ctx, pod, newPod)
		Expect(err).To(HaveOccurred())
	})

	It("should go through when modifying other annotations", func() {
		newPod := pod.DeepCopy()
		newPod.Annotations["some-other-annotation"] = "new-annotation"

		err := frameWork.PodManager.PatchPod(ctx, pod, newPod)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail on creating new pod with annotation", func() {
		pod, err = manifest.NewDefaultPodBuilder().
			Annotations(map[string]string{config.ResourceNamePodENI: "new-annotation"}).
			Namespace(namespace).
			Build()
		Expect(err).ToNot(HaveOccurred())

		_, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, pod)
		Expect(err).To(HaveOccurred())
	})
})
