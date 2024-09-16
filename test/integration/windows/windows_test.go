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

package windows_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	configMapWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/configmap"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsV1 "k8s.io/api/apps/v1"
	batchV1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
)

var _ = Describe("Windows Integration Test", func() {
	var (
		namespace string

		// pod label kay and value which can be used to
		// target pods to run behind a service and to
		// get a list of pods having to key value pair
		// for verification
		podLabelKey string
		podLabelVal string

		// Tester Container that can be used to test network
		// connectivity to a pod/service etc
		testerContainer         v1.Container
		testerContainerCommands []string

		// Tester container can be wrapped in job to run parallel
		// jobs and monitor the status of the jobs to verify if
		// tests succeeded or failed
		job            *batchV1.Job
		jobParallelism int

		//test enabling/disabling Windows IPAM
		data          map[string]string
		testConfigMap v1.ConfigMap
	)

	var sleepInfinityContainerCommands = []string{
		GetCommandToTestHostConnectivity(
			"www.amazon.com", 80, 2, true,
		),
	}

	BeforeEach(func() {
		namespace = "windows-test"
		jobParallelism = 1
		podLabelKey = "role"
		podLabelVal = "integration-test"
		data = map[string]string{config.EnableWindowsIPAMKey: "true"}
	})

	JustBeforeEach(func() {
		frameWork.NSManager.CreateNamespace(ctx, namespace)

		testerContainer = manifest.NewWindowsContainerBuilder().
			Args(testerContainerCommands).
			Build()

		job = manifest.NewWindowsJob().
			Parallelism(jobParallelism).
			PodLabels(podLabelKey, podLabelVal).
			Container(testerContainer).
			Build()
	})

	JustAfterEach(func() {
		// Will clean up all the resources used by a test
		frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)
	})

	Describe("configMap enable-windows-ipam tests", func() {
		// Test windows IPAM feature enable/disable. When feature enabled, pod must have
		// resource limits injected. Otherwise, resources limits must not be injected.
		var testPod *v1.Pod
		var createdPod *v1.Pod
		BeforeEach(func() {
			testPod, err = manifest.NewWindowsPodBuilder().Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when configmap created", func() {
			JustBeforeEach(func() {
				// Update configmap for tests
				testConfigMap = *manifest.NewConfigMapBuilder().Data(data).Build()
				configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, &testConfigMap)
			})

			JustAfterEach(func() {
				// restore the configmap after each test
				testConfigMap = *manifest.NewConfigMapBuilder().Build()
				configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, &testConfigMap)

				err := frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
				Expect(err).ToNot(HaveOccurred())
			})

			Context("[CANARY] when enable-windows-ipam is True", func() {
				It("pod should be running and have resourceLimit injected", func() {
					By("creating pod and waiting for ready")
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)
				})
			})

			Context("when enable-windows-ipam is incorrect", func() {
				BeforeEach(func() {
					data = map[string]string{config.EnableWindowsIPAMKey: "wrongVal"}
				})
				It("pod should not be running and should not have resource limits", func() {
					By("creating pod and waiting for timeout")
					createdPod, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).To(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, false)
				})
			})

			Context("When data is missing", func() {
				BeforeEach(func() {
					data = map[string]string{}
				})
				It("pod should not be running and should not have resource limits", func() {
					By("creating pod and wait for timeout")
					createdPod, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).To(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, false)
				})
			})

			Context("when enable-windows-ipam is set to true but old controller deployment exists", func() {
				It("pod should fail to create", func() {
					By("creating a dummy deployment for vpc-resource-controller")
					oldControllerDeployment := manifest.NewDefaultDeploymentBuilder().
						Namespace(config.KubeSystemNamespace).
						Name(config.OldVPCControllerDeploymentName).
						PodLabel("app", "vpc-resource-controller").
						Replicas(1).
						Build()
					_, err = frameWork.DeploymentManager.
						CreateAndWaitUntilDeploymentReady(ctx, oldControllerDeployment)
					Expect(err).ToNot(HaveOccurred())

					By("creating windows pod and waiting for it to timout")
					createdPod, err := frameWork.PodManager.
						CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).To(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, false)

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, createdPod)
					Expect(err).ToNot(HaveOccurred())

					By("deleting the old controller dummy deployment")
					err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx,
						oldControllerDeployment)
					Expect(err).ToNot(HaveOccurred())

					By("creating windows pod and waiting for it to run")
					testPod, err = manifest.NewWindowsPodBuilder().Build()
					Expect(err).ToNot(HaveOccurred())

					createdPod, err = frameWork.PodManager.
						CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)
				})
			})
		})

		Context("[CANARY] when configmap not created", func() {
			JustBeforeEach(func() {
				// Delete configmap created in BeforeSuite to test
				configMapWrapper.DeleteConfigMap(frameWork.ConfigMapManager, ctx, configMap)
			})
			JustAfterEach(func() {
				// Create the default configmap to continue tests
				defaultConfigMap := manifest.NewConfigMapBuilder().Build()
				configMapWrapper.CreateConfigMap(frameWork.ConfigMapManager, ctx, defaultConfigMap)
			})
			It("pod should not be running and should not have resource limits", func() {
				By("creating pod and waiting for timeout")
				createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
				Expect(err).To(HaveOccurred())
				verify.WindowsPodHaveResourceLimits(createdPod, false)
			})
		})
	})

	Describe("configMap secondary IP mode tests", Label("windows-prefix-delegation-disabled"), func() {
		// Test workflow when windows prefix delegation is disabled and secondary IP mode is active
		// In secondary IP mode, pods must have secondary IPs assigned
		var testPod, testPod2, testPod3 *v1.Pod
		var instanceID string
		var nodeName string
		var bufferForCoolDown = config.CoolDownPeriod + (time.Second * 5)
		var poolReconciliationWaitTime = time.Second * 5
		container := manifest.NewWindowsContainerBuilder().Args(sleepInfinityContainerCommands).Build()

		data = map[string]string{
			config.EnableWindowsIPAMKey:             "true",
			config.EnableWindowsPrefixDelegationKey: "false",
		}
		testerContainerCommands = []string{
			GetCommandToTestHostConnectivity("www.amazon.com", 80, 2, false),
		}

		JustBeforeEach(func() {
			windowsNodeList = node.GetNodeAndWaitTillCapacityPresent(frameWork.NodeManager,ctx, "windows",
				config.ResourceNameIPAddress)
			instanceID = manager.GetNodeInstanceID(&windowsNodeList.Items[0])
			nodeName = windowsNodeList.Items[0].Name

			testPod = generateTestPodSpec(1, container, nodeName)
			testPod2 = generateTestPodSpec(2, container, nodeName)
			testPod3 = generateTestPodSpec(3, container, nodeName)

			data[config.EnableWindowsIPAMKey] = "true"
			data[config.EnableWindowsPrefixDelegationKey] = "false"
			updateConfigMap(data, poolReconciliationWaitTime)

			GinkgoWriter.Printf("Waiting %d seconds for cooldown period...\n", int(bufferForCoolDown.Seconds()))
			time.Sleep(bufferForCoolDown)
		})

		AfterEach(func() {
			data = map[string]string{
				config.EnableWindowsIPAMKey:             "true",
				config.EnableWindowsPrefixDelegationKey: "false",
			}
			updateConfigMap(data, poolReconciliationWaitTime)
		})

		Context("when prefix delegation is disabled and secondary IP mode is active", func() {
			Context("When windows-warm-prefix-target is set to non zero value", Label("windows-warm-prefix-target"), func() {
				BeforeEach(func() {
					data[config.WinWarmPrefixTarget] = "2"
				})
				It("if windows-warm-prefix-target is 2 the value should be ignored and no prefixes should be assigned", func() {
					By(fmt.Sprintf("creating 1 Windows pod and waiting until in ready status with timeout of %d seconds", int(utils.WindowsPodsCreationTimeout.Seconds())))
					createdPod1, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveIPv4Address(createdPod1)

					GinkgoWriter.Printf("Waiting %d seconds for warmpool to reconciliate after creating new pod(s)...\n", int(poolReconciliationWaitTime.Seconds()))
					time.Sleep(poolReconciliationWaitTime)

					ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(ipv4AddressCount)).To(BeNumerically(">=", 1))
					Expect(len(prefixCount)).To(Equal(0))

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When windows-warm-ip-target is set to non zero value", Label("windows-warm-ip-target"), func() {
				BeforeEach(func() {
					data[config.WinWarmIPTarget] = "1"
					data[config.WinMinimumIPTarget] = "0"
				})
				It("if windows-warm-ip-target is 1 should have 1 warm IPs available when 0 pods were running", func() {
					ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())

					Expect(len(ipv4AddressCount)).To(Equal(1))
					Expect(len(prefixCount)).To(Equal(0))
				})
			})

			Context("When windows-warm-ip-target is 5", Label("windows-warm-ip-target"), func() {
				BeforeEach(func() {
					data[config.WinWarmIPTarget] = "5"
				})
				It("should have 7 warm IPs available when 2 pods were running", func() {
					By(fmt.Sprintf("creating 2 Windows pods and waiting until in ready status with timeout of %d seconds", int(utils.WindowsPodsCreationTimeout.Seconds())))
					createdPod1, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					createdPod2, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod2, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())

					verify.WindowsPodHaveIPv4Address(createdPod1)
					verify.WindowsPodHaveIPv4Address(createdPod2)

					GinkgoWriter.Printf("Waiting %d seconds for warmpool to reconciliate after creating new pod(s)...\n", int(poolReconciliationWaitTime.Seconds()))
					time.Sleep(poolReconciliationWaitTime)

					ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(ipv4AddressCount)).To(BeNumerically("==", 7))
					Expect(len(prefixCount)).To(Equal(0))

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
					Expect(err).ToNot(HaveOccurred())
					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod2)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When windows-minimum-ip-target is set to non zero value", Label("windows-minimum-ip-target"), func() {
				BeforeEach(func() {
					data[config.WinMinimumIPTarget] = "6"
				})
				It("if windows-minimum-ip-target is 6 should have 6 warm IPs available when 0 pods were running", func() {
					ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(ipv4AddressCount)).To(Equal(6))
					Expect(len(prefixCount)).To(Equal(0))
				})
			})

			Context("When windows-minimum-ip-target is set to 6 with 3 pods running", Label("windows-minimum-ip-target"), func() {
				BeforeEach(func() {
					data[config.WinMinimumIPTarget] = "6"
				})
				It("if windows-minimum-ip-target is 6 should have 6 IPs assigned to the node when 3 pods were running", func() {
					By(fmt.Sprintf("creating 3 Windows pods and waiting until in ready status with timeout of %d seconds", int(utils.WindowsPodsCreationTimeout.Seconds())))
					createdPod1, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					createdPod2, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod2, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					createdPod3, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod3, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())

					verify.WindowsPodHaveIPv4Address(createdPod1)
					verify.WindowsPodHaveIPv4Address(createdPod2)
					verify.WindowsPodHaveIPv4Address(createdPod3)

					GinkgoWriter.Printf("Waiting %d seconds for warmpool to reconciliate after creating new pod(s)...\n", int(poolReconciliationWaitTime.Seconds()))
					time.Sleep(poolReconciliationWaitTime)

					ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(ipv4AddressCount)).To(Equal(6))
					Expect(len(prefixCount)).To(Equal(0))

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
					Expect(err).ToNot(HaveOccurred())
					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod2)
					Expect(err).ToNot(HaveOccurred())
					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod3)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When windows-warm-ip-target and windows-minimum-ip-target set to non zero values", Label("windows-minimum-ip-target"), func() {
				Context("windows-minimum-ip-target=3 and windows-warm-ip-target=6", func() {
					BeforeEach(func() {
						data[config.WinMinimumIPTarget] = "3"
						data[config.WinWarmIPTarget] = "6"
					})
					It("if  should have 6 IPs assigned and 6 warm IPs available when 0 pods were running", func() {
						ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
						Expect(err).ToNot(HaveOccurred())
						Expect(len(ipv4AddressCount)).To(Equal(6))
						Expect(len(prefixCount)).To(Equal(0))
					})
				})

				Context("windows-minimum-ip-target=8 and windows-warm-ip-target=4", func() {
					BeforeEach(func() {
						data[config.WinMinimumIPTarget] = "8"
						data[config.WinWarmIPTarget] = "4"
					})
					It("should have 8 IPs assigned and 8 warm IPs available when 0 pods were running", func() {
						ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
						Expect(err).ToNot(HaveOccurred())
						Expect(len(ipv4AddressCount)).To(Equal(8))
						Expect(len(prefixCount)).To(Equal(0))
					})
				})
				Context("windows-minimum-ip-target=2 and windows-warm-ip-target=4", func() {
					BeforeEach(func() {
						data[config.WinMinimumIPTarget] = "2"
						data[config.WarmIPTarget] = "4"
					})
					It("should have 6 IPs assigned and 4 warm IPs available when 2 pods were running", func() {
						By(fmt.Sprintf("creating 2 Windows pods and waiting until in ready status with timeout of %d seconds", int(utils.WindowsPodsCreationTimeout.Seconds())))
						createdPod1, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
						Expect(err).ToNot(HaveOccurred())
						createdPod2, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod2, utils.WindowsPodsCreationTimeout)
						Expect(err).ToNot(HaveOccurred())

						verify.WindowsPodHaveIPv4Address(createdPod1)
						verify.WindowsPodHaveIPv4Address(createdPod2)

						GinkgoWriter.Printf("Waiting %d seconds for warmpool to reconciliate after creating new pod(s)...\n", int(poolReconciliationWaitTime.Seconds()))
						time.Sleep(poolReconciliationWaitTime)

						ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
						Expect(err).ToNot(HaveOccurred())
						Expect(len(ipv4AddressCount)).To(Equal(6))
						Expect(len(prefixCount)).To(Equal(0))

						err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
						Expect(err).ToNot(HaveOccurred())
						err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod2)
						Expect(err).ToNot(HaveOccurred())
					})
				})
			})

			Context("When windows-warm-ip-target and windows-min-ip-target set to 0", Label("windows-warm-ip-target"), func() {
				BeforeEach(func() {
					data[config.WinMinimumIPTarget] = "0"
					data[config.WinWarmIPTarget] = "0"
				})

				It("should result in warm-ip-target=1 and min-ip-target=0", func() {
					ipv4AddressCount, prefixCount, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(ipv4AddressCount)).To(Equal(1))
					Expect(len(prefixCount)).To(Equal(0))
				})
			})
		})
	})

	Describe("configMap enable-windows-prefix-delegation tests", Label("windows-prefix-delegation"), func() {
		// Test windows prefix delegation feature enable/disable. When feature enabled, pod must have
		// prefix ips assigned. Otherwise, pod must have secondary ip assigned.
		var testPod, testPod2, testPodLongLiving *v1.Pod
		var createdPod *v1.Pod
		var instanceID string
		var nodeName string
		var bufferForCoolDown time.Duration

		BeforeEach(func() {
			data = map[string]string{
				config.EnableWindowsIPAMKey:             "true",
				config.EnableWindowsPrefixDelegationKey: "true"}

			bufferForCoolDown = time.Second * 30

			windowsNodeList = node.GetNodeAndWaitTillCapacityPresent(frameWork.NodeManager, ctx, "windows",
				config.ResourceNameIPAddress)
			instanceID = manager.GetNodeInstanceID(&windowsNodeList.Items[0])
			nodeName = windowsNodeList.Items[0].Name

			testerContainerCommands = []string{
				GetCommandToTestHostConnectivity("www.amazon.com", 80, 2, false),
			}

			testerContainer = manifest.NewWindowsContainerBuilder().
				Args(testerContainerCommands).
				Build()
			testContainerLongLiving := manifest.NewWindowsContainerBuilder().Args(sleepInfinityContainerCommands).Build()

			testPod, err = manifest.NewWindowsPodBuilder().
				Namespace("windows-test").
				Name("windows-pd-pod").
				Container(testerContainer).
				OS("windows").
				TerminationGracePeriod(0).
				RestartPolicy(v1.RestartPolicyNever).
				NodeName(nodeName).
				Build()
			Expect(err).ToNot(HaveOccurred())

			testPod2, err = manifest.NewWindowsPodBuilder().
				Namespace("windows-test").
				Name("windows-pd-pod2").
				Container(testerContainer).
				OS("windows").
				TerminationGracePeriod(0).
				RestartPolicy(v1.RestartPolicyNever).
				NodeName(nodeName).
				Build()
			Expect(err).ToNot(HaveOccurred())

			testPodLongLiving, err = manifest.NewWindowsPodBuilder().
				Namespace("windows-test").
				Name("windows-pod-long-living").
				Container(testContainerLongLiving).
				OS("windows").
				TerminationGracePeriod(0).
				RestartPolicy(v1.RestartPolicyNever).
				NodeName(nodeName).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when prefix delegation is enabled", func() {
			JustBeforeEach(func() {
				// update configmap for tests
				testConfigMap = *manifest.NewConfigMapBuilder().Data(data).Build()
				configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, &testConfigMap)
			})

			JustAfterEach(func() {
				// restore the configmap after each test
				testConfigMap = *manifest.NewConfigMapBuilder().Data(data).Build()
				configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, &testConfigMap)
			})

			Context("[CANARY] When enable-windows-prefix-delegation is true", func() {
				It("pod should be running and assigned ips are from prefix", func() {
					By("creating pod and waiting for ready")
					numIPsBefore, prefixesBefore, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(numIPsBefore)).To(Equal(0))
					Expect(len(prefixesBefore)).To(Equal(1))

					// verify if ip assigned is coming from a prefix
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveIPv4AddressFromPrefixes(createdPod, prefixesBefore)

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			// TODO: remove this context when VPC CNI also updates the flag name to windows prefixed.
			Context("When warm-prefix-target is set to 2", Label("warm-prefix-target"), func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "true",
						config.WarmPrefixTarget:                 "2"}

				})
				It("two prefixes should be assigned", func() {
					// allow some time for previous test pod to cool down
					time.Sleep(bufferForCoolDown)
					numIPsBefore, prefixesBefore, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(numIPsBefore)).To(Equal(0))
					Expect(len(prefixesBefore)).To(Equal(2))

					By("creating pod and waiting for ready should have 1 new prefix assigned")
					// verify if ip assigned is coming from a prefix
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveIPv4AddressFromPrefixes(createdPod, prefixesBefore)

					// number of prefixes should increase by 1 since need 1 more prefix to fulfill warm-prefix-target of 2
					numIPsAfter, prefixesAfter, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(numIPsAfter)).To(Equal(0))
					Expect(len(prefixesAfter) - len(prefixesBefore)).To(Equal(1))

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			// TODO: remove this context when VPC CNI also updates the flag name to windows prefixed.
			Context("When warm-ip-target is set to 15", Label("warm-ip-target"), func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "true",
						config.WarmIPTarget:                     "15"}
				})
				It("should assign new prefix when 2nd pod is launched", func() {
					// allow some time for previous test pod to cool down
					time.Sleep(bufferForCoolDown)
					// before running any pod, should have 1 prefix assigned
					privateIPsBefore, prefixesBefore, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesBefore)).To(Equal(1))

					By("creating 1 pod and waiting for ready should not create new prefix")
					// verify if ip assigned is coming from a prefix
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPodLongLiving, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())

					_, prefixesAfterPod1, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesAfterPod1)).To(Equal(len(prefixesBefore)))
					verify.WindowsPodHaveIPv4AddressFromPrefixes(createdPod, prefixesAfterPod1)

					// launch 2nd pod to trigger a new prefix to be assigned since warm-ip-target=15
					By("creating 2nd pod and waiting for ready should have 1 more prefix assigned")
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod2, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)

					privateIPsAfter, prefixesAfterPod2, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					// 1 more prefix should be created to fulfill warm-ip-target=15
					Expect(len(prefixesAfterPod2) - len(prefixesAfterPod1)).To(Equal(1))
					// number of secondary ips should not change
					Expect(len(privateIPsBefore)).To(Equal(len(privateIPsAfter)))
					verify.WindowsPodHaveIPv4AddressFromPrefixes(createdPod, prefixesAfterPod2)

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPodLongLiving)
					Expect(err).ToNot(HaveOccurred())
					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod2)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			// TODO: remove this context when VPC CNI also updates the flag name to windows prefixed.
			Context("When minimum-ip-target is set to 20", Label("minimum-ip-target"), func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "true",
						config.MinimumIPTarget:                  "20"}
				})
				It("should have 2 prefixes to satisfy minimum-ip-target when no pods running", func() {
					By("adding labels to selected nodes for testing")
					node := windowsNodeList.Items[0]
					err = frameWork.NodeManager.AddLabels([]v1.Node{node}, map[string]string{podLabelKey: podLabelVal})
					Expect(err).ToNot(HaveOccurred())

					// allow some time for previous test pod to cool down
					time.Sleep(bufferForCoolDown)
					// before running any pod, should have 2 prefixes assigned
					instanceID = manager.GetNodeInstanceID(&node)
					privateIPsBefore, prefixesBefore, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesBefore)).To(Equal(2))

					By("creating 33 pods and waiting for ready should have 3 prefixes attached")
					deployment := manifest.NewWindowsDeploymentBuilder().
						Replicas(33).
						Container(manifest.NewWindowsContainerBuilder().Build()).
						PodLabel(podLabelKey, podLabelVal).
						NodeSelector(map[string]string{"kubernetes.io/os": "windows", podLabelKey: podLabelVal}).
						Build()
					_, err = frameWork.DeploymentManager.CreateAndWaitUntilDeploymentReady(ctx, deployment)
					Expect(err).ToNot(HaveOccurred())

					_, prefixesAfterDeployment, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesAfterDeployment)).To(Equal(3))

					By("deleting 33 pods should still have 2 prefixes attached")
					err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)
					Expect(err).ToNot(HaveOccurred())

					// allow some time for previous test pods to cool down since deletion of deployment doesn't wait for pods to terminate
					time.Sleep(utils.WindowsPodsDeletionTimeout)
					privateIPsAfter, prefixesAfterDelete, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesAfterDelete)).To(Equal(2))
					// number of secondary ips should not change
					Expect(len(privateIPsBefore)).To(Equal(len(privateIPsAfter)))

					By("removing labels on selected nodes for testing")
					err = frameWork.NodeManager.RemoveLabels([]v1.Node{node}, map[string]string{podLabelKey: podLabelVal})
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When windows-warm-prefix-target is set to 2", Label("windows-warm-prefix-target"), func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "true",
						config.WinWarmPrefixTarget:              "2"}

				})

				It("two prefixes should be assigned", func() {
					// allow some time for previous test pod to cool down
					time.Sleep(bufferForCoolDown)
					numIPsBefore, prefixesBefore, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(numIPsBefore)).To(Equal(0))
					Expect(len(prefixesBefore)).To(Equal(2))

					By("creating pod and waiting for ready should have 1 new prefix assigned")
					// verify if ip assigned is coming from a prefix
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveIPv4AddressFromPrefixes(createdPod, prefixesBefore)

					// number of prefixes should increase by 1 since need 1 more prefix to fulfill warm-prefix-target of 2
					numIPsAfter, prefixesAfter, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(numIPsAfter)).To(Equal(0))
					Expect(len(prefixesAfter) - len(prefixesBefore)).To(Equal(1))

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When windows-warm-ip-target is set to 15", Label("windows-warm-ip-target"), func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "true",
						config.WinWarmIPTarget:                  "15"}
				})
				It("should assign new prefix when 2nd pod is launched", func() {
					// allow some time for previous test pod to cool down
					time.Sleep(bufferForCoolDown)
					// before running any pod, should have 1 prefix assigned
					privateIPsBefore, prefixesBefore, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesBefore)).To(Equal(1))

					By("creating 1 pod and waiting for ready should not create new prefix")
					// verify if ip assigned is coming from a prefix
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPodLongLiving, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())

					_, prefixesAfterPod1, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesAfterPod1)).To(Equal(len(prefixesBefore)))
					verify.WindowsPodHaveIPv4AddressFromPrefixes(createdPod, prefixesAfterPod1)

					// launch 2nd pod to trigger a new prefix to be assigned since warm-ip-target=15
					By("creating 2nd pod and waiting for ready should have 1 more prefix assigned")
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod2, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)

					privateIPsAfter, prefixesAfterPod2, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					// 1 more prefix should be created to fulfill warm-ip-target=15
					Expect(len(prefixesAfterPod2) - len(prefixesAfterPod1)).To(Equal(1))
					// number of secondary ips should not change
					Expect(len(privateIPsBefore)).To(Equal(len(privateIPsAfter)))
					verify.WindowsPodHaveIPv4AddressFromPrefixes(createdPod, prefixesAfterPod2)

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPodLongLiving)
					Expect(err).ToNot(HaveOccurred())
					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, testPod2)
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("When windows-minimum-ip-target is set to 20", Label("windows-minimum-ip-target"), func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "true",
						config.WinMinimumIPTarget:               "20"}
				})
				It("should have 2 prefixes to satisfy windows-minimum-ip-target when no pods running", func() {
					By("adding labels to selected nodes for testing")
					node := windowsNodeList.Items[0]
					err = frameWork.NodeManager.AddLabels([]v1.Node{node}, map[string]string{podLabelKey: podLabelVal})
					Expect(err).ToNot(HaveOccurred())

					// allow some time for previous test pod to cool down
					time.Sleep(bufferForCoolDown)
					// before running any pod, should have 2 prefixes assigned
					instanceID = manager.GetNodeInstanceID(&node)
					privateIPsBefore, prefixesBefore, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesBefore)).To(Equal(2))

					By("creating 33 pods and waiting for ready should have 3 prefixes attached")
					deployment := manifest.NewWindowsDeploymentBuilder().
						Replicas(33).
						Container(manifest.NewWindowsContainerBuilder().Build()).
						PodLabel(podLabelKey, podLabelVal).
						NodeSelector(map[string]string{"kubernetes.io/os": "windows", podLabelKey: podLabelVal}).
						Build()
					_, err = frameWork.DeploymentManager.CreateAndWaitUntilDeploymentReady(ctx, deployment)
					Expect(err).ToNot(HaveOccurred())

					_, prefixesAfterDeployment, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesAfterDeployment)).To(Equal(3))

					By("deleting 33 pods should still have 2 prefixes attached")
					err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)
					Expect(err).ToNot(HaveOccurred())

					// allow some time for previous test pods to cool down since deletion of deployment doesn't wait for pods to terminate
					time.Sleep(utils.WindowsPodsDeletionTimeout)
					privateIPsAfter, prefixesAfterDelete, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesAfterDelete)).To(Equal(2))
					// number of secondary ips should not change
					Expect(len(privateIPsBefore)).To(Equal(len(privateIPsAfter)))

					By("removing labels on selected nodes for testing")
					err = frameWork.NodeManager.RemoveLabels([]v1.Node{node}, map[string]string{podLabelKey: podLabelVal})
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("[CANARY] When enable-windows-prefix-delegation is toggled to false", func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "false",
					}
				})
				It("prefixes should be released", func() {
					// allow some time for previous test pod to cool down
					time.Sleep(bufferForCoolDown)

					privateIPsBefore, _, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())

					// verify if ip assigned is a secondary ip
					By("creating pod and waiting for ready should have secondary IP assigned")
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)
					verify.WindowsPodHaveIPv4Address(createdPod)

					// launch another pod to exceed maxDeviation of 1 in secondary ip pool
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod2, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)
					verify.WindowsPodHaveIPv4Address(createdPod)

					// prefixes should be released
					privateIPsAfter, prefixesAfter, err := frameWork.EC2Manager.GetPrivateIPv4AddressAndPrefix(instanceID)
					Expect(err).ToNot(HaveOccurred())
					Expect(len(prefixesAfter)).To(Equal(0))
					// number of secondary ips should increase by 2 since warm pool desired size is 3 and 2 of them are used
					Expect(len(privateIPsAfter) - len(privateIPsBefore)).To(Equal(2))
				})
			})

			Context("When enable-windows-prefix-delegation is incorrect", func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "wrongVal"}
				})
				It("pod should be running with secondary ip assigned and not prefix ip", func() {
					By("creating pod and waiting for ready")
					createdPod, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveIPv4Address(createdPod)
				})
			})

			Context("When PD flag present but IPAM flag is missing", func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsPrefixDelegationKey: "true"}
				})
				It("pod should not be running and should not have resource limits", func() {
					By("creating pod and waiting for timeout")
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).To(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, false)
				})
			})

			Context("when enable-windows-prefix-delegation is set to true but old controller deployment exists", func() {
				BeforeEach(func() {
					data = map[string]string{
						config.EnableWindowsIPAMKey:             "true",
						config.EnableWindowsPrefixDelegationKey: "true"}
				})

				It("pod should fail to create", func() {
					By("creating a dummy deployment for vpc-resource-controller")
					oldControllerDeployment := manifest.NewDefaultDeploymentBuilder().
						Namespace(config.KubeSystemNamespace).
						Name(config.OldVPCControllerDeploymentName).
						PodLabel("app", "vpc-resource-controller").
						Replicas(1).
						Build()
					_, err = frameWork.DeploymentManager.
						CreateAndWaitUntilDeploymentReady(ctx, oldControllerDeployment)
					Expect(err).ToNot(HaveOccurred())

					By("creating windows pod and waiting for it to timeout")
					createdPod, err := frameWork.PodManager.
						CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).To(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, false)

					err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, createdPod)
					Expect(err).ToNot(HaveOccurred())

					By("deleting the old controller dummy deployment")
					err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx,
						oldControllerDeployment)
					Expect(err).ToNot(HaveOccurred())

					By("creating windows pod and waiting for it to run")
					testPod, err = manifest.NewWindowsPodBuilder().Build()
					Expect(err).ToNot(HaveOccurred())

					createdPod, err = frameWork.PodManager.
						CreateAndWaitTillPodIsRunning(ctx, testPod, utils.WindowsPodsCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)
				})
			})

		})
	})

	Describe("[CANARY] windows connectivity tests", func() {
		var service *v1.Service

		BeforeEach(func() {
			service, err = frameWork.SVCManager.
				GetService(ctx, "default", "kubernetes")
			Expect(err).ToNot(HaveOccurred())
		})

		JustBeforeEach(func() {
			// restore the configmap to secondary IP mode
			testConfigMap = *manifest.NewConfigMapBuilder().Build()
			configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, &testConfigMap)
			_, err = frameWork.JobManager.CreateAndWaitForJobToComplete(ctx, job)
		})

		JustAfterEach(func() {
			err = frameWork.JobManager.DeleteAndWaitTillJobIsDeleted(ctx, job)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when multiples jobs are created that try connect to a service", func() {

			BeforeEach(func() {
				jobParallelism = 30
				testerContainerCommands = []string{
					GetCommandToTestHostConnectivity(service.Spec.ClusterIP, service.Spec.Ports[0].Port, 10, false),
				}
			})

			It("all job should complete", func() {
				Expect(err).ToNot(HaveOccurred())

				By("verifying the job has same IPv4 Address as allocated by the controller")
				verify.WindowsPodsHaveIPv4Address(namespace, podLabelKey, podLabelVal)
			})
		})

		// Negative test to reinforce the positive one works
		Context("when creating window job to connect to unreachable port", func() {
			BeforeEach(func() {
				jobParallelism = 1
				testerContainerCommands = []string{
					GetCommandToTestHostConnectivity(service.Spec.ClusterIP, 1, 1, false),
				}
			})

			It("all jobs should fail", func() {
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when connecting to internet", func() {
			BeforeEach(func() {
				testerContainerCommands = []string{
					GetCommandToTestHostConnectivity("www.amazon.com", 80, 2, false),
				}
			})

			It("should connect", func() {
				Expect(err).ToNot(HaveOccurred())
			})
		})

		// Negative test to reinforce the positive one works
		Context("when connecting to invalid url", func() {
			BeforeEach(func() {
				testerContainerCommands = []string{
					GetCommandToTestHostConnectivity("www.amazon.zzz", 80, 1, false),
				}
			})

			It("should fail to connect", func() {
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("[CANARY] windows service tests", func() {
		var service v1.Service
		var deployment *appsV1.Deployment
		var deploymentContainer v1.Container
		var testerContainer v1.Container
		var testerJob *batchV1.Job
		var serviceType v1.ServiceType
		var bufferForSvcToBecomeReady time.Duration

		JustBeforeEach(func() {

			deploymentContainer = manifest.NewWindowsContainerBuilder().
				Args([]string{GetCommandToStartHttpServer()}).
				Build()

			deployment = manifest.NewWindowsDeploymentBuilder().
				Replicas(10).
				PodLabel(podLabelKey, podLabelVal).
				Container(deploymentContainer).
				Build()

			By("creating a deployment running a web server")
			_, err = frameWork.DeploymentManager.CreateAndWaitUntilDeploymentReady(ctx, deployment)
			Expect(err).ToNot(HaveOccurred())

			service = manifest.NewHTTPService().
				ServiceType(serviceType).
				Namespace(namespace).
				Name("windows-service-"+strings.ToLower(string(serviceType))).
				Selector(podLabelKey, podLabelVal).
				Build()

			By("creating a service of type " + string(serviceType))
			_, err := frameWork.SVCManager.CreateService(ctx, &service)
			Expect(err).ToNot(HaveOccurred())

			// Allow some time for service to become ready
			time.Sleep(bufferForSvcToBecomeReady)

			testerContainer = manifest.NewWindowsContainerBuilder().
				Args([]string{
					GetCommandToTestHostConnectivity(service.Spec.ClusterIP, service.Spec.Ports[0].Port, 10, false)}).
				Build()

			testerJob = manifest.NewWindowsJob().
				Parallelism(10).
				Container(testerContainer).
				Build()

			By(fmt.Sprintf("creating testers to connect to service %s on %s on %d",
				service.Name, service.Spec.ClusterIP, service.Spec.Ports[0].Port))
			_, err = frameWork.JobManager.CreateAndWaitForJobToComplete(ctx, testerJob)
			Expect(err).ToNot(HaveOccurred())
		})

		JustAfterEach(func() {
			err = frameWork.JobManager.DeleteAndWaitTillJobIsDeleted(ctx, testerJob)
			Expect(err).ToNot(HaveOccurred())

			err = frameWork.SVCManager.DeleteService(ctx, &service)
			Expect(err).ToNot(HaveOccurred())

			err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)
			Expect(err).ToNot(HaveOccurred())
		})

		Context("when a deployment behind lb service is created", func() {
			BeforeEach(func() {
				serviceType = v1.ServiceTypeLoadBalancer
				// LB takes some extra time
				bufferForSvcToBecomeReady = time.Minute * 2
			})

			It("load balancer service pods should be reachable", func() {})
		})

		Context("when a deployment behind cluster ip is created", func() {
			BeforeEach(func() {
				serviceType = v1.ServiceTypeClusterIP
				bufferForSvcToBecomeReady = time.Second * 30
			})

			It("clusterIP service pods should be reachable", func() {})
		})

		Context("when a deployment behind cluster ip is created", func() {
			BeforeEach(func() {
				serviceType = v1.ServiceTypeNodePort
				bufferForSvcToBecomeReady = time.Second * 30
			})

			It("nodeport service pods should be reachable", func() {})
		})
	})

	Describe("when creating pod with same namespace and name", func() {
		BeforeEach(func() {
			testerContainerCommands = []string{
				GetCommandToTestHostConnectivity("www.amazon.com", 80, 2, false),
			}
		})

		It("should successfully run the pod each time", func() {
			for i := 0; i < 5; i++ {
				By(fmt.Sprintf("run # %d: creating pod with same ns/name", i))
				pod, err := manifest.NewWindowsPodBuilder().Container(testerContainer).Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = frameWork.PodManager.CreateAndWaitTillPodIsCompleted(ctx, pod)
				Expect(err).ToNot(HaveOccurred())

				err = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, pod)
				Expect(err).ToNot(HaveOccurred())
			}
		})
	})

	Describe("windows deployment tests", func() {
		var deployment *appsV1.Deployment

		Context("creating a deployment multiple times", func() {

			It("deployment should be ready each time", func() {

				for i := 0; i < 5; i++ {
					By(fmt.Sprintf("run # %d: creating the deployment", i))

					deployment = manifest.NewWindowsDeploymentBuilder().
						Replicas(30).
						Container(manifest.NewWindowsContainerBuilder().Build()).
						PodLabel(podLabelKey, podLabelVal).
						Build()

					_, err = frameWork.DeploymentManager.CreateAndWaitUntilDeploymentReady(ctx, deployment)
					Expect(err).ToNot(HaveOccurred())

					verify.WindowsPodsHaveIPv4Address(namespace, podLabelKey, podLabelVal)

					err = frameWork.DeploymentManager.DeleteAndWaitUntilDeploymentDeleted(ctx, deployment)
					Expect(err).ToNot(HaveOccurred())
				}
			})
		})
	})
})

func generateTestPodSpec(index int, testerContainer v1.Container, nodeName string) *v1.Pod {
	testPod, err := manifest.NewWindowsPodBuilder().
		Namespace("windows-test").
		Name(fmt.Sprintf("windows-secondary-ip-pod-%d", index)).
		Container(testerContainer).
		OS("windows").
		TerminationGracePeriod(0).
		RestartPolicy(v1.RestartPolicyNever).
		NodeName(nodeName).
		Build()
	Expect(err).ToNot(HaveOccurred())
	return testPod
}

func updateConfigMap(data map[string]string, waitTime time.Duration) {
	By("updating the configmap")
	builtConfigMap := *manifest.NewConfigMapBuilder().Data(data).Build()
	configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, &builtConfigMap)
	GinkgoWriter.Printf("Updated amazon-vpc-cni config map data: %v\n", data)
	GinkgoWriter.Printf("Waiting %d seconds for pool reconciliation...\n", int(waitTime.Seconds()))
	time.Sleep(waitTime)
}

func GetCommandToTestHostConnectivity(host string, port int32, retries int, sleepForever bool) string {
	return fmt.Sprintf(`
     $Server = "%s"
     $Port = %d
     $Retries = %d
     $RetryInterval = 1
     $SleepForever = $%t # If true, sleep forever after the test is complete

     While (-Not (Test-NetConnection -ComputerName $Server -Port $Port).TcpTestSucceeded) {
       if ($Retries -le 0) {
         Write-Warning "maximum number of connection attempts reached, exiting"
         if (!$SleepForever) {
           exit 1
         } else {
           break
         }
       }
       Write-Warning "failed to connect to server $Server, will retry"
       Start-Sleep -s $RetryInterval
       $Retries -= 1
       # Limit RetryInterval to 20 seconds after it exceeds certain value
       $RetryInterval = if ($RetryInterval -lt 20) {$RetryInterval*2} else {20}
     }
     Write-Output "connection from $env:COMPUTERNAME to $Server succeeded"
     if ($SleepForever) {
       while ($true) { 
	      $SleepSeconds = 3600
	      Write-Output "Sleeping forver, will sleep for $SleepSeconds seconds at a time..."
	      Start-Sleep -Seconds $SleepSeconds; 
       }
     }`, host, port, retries, sleepForever)
}

// Install and start the dot net web server, it's light weight so starts pretty quick
func GetCommandToStartHttpServer() string {
	return "Add-WindowsFeature Web-Server; Invoke-WebRequest " +
		"-Uri 'https://dotnetbinaries.blob.core.windows.net/servicemonitor/2.0.1.6/ServiceMonitor.exe'" +
		" -OutFile 'C:\\ServiceMonitor.exe'; " +
		"echo 'ok' > C:\\inetpub\\wwwroot\\default.html; " + "C:\\ServiceMonitor.exe 'w3svc'; "
}

// TODO: Test internet connectivity too along side pod to pod connectivity
func GetCommandToContinuouslyTestHostConnectivity(host string, tries int, interval int) string {
	return fmt.Sprintf(`
    while($val -ne %d) {
      Start-Sleep -s %d # Sleep for specified interval before testing connection
      %s # The test connection command
      $val++
    }`, tries, interval, GetCommandToTestHostConnectivity(host, 80, 10, false))
}
