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

package metrics

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/controller"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

var frameWork *framework.Framework
var ctx context.Context
var curlPod *v1.Pod
var testingMetrics map[string]TestMetric

func TestAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Per Pod Security Group Suite")
}

var _ = BeforeSuite(func() {
	By("creating a framework")
	frameWork = framework.New(framework.GlobalOptions)

	ctx = context.Background()
	image, err := getCurrentControllerImage()
	Expect(err).NotTo(HaveOccurred())
	testingVersionImage = image
	releasedImgTag = frameWork.Options.ReleasedImageVersion
	fmt.Printf("Release image tag flag is set to %s in BeforeSuite\n", releasedImgTag)
	Expect(testingVersionImage).ShouldNot(Equal(""))
	Expect(releasedImgTag).ShouldNot(Equal(""))
	Expect(strings.Split(testingVersionImage, ":")[1]).ShouldNot(Equal(releasedImgTag))
	curlPod, err = createCurlPod()
	Expect(err).NotTo(HaveOccurred())
	testingMetrics = NewTestingMetrics()
	err = ensureControllerReadyTobeScraped()
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	By("Restore testing environment")
	err := frameWork.DeploymentManager.UpdateDeploymentImage(ctx, controller.Namespace, controller.DeploymentName, testingVersionImage)
	Expect(err).NotTo(HaveOccurred())
	err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, curlPod)
	Expect(err).NotTo(HaveOccurred())
})

func getCurrentControllerImage() (string, error) {
	if deployment, err := frameWork.DeploymentManager.GetDeployment(ctx, controller.Namespace, controller.DeploymentName); err != nil {
		return "", err
	} else {
		return deployment.Spec.Template.Spec.Containers[0].Image, nil
	}
}

func createCurlPod() (*v1.Pod, error) {
	container := manifest.NewBusyBoxContainerBuilder().Image("curlimages/curl").Name("curl").Build()
	pod, err := manifest.NewDefaultPodBuilder().Name("curl").Namespace(controller.Namespace).Container(container).Build()
	if err != nil {
		return nil, err
	}
	return frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, pod, utils.ResourceCreationTimeout)
}

func ensureControllerReadyTobeScraped() error {
	if deployment, err := frameWork.DeploymentManager.GetDeployment(ctx, controller.Namespace, controller.DeploymentName); err != nil {
		return err
	} else {
		for _, port := range deployment.Spec.Template.Spec.Containers[0].Ports {
			if port.ContainerPort == 8443 {
				return nil
			}
		}
		// If the metrics endpoint is not created, we should create it for following tests.
		newController := deployment.DeepCopy()
		newController.Spec.Template.Spec.Containers[0].Args = append(
			newController.Spec.Template.Spec.Containers[0].Args, "--metrics-bind-address=:8443")
		port := v1.ContainerPort{
			Name:          "metrics",
			ContainerPort: 8443,
			Protocol:      "TCP",
		}
		newController.Spec.Template.Spec.Containers[0].Ports = append(
			newController.Spec.Template.Spec.Containers[0].Ports, port)
		if err := frameWork.DeploymentManager.PatchDeployment(ctx, newController, deployment); err != nil {
			return err
		}
	}
	return nil
}
