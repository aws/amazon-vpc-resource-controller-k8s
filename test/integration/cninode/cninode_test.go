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

package cninode_test

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	testUtils "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var cniNodeAutoScalingGroupName = flag.String(
	"cninode-asg-name",
	"",
	"Auto Scaling group used by the CNINode suite node",
)

var _ = Describe("[CANARY]CNINode test", Serial, func() {
	Describe("CNINode checkpoint on the suite node", func() {
		It("matches EC2 for the newly added node", func(ctx SpecContext) {
			node := suiteNode()
			Expect(waitTillPodENICapacityPresent(ctx, node)).To(Succeed())

			Eventually(func() error {
				expectedCheckpoint, err := expectedNetworkCheckpoint(ctx, suiteNodeInstanceID)
				if err != nil {
					return err
				}
				cniNode, err := frameWork.NodeManager.GetCNINodeContext(ctx, node)
				if err != nil {
					return err
				}
				return verifyNetworkCheckpoint(cniNode, expectedCheckpoint)
			}, testUtils.PollTimeout, testUtils.PollIntervalShort).
				WithContext(ctx).
				Should(Succeed())
		})
	})

	Describe("CNINode is re-created when node exists", func() {
		Context("when CNINode is deleted but node exists", func() {
			It("it should re-create CNINode", func() {
				Skip("Skipping this test until we make release with manifest update of cni-node")
				node := suiteNode()
				cniNode, err := frameWork.NodeManager.GetCNINode(node)
				Expect(err).ToNot(HaveOccurred())
				err = frameWork.NodeManager.DeleteCNINode(cniNode)
				Expect(err).ToNot(HaveOccurred())
				time.Sleep(testUtils.PollIntervalShort) // allow time to re-create CNINode
				_, err = frameWork.NodeManager.GetCNINode(node)
				Expect(err).ToNot(HaveOccurred())
				VerifyCNINodeFields(cniNode)
			})
		})

	})

	Describe("CNINode update tests", func() {
		var cniNode *v1alpha1.CNINode
		var node *v1.Node
		BeforeEach(func() {
			node = suiteNode()
			var err error
			cniNode, err = frameWork.NodeManager.GetCNINode(node)
			Expect(err).ToNot(HaveOccurred())
			VerifyCNINodeFields(cniNode)
		})
		AfterEach(func() {
			time.Sleep(testUtils.PollIntervalShort)
			newCNINode, err := frameWork.NodeManager.GetCNINode(node)
			Expect(err).ToNot(HaveOccurred())
			// Verify CNINode after update matches CNINode before update
			Expect(newCNINode).To(BeComparableTo(cniNode, cmp.Options{
				cmpopts.IgnoreTypes(metav1.TypeMeta{}),
				cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion", "Generation", "ManagedFields"),
			}))
		})

		Context("when finalizer is removed", func() {
			It("it should add the finalizer", func() {
				Skip("Skipping this test until we make release with manifest update of cni-node")
				cniNodeCopy := cniNode.DeepCopy()
				controllerutil.RemoveFinalizer(cniNodeCopy, config.NodeTerminationFinalizer)
				err := frameWork.NodeManager.UpdateCNINode(cniNode, cniNodeCopy)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("when Tags is removed", func() {
			It("it should add the Tags", func() {
				Skip("Skipping this test until we make release with manifest update of cni-node")
				cniNodeCopy := cniNode.DeepCopy()
				cniNodeCopy.Spec.Tags = map[string]string{}
				err := frameWork.NodeManager.UpdateCNINode(cniNode, cniNodeCopy)
				Expect(err).ToNot(HaveOccurred())
			})
		})
		Context("when Label is removed", func() {
			It("it should add the label", func() {
				Skip("Skipping this test until we make release with manifest update of cni-node")
				cniNodeCopy := cniNode.DeepCopy()
				cniNodeCopy.ObjectMeta.Labels = map[string]string{}
				err := frameWork.NodeManager.UpdateCNINode(cniNode, cniNodeCopy)
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})

})

type networkCheckpointExpectation struct {
	state       v1alpha1.NodeNetworkState
	trunkENIIDs map[string]struct{}
}

func autoScalingGroupName(ctx context.Context, nodeList *v1.NodeList) string {
	if *cniNodeAutoScalingGroupName != "" {
		By(fmt.Sprintf(
			"using Auto Scaling group %s from -cninode-asg-name",
			*cniNodeAutoScalingGroupName,
		))
		return *cniNodeAutoScalingGroupName
	}

	By("getting instance details")
	instanceID := frameWork.NodeManager.GetInstanceID(&nodeList.Items[0])
	Expect(instanceID).ToNot(BeEmpty())
	instance, err := frameWork.EC2Manager.GetInstanceDetailsContext(ctx, instanceID)
	Expect(err).ToNot(HaveOccurred())
	tags := utils.GetTagKeyValueMap(instance.Tags)
	val, ok := tags["aws:autoscaling:groupName"]
	Expect(ok).To(BeTrue())
	Expect(val).ToNot(BeEmpty())
	By(fmt.Sprintf(
		"using Auto Scaling group %s from the first Linux node %s",
		val,
		nodeList.Items[0].Name,
	))
	return val
}

func waitForNewInstanceInAutoScalingGroup(
	ctx context.Context,
	asgName string,
	excludedInstances map[string]struct{},
) (string, error) {
	var foundInstanceID string
	var lastErr error
	err := wait.PollUntilContextTimeout(
		ctx,
		testUtils.PollIntervalShort,
		testUtils.ResourceCreationTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			asg, err := frameWork.AutoScalingManager.DescribeAutoScalingGroup(ctx, asgName)
			if err != nil {
				lastErr = err
				return false, nil
			}
			for _, instance := range asg[0].Instances {
				instanceID := aws.ToString(instance.InstanceId)
				if _, excluded := excludedInstances[instanceID]; !excluded {
					foundInstanceID = instanceID
					return true, nil
				}
			}
			lastErr = fmt.Errorf("no new instance in Auto Scaling group %s was found", asgName)
			return false, nil
		},
	)
	if err != nil {
		if lastErr != nil {
			return "", fmt.Errorf("waiting for a new instance in Auto Scaling group %s: %w", asgName, lastErr)
		}
		return "", err
	}
	return foundInstanceID, nil
}

func waitForReadyNode(
	ctx context.Context,
	instanceID string,
	excludedNodes map[string]struct{},
) (*v1.Node, error) {
	var foundNode *v1.Node
	var lastErr error
	err := wait.PollUntilContextTimeout(
		ctx,
		testUtils.PollIntervalShort,
		testUtils.ResourceCreationTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			nodes, err := frameWork.NodeManager.GetNodesWithOSContext(ctx, config.OSLinux)
			if err != nil {
				lastErr = err
				return false, nil
			}
			for index := range nodes.Items {
				candidate := &nodes.Items[index]
				if _, excluded := excludedNodes[candidate.Name]; excluded {
					continue
				}
				if frameWork.NodeManager.GetInstanceID(candidate) != instanceID ||
					!nodeIsReady(candidate) {
					continue
				}
				foundNode = candidate.DeepCopy()
				return true, nil
			}
			lastErr = fmt.Errorf("no Ready Linux node for instance %s was found", instanceID)
			return false, nil
		},
	)
	if err != nil {
		if lastErr != nil {
			return nil, fmt.Errorf("waiting for a Ready node for instance %s: %w", instanceID, lastErr)
		}
		return nil, err
	}
	return foundNode, nil
}

func waitTillCNINodePresent(ctx context.Context, node *v1.Node) error {
	By(fmt.Sprintf("waiting for CNINode %s to be created", node.Name))
	return wait.PollUntilContextTimeout(
		ctx,
		testUtils.PollIntervalShort,
		testUtils.ResourceCreationTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			_, err := frameWork.NodeManager.GetCNINodeContext(ctx, node)
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return err == nil, err
		},
	)
}

func waitTillPodENICapacityPresent(ctx context.Context, node *v1.Node) error {
	By(fmt.Sprintf("waiting for pod ENI capacity on node %s", node.Name))
	return wait.PollUntilContextTimeout(
		ctx,
		testUtils.PollIntervalShort,
		testUtils.ResourceCreationTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			observedNode, err := frameWork.NodeManager.GetNodeContext(ctx, node)
			if err != nil {
				return false, nil
			}
			_, found := observedNode.Status.Allocatable[config.ResourceNamePodENI]
			return found, nil
		},
	)
}

func waitTillNodeDeleted(ctx context.Context, node *v1.Node) error {
	By(fmt.Sprintf("waiting for node %s to be deleted", node.Name))
	return wait.PollUntilContextTimeout(
		ctx,
		testUtils.PollIntervalShort,
		testUtils.ResourceCreationTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			_, err := frameWork.NodeManager.GetNodeContext(ctx, node)
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		},
	)
}

func waitTillCNINodeDeleted(ctx context.Context, node *v1.Node) error {
	By(fmt.Sprintf("waiting for CNINode %s to return NotFound", node.Name))
	return wait.PollUntilContextTimeout(
		ctx,
		testUtils.PollIntervalShort,
		testUtils.ResourceCreationTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			_, err := frameWork.NodeManager.GetCNINodeContext(ctx, node)
			if apierrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		},
	)
}

func expectedNetworkCheckpoint(
	ctx context.Context,
	instanceID string,
) (networkCheckpointExpectation, error) {
	instance, err := frameWork.EC2Manager.GetInstanceDetailsContext(ctx, instanceID)
	if err != nil {
		return networkCheckpointExpectation{}, err
	}
	if aws.ToString(instance.InstanceId) != instanceID {
		return networkCheckpointExpectation{}, fmt.Errorf(
			"EC2 returned instance %s while looking up %s",
			aws.ToString(instance.InstanceId),
			instanceID,
		)
	}
	if instance.SubnetId == nil {
		return networkCheckpointExpectation{}, fmt.Errorf("instance %s has no subnet ID", instanceID)
	}

	subnet, err := frameWork.EC2Manager.GetSubnetDetailsContext(ctx, aws.ToString(instance.SubnetId))
	if err != nil {
		return networkCheckpointExpectation{}, err
	}
	if subnet.CidrBlock == nil {
		return networkCheckpointExpectation{}, fmt.Errorf(
			"subnet %s has no IPv4 CIDR block",
			aws.ToString(instance.SubnetId),
		)
	}

	primaryENIID := ""
	trunkENIIDs := make(map[string]struct{})
	for _, networkInterface := range instance.NetworkInterfaces {
		if networkInterface.Attachment == nil || networkInterface.NetworkInterfaceId == nil {
			continue
		}
		if aws.ToInt32(networkInterface.Attachment.DeviceIndex) == 0 {
			primaryENIID = aws.ToString(networkInterface.NetworkInterfaceId)
		}
		if aws.ToString(networkInterface.InterfaceType) == string(ec2types.NetworkInterfaceTypeTrunk) &&
			networkInterface.Attachment.Status == ec2types.AttachmentStatusAttached {
			trunkENIIDs[aws.ToString(networkInterface.NetworkInterfaceId)] = struct{}{}
		}
	}
	if primaryENIID == "" {
		return networkCheckpointExpectation{}, fmt.Errorf(
			"instance %s has no network interface at device index 0",
			instanceID,
		)
	}
	if len(trunkENIIDs) == 0 {
		return networkCheckpointExpectation{}, fmt.Errorf(
			"instance %s has no attached trunk network interface",
			instanceID,
		)
	}

	subnetV6CIDRBlock := ""
	// Production checkpoint construction also uses the first IPv6 CIDR association.
	for _, association := range subnet.Ipv6CidrBlockAssociationSet {
		if association.Ipv6CidrBlock != nil {
			subnetV6CIDRBlock = aws.ToString(association.Ipv6CidrBlock)
			break
		}
	}

	return networkCheckpointExpectation{
		state: v1alpha1.NodeNetworkState{
			InstanceID:                instanceID,
			InstanceType:              string(instance.InstanceType),
			SubnetID:                  aws.ToString(instance.SubnetId),
			SubnetCIDRBlock:           aws.ToString(subnet.CidrBlock),
			SubnetV6CIDRBlock:         subnetV6CIDRBlock,
			PrimaryNetworkInterfaceID: primaryENIID,
		},
		trunkENIIDs: trunkENIIDs,
	}, nil
}

func verifyNetworkCheckpoint(
	cniNode *v1alpha1.CNINode,
	expected networkCheckpointExpectation,
) error {
	if cniNode.Status.NodeNetworkState == nil {
		return fmt.Errorf("CNINode %s has no node network state checkpoint", cniNode.Name)
	}
	// Compare the complete stable checkpoint state.
	if diff := cmp.Diff(expected.state, *cniNode.Status.NodeNetworkState); diff != "" {
		return fmt.Errorf("CNINode %s network checkpoint differs from EC2 (-want +got):\n%s", cniNode.Name, diff)
	}
	if cniNode.Status.TrunkInterface == nil {
		return fmt.Errorf("CNINode %s has no trunk interface", cniNode.Name)
	}
	if _, found := expected.trunkENIIDs[cniNode.Status.TrunkInterface.ID]; !found {
		return fmt.Errorf(
			"CNINode %s trunk interface %s is not an attached EC2 trunk interface",
			cniNode.Name,
			cniNode.Status.TrunkInterface.ID,
		)
	}
	return nil
}

func nodeIsReady(node *v1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == v1.NodeReady {
			return condition.Status == v1.ConditionTrue
		}
	}
	return false
}

// Verify finalizer, tag, and label is set on new CNINode
func VerifyCNINodeFields(cniNode *v1alpha1.CNINode) {
	By("verifying finalizer is set")
	Expect(cniNode.ObjectMeta.Finalizers).To(ContainElement(config.NodeTerminationFinalizer))
	// For maps, ContainElement searches through the map's values.
	By("verifying cluster name tag is set")
	Expect(cniNode.Spec.Tags).To(ContainElement(frameWork.Options.ClusterName))
	Expect(config.VPCCNIClusterNameKey).To(BeKeyOf(cniNode.Spec.Tags))

	By("verifying node OS label is set")
	Expect(cniNode.ObjectMeta.Labels).To(ContainElement(config.OSLinux))
	Expect(config.NodeLabelOS).To(BeKeyOf(cniNode.ObjectMeta.Labels))
}
