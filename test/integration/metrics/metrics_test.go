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
	"fmt"
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/controller"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	v1 "k8s.io/api/core/v1"
)

var (
	releasedVersionDescribeCallVolume float64
	testingVersionDescribeCallVolume  float64
	releasedVersionMallocBytes        float64
	testingVersionMallocBytes         float64
	releasedVersionGoroutines         float64
	testingVersionGoroutines          float64
	testingVersionImage               string
	cmd                               []string
	releasedImgTag                    string

	waitTillControllerLoaded = time.Minute * 2
)

var _ = Describe("VPC Resource Controller Metrics Tests", func() {
	Describe("[LOCAL]Test controller key metrics change", func() {
		var stdout string
		Context("Get Released RC metrics", func() {
			It("Use configured released RC image to get call volume", func() {
				controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 0)

				imageUrl := strings.Split(testingVersionImage, ":")[0]
				By(fmt.Sprintf("Testing using released image tag %s:%s\n", imageUrl, releasedImgTag))
				err := frameWork.DeploymentManager.UpdateDeploymentImage(ctx, controller.Namespace, controller.DeploymentName, fmt.Sprintf("%s:%s", imageUrl, releasedImgTag))
				controller.ScaleControllerDeployment(ctx, frameWork.DeploymentManager, 1)
				time.Sleep(waitTillControllerLoaded)
				controllerPod, err := getControllerPodFromLabels(controller.Namespace, controller.PodLabelKey, controller.PodLabelVal)
				controllerIP := controllerPod.Status.PodIP

				cmd = []string{
					"curl",
					"http://" + controllerIP + ":8443/metrics",
				}

				stdout, _, err = frameWork.PodManager.PodExec(controller.Namespace, curlPod.Name, cmd)
				Expect(err).NotTo(HaveOccurred())
			})

			It("Use configured released RC image to get call volume", func() {
				metricsENI, err := utils.RetrieveTestedMetricValue([]byte(stdout), describeENICallCount, dto.MetricType_COUNTER)
				Expect(err).NotTo(HaveOccurred())
				releasedVersionDescribeCallVolume = metricsENI[0].GetCounter().GetValue()
			})

			It("Use configured released RC image to get MEM allocated", func() {
				metricsMEM, err := utils.RetrieveTestedMetricValue([]byte(stdout), totalMemAllocatedBytes, dto.MetricType_COUNTER)
				Expect(err).NotTo(HaveOccurred())
				releasedVersionMallocBytes = metricsMEM[0].GetCounter().GetValue()
			})

			It("Use configured released RC image to get goroutines", func() {
				metricsRoutines, err := utils.RetrieveTestedMetricValue([]byte(stdout), goRoutineCount, dto.MetricType_GAUGE)
				Expect(err).NotTo(HaveOccurred())
				releasedVersionGoroutines = metricsRoutines[0].GetCounter().GetValue()
			})
		})

		Context("Get Testing RC metrics", func() {
			It("Use testing RC image to get call volume", func() {
				By(fmt.Sprintf("Testing using image built on master is %s", testingVersionImage))
				err := frameWork.DeploymentManager.UpdateDeploymentImage(ctx, controller.Namespace, controller.DeploymentName, testingVersionImage)
				By("Waiting new version of controller starting and working through all loads")
				time.Sleep(waitTillControllerLoaded)
				controllerPod, err := getControllerPodFromLabels(controller.Namespace, controller.PodLabelKey, controller.PodLabelVal)
				Expect(err).NotTo(HaveOccurred())
				controllerIP := controllerPod.Status.PodIP
				cmd = []string{
					"curl",
					"http://" + controllerIP + ":8443/metrics",
				}

				stdout, _, err = frameWork.PodManager.PodExec(controller.Namespace, curlPod.Name, cmd)

				Expect(err).NotTo(HaveOccurred())
				metricsENI, err := utils.RetrieveTestedMetricValue([]byte(stdout), describeENICallCount, dto.MetricType_COUNTER)
				Expect(err).NotTo(HaveOccurred())
				testingVersionDescribeCallVolume = metricsENI[0].GetCounter().GetValue()
			})

			It("Use testing RC image to get MEM allocated", func() {
				metricsMEM, err := utils.RetrieveTestedMetricValue([]byte(stdout), totalMemAllocatedBytes, dto.MetricType_COUNTER)
				Expect(err).NotTo(HaveOccurred())
				testingVersionMallocBytes = metricsMEM[0].GetCounter().GetValue()
			})

			It("Use testing RC image to get goroutines", func() {
				metricsRoutines, err := utils.RetrieveTestedMetricValue([]byte(stdout), goRoutineCount, dto.MetricType_GAUGE)
				Expect(err).NotTo(HaveOccurred())
				testingVersionGoroutines = metricsRoutines[0].GetCounter().GetValue()
			})
		})

		Context("Current testing version of controller should have metrics in expected range", func() {
			It("Describe ENI API call shouldn't exceed 2 times", func() {
				Expect(testingVersionDescribeCallVolume).ShouldNot(BeNumerically(">",
					releasedVersionDescribeCallVolume*testingMetrics[describeENICallCount].MetricsThreshold))
			})

			It("Current testing version of controller shouldn't use 50% more memory under same settings", func() {
				Expect(testingVersionMallocBytes).ShouldNot(BeNumerically(">",
					releasedVersionMallocBytes*testingMetrics[totalMemAllocatedBytes].MetricsThreshold))
			})

			It("Current testing version of controller shouldn't create 25% more routines under same settings", func() {
				Expect(testingVersionGoroutines).ShouldNot(BeNumerically(">",
					releasedVersionGoroutines*testingMetrics[goRoutineCount].MetricsThreshold))
			})
		})
	})
})

func getControllerPodFromLabels(namespace string, labelKey string, labelValue string) (*v1.Pod, error) {
	if pods, err := frameWork.PodManager.GetPodsWithLabel(ctx, namespace, labelKey, labelValue); err != nil {
		return nil, err
	} else {
		pod := &pods[0]
		for {
			if pod.Status.Phase == v1.PodRunning {
				break
			}

			time.Sleep(utils.PollIntervalShort)
		}
		return pod, nil
	}
}
