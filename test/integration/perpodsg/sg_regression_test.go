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

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	podWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/pod"
	sgpWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/sgp"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

// Scale-regression layer A (doc/reports/scale-acceptance/redesign-regression-soak-vlan-test-plan.md,
// 测试 1/测试 3 的 e2e 子集): every assertion here needs only the CLUSTER account - the pod-eni
// annotation (the controller's committed ledger view) and EC2 describe (the truth). Anything that
// needs control-plane host access (leader restart for R-E1, metric families on :8443, preflight)
// lives in the plan's driver scripts, not in this suite.
//
// The suite answers the top-risk question for the redesign's ledger changes: after pods travel the
// M1 release/cooldown path, the delete/recreate churn path, and the at-capacity contention path
// (which organically exercises M3 reclaim, as observed in the S3/S4 acceptance runs), does every
// pod still hold a branch ENI carrying EXACTLY its own SGP's security groups - no
// cross-contamination, no stale ENI reuse, no VLAN held by two live pods, and no leaked ENIs.
var _ = Describe("SG correctness regression across redesign lifecycle paths", Label("scale-regression"), func() {
	var (
		namespace string

		sgpAlpha *v1beta1.SecurityGroupPolicy
		sgpBeta  *v1beta1.SecurityGroupPolicy

		labelKey   string
		alphaValue string
		betaValue  string

		sgAlpha []string
		sgBeta  []string

		targetNode  string
		branchLimit int

		// every pod this spec created and has not yet deleted; best-effort cleanup
		livePods []*v1.Pod
	)

	BeforeEach(func() {
		namespace = "sg-regression"
		labelKey = "sgp"
		alphaValue = "alpha"
		betaValue = "beta"
		sgAlpha = []string{securityGroupID1}
		sgBeta = []string{securityGroupID2}
		livePods = nil

		Expect(nodeList.Items).NotTo(BeEmpty())
		targetNode = nodeList.Items[0].Name
		instanceID := frameWork.NodeManager.GetInstanceID(&nodeList.Items[0])
		instanceDetails, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
		Expect(err).NotTo(HaveOccurred())
		branchLimit = vpc.Limits[string(instanceDetails.InstanceType)].BranchInterface
		Expect(branchLimit).To(BeNumerically(">", 1))
	})

	JustBeforeEach(func() {
		By("creating the namespace and the two SGPs with DISTINCT security groups")
		Expect(frameWork.NSManager.CreateNamespace(ctx, namespace)).To(Succeed())

		var err error
		sgpAlpha, err = manifest.NewSGPBuilder().
			Name("sgp-alpha").Namespace(namespace).
			PodMatchLabel(labelKey, alphaValue).
			SecurityGroup(sgAlpha).Build()
		Expect(err).NotTo(HaveOccurred())
		sgpBeta, err = manifest.NewSGPBuilder().
			Name("sgp-beta").Namespace(namespace).
			PodMatchLabel(labelKey, betaValue).
			SecurityGroup(sgBeta).Build()
		Expect(err).NotTo(HaveOccurred())
		sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, sgpAlpha)
		sgpWrapper.CreateSecurityGroupPolicy(frameWork.K8sClient, ctx, sgpBeta)
	})

	JustAfterEach(func() {
		By("cleaning up pods, SGPs, and the namespace")
		for _, pod := range livePods {
			_ = frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, pod)
		}
		_ = frameWork.SGPManager.DeleteAndWaitTillSecurityGroupIsDeleted(ctx, sgpAlpha)
		_ = frameWork.SGPManager.DeleteAndWaitTillSecurityGroupIsDeleted(ctx, sgpBeta)
		Expect(frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)).To(Succeed())
	})

	// createPods creates count pods pinned to targetNode with the given label value and name
	// prefix, waits for each to run, verifies its branch ENI carries exactly wantSG, and returns
	// the pods with their ENI details.
	createPods := func(count int, labelValue, namePrefix string, wantSG []string) ([]*v1.Pod, [][]*trunk.ENIDetails) {
		var pods []*v1.Pod
		var enis [][]*trunk.ENIDetails
		for i := 0; i < count; i++ {
			pod, err := manifest.NewDefaultPodBuilder().
				Namespace(namespace).
				Name(fmt.Sprintf("%s%s-%d", utils.ResourceNamePrefix, namePrefix, i)).
				Labels(map[string]string{labelKey: labelValue}).
				NodeName(targetNode).Build()
			Expect(err).NotTo(HaveOccurred())
			pod = podWrapper.CreateAndWaitForPodToStart(frameWork.PodManager, ctx, pod)
			livePods = append(livePods, pod)
			enis = append(enis, verify.VerifyNetworkingOfPodUsingENI(*pod, wantSG))
			pods = append(pods, pod)
		}
		return pods, enis
	}

	// assertNoVlanDoubleHold asserts the ledger invariant a customer would experience as a VLAN
	// conflict: no two LIVE pods on the shared trunk hold the same vlan id (per their pod-eni
	// annotations, the controller's committed allocation record).
	assertNoVlanDoubleHold := func(labelValues ...string) {
		vlanHolder := map[int]string{}
		for _, labelValue := range labelValues {
			pods, err := frameWork.PodManager.GetPodsWithLabel(ctx, namespace, labelKey, labelValue)
			Expect(err).NotTo(HaveOccurred())
			for _, pod := range pods {
				if pod.DeletionTimestamp != nil {
					continue
				}
				eniDetails, err := frameWork.PodManager.GetENIDetailsFromPodAnnotation(pod.Annotations)
				Expect(err).NotTo(HaveOccurred())
				for _, eni := range eniDetails {
					holder, taken := vlanHolder[eni.VlanID]
					Expect(taken).To(BeFalse(), fmt.Sprintf(
						"vlan %d held by two live pods at once: %s and %s", eni.VlanID, holder, pod.Name))
					vlanHolder[eni.VlanID] = pod.Name
				}
			}
		}
	}

	Context("when one SGP's pods churn next to another SGP's steady pods", func() {
		It("keeps every pod on exactly its own SGP's security groups and never disturbs the neighbor", func() {
			const podsPerSGP = 3

			By("baseline: pods of both SGPs on one shared trunk, each on its own SG")
			_, _ = createPods(podsPerSGP, alphaValue, "alpha", sgAlpha)
			betaPods, betaENIs := createPods(podsPerSGP, betaValue, "beta", sgBeta)
			assertNoVlanDoubleHold(alphaValue, betaValue)

			By("churning ONLY the alpha pods: delete all, recreate immediately (inside the vlan reuse cooldown)")
			var alphaOld []*v1.Pod
			for _, pod := range livePods {
				if pod.Labels[labelKey] == alphaValue {
					alphaOld = append(alphaOld, pod)
				}
			}
			for _, pod := range alphaOld {
				Expect(frameWork.PodManager.DeleteAndWaitTillPodIsDeleted(ctx, pod)).To(Succeed())
			}
			livePods = betaPods

			_, alphaNewENIs := createPods(podsPerSGP, alphaValue, "alpha-replacement", sgAlpha)

			By("asserting the replacements carry ONLY sg-alpha (createPods already verified) and hold no duplicate vlans")
			Expect(alphaNewENIs).To(HaveLen(podsPerSGP))
			assertNoVlanDoubleHold(alphaValue, betaValue)

			By("asserting the beta pods were completely undisturbed by the neighbor churn")
			for i, pod := range betaPods {
				current, err := frameWork.PodManager.GetPodsWithLabel(ctx, namespace, labelKey, betaValue)
				Expect(err).NotTo(HaveOccurred())
				for _, cur := range current {
					if cur.Name != pod.Name {
						continue
					}
					curENIs, err := frameWork.PodManager.GetENIDetailsFromPodAnnotation(cur.Annotations)
					Expect(err).NotTo(HaveOccurred())
					Expect(curENIs[0].ID).To(Equal(betaENIs[i][0].ID),
						"a steady pod's branch ENI must not be swapped by a neighbor SGP's churn")
				}
				verify.VerifyNetworkingOfPodUsingENI(*pod, sgBeta)
			}
		})
	})

	// The strongest cluster-side proxy for "SG stays correct THROUGH the reclaim machinery": the
	// delete-all/recreate-all swap at exactly full trunk capacity is the same workload that drove
	// M1 immediate release plus organic M3 error-driven reclaim in the S3/S4 acceptance runs. If
	// any freed/reclaimed state leaked across the swap, a beta pod would surface it here as a
	// wrong SG, a duplicated vlan, or a stuck Pending.
	Context("when a full-capacity trunk swaps every pod from one SGP to another", func() {
		It("brings all replacement pods up on the new SGP's security groups with no leak from the old set", func() {
			By(fmt.Sprintf("filling the target node to full branch-ENI capacity (%d) with alpha pods", branchLimit))
			_, alphaENIs := createPods(branchLimit, alphaValue, "swap-alpha", sgAlpha)

			var oldENIIDs []string
			for _, enis := range alphaENIs {
				for _, eni := range enis {
					oldENIIDs = append(oldENIIDs, eni.ID)
				}
			}

			By("deleting ALL alpha pods at once (grace 0) and immediately demanding full capacity for beta pods")
			Expect(frameWork.PodManager.DeleteAllPodsForcefully(ctx, labelKey, alphaValue)).To(Succeed())
			livePods = nil

			_, _ = createPods(branchLimit, betaValue, "swap-beta", sgBeta)

			By("asserting no vlan is double-held after the swap")
			assertNoVlanDoubleHold(betaValue)

			By("asserting every alpha branch ENI is eventually deleted from EC2 (no orphan leak)")
			for _, eniID := range oldENIIDs {
				Expect(frameWork.EC2Manager.WaitTillTheENIIsDeleted(ctx, eniID)).To(Succeed(),
					fmt.Sprintf("old branch ENI %s must not leak after the at-capacity swap", eniID))
			}
		})
	})

	// VLAN double-hold invariant under repeated fast churn (plan 测试 3 的 lite 版). Full
	// 120-vlan-pool saturation needs a sustained ~3.4 deletes/second on one trunk and stays in
	// the real-env plan (INCONCLUSIVE there if unreached); the deterministic reuse-cooldown
	// assertion is the dedicated full-capacity spec in perpodsg_test.go. What this spec pins is
	// the invariant a customer would feel: across repeated delete-all/recreate-all cycles, no
	// vlan id is ever recorded against two live pods, and every survivor stays on its own SG.
	Context("when a pod batch churns repeatedly on one trunk", func() {
		It("never records the same vlan id against two live pods across churn cycles", func() {
			const batchSize = 4
			const cycles = 3

			for cycle := 0; cycle < cycles; cycle++ {
				By(fmt.Sprintf("churn cycle %d: creating %d pods", cycle, batchSize))
				_, _ = createPods(batchSize, alphaValue, fmt.Sprintf("churn-%d", cycle), sgAlpha)
				assertNoVlanDoubleHold(alphaValue)

				if cycle < cycles-1 {
					By(fmt.Sprintf("churn cycle %d: deleting the whole batch at once (grace 0)", cycle))
					Expect(frameWork.PodManager.DeleteAllPodsForcefully(ctx, labelKey, alphaValue)).To(Succeed())
					livePods = nil
				}
			}
		})
	})
})
