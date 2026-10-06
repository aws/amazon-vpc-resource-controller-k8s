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

package perpodsg_test

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	cniv1alpha1 "github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	sgpWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

const (
	connectivityPort = 80
	sgpRoleLabelKey  = "sgp-connectivity-role"
)

var _ = Describe("Security group connectivity", func() {
	const namespace = "sgp-connectivity"

	BeforeEach(func() {
		Expect(frameWork.NSManager.CreateNamespace(ctx, namespace)).To(Succeed())
	})

	AfterEach(func() {
		Expect(frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)).To(Succeed())
	})

	It("allows a permitted security group and denies another through a Service", func() {
		const (
			serverRole = "server"
			allowRole  = "allow"
			denyRole   = "deny"
		)

		createConnectivitySGP(namespace, "server-sgp", serverRole, securityGroupID1)
		createConnectivitySGP(namespace, "allow-sgp", allowRole, securityGroupID2)
		createConnectivitySGP(namespace, "deny-sgp", denyRole, securityGroupID1)

		Expect(frameWork.EC2Manager.AuthorizeSecurityGroupIngressFromSecurityGroup(
			securityGroupID1,
			securityGroupID2,
			connectivityPort,
			"TCP",
		)).To(Succeed())
		DeferCleanup(func() {
			Expect(frameWork.EC2Manager.RevokeSecurityGroupIngressFromSecurityGroup(
				securityGroupID1,
				securityGroupID2,
				connectivityPort,
				"TCP",
			)).To(Succeed())
		})

		server := createConnectivityPod(namespace, "sgp-server", serverRole, true, nil, "")
		verify.VerifyNetworkingOfPodUsingENI(*server, []string{securityGroupID1})

		service := manifest.NewHTTPService().
			Name("sgp-server").
			Namespace(namespace).
			Selector("app", "sgp-server").
			Build()
		createdService, err := frameWork.SVCManager.CreateService(ctx, &service)
		Expect(err).NotTo(HaveOccurred())

		allowPod := createConnectivityPod(namespace, "allow-client", allowRole, false, nil, "")
		denyPod := createConnectivityPod(namespace, "deny-client", denyRole, false, nil, "")
		verify.VerifyNetworkingOfPodUsingENI(*allowPod, []string{securityGroupID2})
		verify.VerifyNetworkingOfPodUsingENI(*denyPod, []string{securityGroupID1})

		Eventually(func() error {
			stdout, stderr, err := frameWork.PodManager.PodExec(
				namespace,
				allowPod.Name,
				connectivityProbeCommand(createdService),
			)
			if err != nil {
				return fmt.Errorf("allow client probe transport failed: %w: %s", err, stderr)
			}
			if !strings.Contains(stdout, "ALLOWED") {
				return fmt.Errorf("allow client was denied: %s", stdout)
			}
			return nil
		}, utils.ResourceCreationTimeout, utils.PollIntervalShort).Should(Succeed())

		for range 3 {
			stdout, stderr, err := frameWork.PodManager.PodExec(
				namespace,
				denyPod.Name,
				connectivityProbeCommand(createdService),
			)
			Expect(err).NotTo(HaveOccurred(), "deny client probe transport failed: %s", stderr)
			Expect(stdout).To(ContainSubstring("DENIED"))
		}
	})
})

var _ = Describe("Custom networking connectivity", func() {
	const namespace = "sgp-custom-networking"

	BeforeEach(func() {
		Expect(frameWork.NSManager.CreateNamespace(ctx, namespace)).To(Succeed())
	})

	AfterEach(func() {
		Expect(frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)).To(Succeed())
	})

	It("uses the selected node's ENIConfig subnet and reaches a Service", func() {
		node, eniConfig, err := customNetworkingTarget()
		Expect(err).NotTo(HaveOccurred())
		if node == nil {
			Skip("cluster has no custom-networking node with a readable ENIConfig and pod-eni capacity")
		}

		const role = "custom-networking"
		createConnectivitySGP(namespace, "custom-network-sgp", role, securityGroupID1)
		Expect(frameWork.EC2Manager.AuthorizeSecurityGroupIngressFromSecurityGroup(
			securityGroupID1,
			securityGroupID1,
			connectivityPort,
			"TCP",
		)).To(Succeed())
		DeferCleanup(func() {
			Expect(frameWork.EC2Manager.RevokeSecurityGroupIngressFromSecurityGroup(
				securityGroupID1,
				securityGroupID1,
				connectivityPort,
				"TCP",
			)).To(Succeed())
		})

		tolerations := tolerationsForNode(node)
		server := createConnectivityPod(
			namespace,
			"custom-network-server",
			role,
			true,
			tolerations,
			node.Name,
		)
		verify.VerifyNetworkingOfPodUsingENI(*server, []string{securityGroupID1})

		service := manifest.NewHTTPService().
			Name("custom-network-server").
			Namespace(namespace).
			Selector("app", "custom-network-server").
			Build()
		createdService, err := frameWork.SVCManager.CreateService(ctx, &service)
		Expect(err).NotTo(HaveOccurred())

		client := createConnectivityPod(namespace, "custom-network-client", role, false, tolerations, node.Name)
		eniDetails := verify.VerifyNetworkingOfPodUsingENI(*client, []string{securityGroupID1})
		subnetID, err := frameWork.EC2Manager.GetENISubnetID(eniDetails[0].ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(subnetID).To(Equal(eniConfig.Spec.Subnet))

		Eventually(func() error {
			stdout, stderr, err := frameWork.PodManager.PodExec(
				namespace,
				client.Name,
				connectivityProbeCommand(createdService),
			)
			if err != nil {
				return fmt.Errorf("custom-network client probe transport failed: %w: %s", err, stderr)
			}
			if !strings.Contains(stdout, "ALLOWED") {
				return fmt.Errorf("custom-network client was denied: %s", stdout)
			}
			return nil
		}, utils.ResourceCreationTimeout, utils.PollIntervalShort).Should(Succeed())

	})
})

func createConnectivitySGP(namespace, name, role, securityGroup string) {
	sgp, err := manifest.NewSGPBuilder().
		Name(name).
		Namespace(namespace).
		PodMatchLabel(sgpRoleLabelKey, role).
		SecurityGroup([]string{securityGroup}).
		Build()
	Expect(err).NotTo(HaveOccurred())
	sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, sgp)
}

func createConnectivityPod(
	namespace string,
	name string,
	role string,
	server bool,
	tolerations []v1.Toleration,
	nodeName string,
) *v1.Pod {
	container := manifest.NewBusyBoxContainerBuilder().
		Name(name).
		Image("curlimages/curl").
		Build()
	labels := map[string]string{sgpRoleLabelKey: role}
	if server {
		container = manifest.NewBusyBoxContainerBuilder().
			Name(name).
			Image("nginx").
			Command(nil).
			AddContainerPort(v1.ContainerPort{ContainerPort: connectivityPort}).
			Build()
		labels["app"] = name
	}

	pod, err := manifest.NewDefaultPodBuilder().
		Name(name).
		Namespace(namespace).
		Labels(labels).
		Container(container).
		NodeName(nodeName).
		Build()
	Expect(err).NotTo(HaveOccurred())
	pod.Spec.Tolerations = tolerations

	pod, err = frameWork.PodManager.CreateAndWaitTillPodIsRunning(ctx, pod, utils.ResourceCreationTimeout)
	Expect(err).NotTo(HaveOccurred())
	return pod
}

func connectivityProbeCommand(service *v1.Service) []string {
	url := "http://" + net.JoinHostPort(
		service.Spec.ClusterIP,
		strconv.Itoa(int(service.Spec.Ports[0].Port)),
	)
	return []string{
		"sh",
		"-c",
		fmt.Sprintf(
			"if curl --fail --connect-timeout 3 --max-time 7 %q >/dev/null 2>&1; then echo ALLOWED; else echo DENIED; fi",
			url,
		),
	}
}

func customNetworkingTarget() (*v1.Node, *cniv1alpha1.ENIConfig, error) {
	// Keep room for the server and client, which are both pinned to the selected
	// custom-networking node so the test works when that node pool is tainted.
	const requiredAvailablePodENISlots = 2

	nodes := &v1.NodeList{}
	if err := frameWork.K8sClient.List(ctx, nodes); err != nil {
		return nil, nil, fmt.Errorf("listing nodes for custom networking: %w", err)
	}
	pods := &v1.PodList{}
	if err := frameWork.K8sClient.List(ctx, pods); err != nil {
		return nil, nil, fmt.Errorf("listing pods for custom-networking capacity: %w", err)
	}

	for index := range nodes.Items {
		node := &nodes.Items[index]
		if node.Labels["kubernetes.io/os"] != config.OSLinux || !nodeIsReady(node) {
			continue
		}
		eniConfigName := node.Labels[config.CustomNetworkingLabel]
		if eniConfigName == "" {
			cniNode := &rcv1alpha1.CNINode{}
			if err := frameWork.K8sClient.Get(
				ctx,
				types.NamespacedName{Name: node.Name},
				cniNode,
			); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return nil, nil, fmt.Errorf("reading CNINode %s for custom networking: %w", node.Name, err)
			}
			for _, feature := range cniNode.Spec.Features {
				if feature.Name == rcv1alpha1.CustomNetworking && feature.Value != "" {
					eniConfigName = feature.Value
					break
				}
			}
			if eniConfigName == "" {
				continue
			}
		}
		capacity, hasCapacity := node.Status.Allocatable[config.ResourceNamePodENI]
		if !hasCapacity ||
			capacity.Value()-activePodENIRequests(node.Name, pods.Items) < requiredAvailablePodENISlots {
			continue
		}

		eniConfig := &cniv1alpha1.ENIConfig{}
		if err := frameWork.K8sClient.Get(
			ctx,
			types.NamespacedName{Name: eniConfigName},
			eniConfig,
		); err != nil {
			return nil, nil, fmt.Errorf("reading ENIConfig %s for node %s: %w", eniConfigName, node.Name, err)
		}
		if eniConfig.Spec.Subnet == "" {
			return nil, nil, fmt.Errorf("ENIConfig %s for node %s has no subnet", eniConfigName, node.Name)
		}
		return node, eniConfig, nil
	}
	return nil, nil, nil
}

func nodeIsReady(node *v1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == v1.NodeReady {
			return condition.Status == v1.ConditionTrue
		}
	}
	return false
}

func activePodENIRequests(nodeName string, pods []v1.Pod) int64 {
	var requests int64
	for index := range pods {
		pod := &pods[index]
		if pod.Spec.NodeName != nodeName ||
			pod.Status.Phase == v1.PodSucceeded ||
			pod.Status.Phase == v1.PodFailed {
			continue
		}
		for _, container := range pod.Spec.Containers {
			request := container.Resources.Requests[config.ResourceNamePodENI]
			requests += request.Value()
		}
	}
	return requests
}

func tolerationsForNode(node *v1.Node) []v1.Toleration {
	tolerations := make([]v1.Toleration, 0, len(node.Spec.Taints))
	for _, taint := range node.Spec.Taints {
		tolerations = append(tolerations, v1.Toleration{
			Key:      taint.Key,
			Operator: v1.TolerationOpEqual,
			Value:    taint.Value,
			Effect:   taint.Effect,
		})
	}
	return tolerations
}
