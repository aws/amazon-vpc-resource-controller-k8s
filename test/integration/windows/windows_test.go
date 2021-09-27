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
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	configMapWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/configmap"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo"
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

			Context("when enable-windows-ipam is True", func() {
				It("pod should be running and have resourceLimit injected", func() {
					By("creating pod and waiting for ready")
					createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.ResourceCreationTimeout)
					Expect(err).ToNot(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, true)
				})
			})

			Context("when enable-windows-ipam is set to true but old controller deployment exists", func() {
				FIt("pod should fail to create", func() {
					By("creating a dummy deployment for vpc-resource-controller")
					oldControllerDeployment := manifest.NewDefaultDeploymentBuilder().
						Namespace(config.OldVPCControllerDeploymentNS).
						Name(config.OldVPCControllerDeploymentName).
						Build()
					_, err = frameWork.DeploymentManager.
						CreateAndWaitUntilDeploymentReady(ctx, oldControllerDeployment)
					Expect(err).ToNot(HaveOccurred())

					By("creating windows pod and waiting for it to timout")
					createdPod, err := frameWork.PodManager.
						CreateAndWaitTillPodIsRunning(ctx, testPod, utils.ResourceCreationTimeout)
					Expect(err).To(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, false)
				})
			})

			Context("when enable-windows-ipam is incorrect", func() {
				BeforeEach(func() {
					data = map[string]string{config.EnableWindowsIPAMKey: "wrongVal"}
				})
				It("pod should not be running and should not have resource limits", func() {
					By("creating pod and waiting for timeout")
					createdPod, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.ResourceCreationTimeout)
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
					createdPod, err := frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.ResourceCreationTimeout)
					Expect(err).To(HaveOccurred())
					verify.WindowsPodHaveResourceLimits(createdPod, false)
				})
			})
		})

		Context("when configmap not created", func() {
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
				createdPod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, testPod, utils.ResourceCreationTimeout)
				Expect(err).To(HaveOccurred())
				verify.WindowsPodHaveResourceLimits(createdPod, false)
			})
		})
	})

	Describe("windows connectivity tests", func() {
		var service *v1.Service

		BeforeEach(func() {
			service, err = frameWork.SVCManager.
				GetService(ctx, "default", "kubernetes")
			Expect(err).ToNot(HaveOccurred())
		})

		JustBeforeEach(func() {
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
					GetCommandToTestTCPConnection(service.Spec.ClusterIP, service.Spec.Ports[0].Port),
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
					GetCommandToTestTCPConnection(service.Spec.ClusterIP, 1),
				}
			})

			It("all jobs should fail", func() {
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when connecting to internet", func() {
			BeforeEach(func() {
				testerContainerCommands = []string{
					GetCommandToTestHostConnectivity("www.amazon.com", 2),
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
					GetCommandToTestHostConnectivity("www.amazon.zzz", 1),
				}
			})

			It("should fail to connect", func() {
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("windows service tests", func() {
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
					GetCommandToTestTCPConnection(service.Spec.ClusterIP, service.Spec.Ports[0].Port)}).
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
				GetCommandToTestHostConnectivity("www.amazon.com", 2),
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

// GetCommandToTestTCPConnection checks TCP connection with the given host and port, if the
// connection fails then the container will exit with non zero exit code which should be used
// by the test case to fail the test case
func GetCommandToTestTCPConnection(host string, port int32) string {
	return fmt.Sprintf("if (-Not (Test-NetConnection %s -Port %d).TcpTestSucceeded)"+
		" {Write-Output 'connection failed:'; exit 10}", host, port)
}

// GetCommandToTestHostConnectivity tests the DNS Resolution and the tcp connection to the
// host
func GetCommandToTestHostConnectivity(host string, retries int) string {
	return fmt.Sprintf(`
     $Server = "%s"
     $Retries = %d

     While (-Not (Test-NetConnection -ComputerName $Server -CommonTCPPort HTTP).TcpTestSucceeded) {
       if ($Retries -le 0) {
         Write-Warning "maximum number of connection attempts reached, exiting"
         exit 1
       }
       Write-Warning "failed to connect to server $Server, will retry"
       $Retries -= 1
     }
     Write-Output "connection from $env:COMPUTERNAME to $Server succeeded"`, host, retries)
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
    }`, tries, interval, GetCommandToTestHostConnectivity(host, 5))
}
