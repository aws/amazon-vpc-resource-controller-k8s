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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// TODO: Add integration test for Windows when ConfigMap feature is implemented.
var _ = Describe("when doing pod operations from non vpc-resource-controller user", func() {
	Context("when updating annotations", func() {
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

	Context("when creating new pod", func() {
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
})
