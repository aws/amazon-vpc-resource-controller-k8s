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

package branch

import (
	"context"
	"slices"
	"time"

	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/vpc"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"golang.org/x/time/rate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
)

type restoredNode struct {
	name         string
	instanceID   string
	instanceType string
}
type restoredNodeState int

const (
	restoredNodePrepared restoredNodeState = iota
	restoredNodeRemoved
	restoredNodeReplaced
	restoredNodeNotEligibleForBranchProvider
	restoredNodeInitializationPending
	restoredNodeNeedsPreparation
)

func (b *branchENIProvider) Start(ctx context.Context) error {
	for {
		cniNodes, err := b.apiWrapper.K8sAPI.ListCNINodes()
		if err != nil {
			b.log.Error(err, "waiting to capture restored nodes")
			if !waitForRestoredNodeCheck(ctx, preparationRetryPeriod) {
				return nil
			}
			continue
		}
		var restoredNodes []restoredNode
		for _, cniNode := range cniNodes {
			if cniNode.Spec.ManagedBy != "" &&
				cniNode.Spec.ManagedBy != rcv1alpha1.ManagedByVPCResourceController {
				continue
			}
			state := cniNode.Status.NodeNetworkState
			if state == nil || state.InstanceID == "" ||
				cniNode.Status.TrunkInterface == nil || cniNode.Status.TrunkInterface.ID == "" {
				continue
			}
			restoredNodes = append(restoredNodes, restoredNode{
				name:         cniNode.Name,
				instanceID:   state.InstanceID,
				instanceType: state.InstanceType,
			})
		}
		return b.runRestoredNodeChecks(
			ctx,
			restoredNodes,
			RestoredNodeCheckGracePeriod,
			preparationRetryPeriod,
			rate.NewLimiter(rate.Limit(RestoredNodeCheckRatePerSecond), restoredNodeCheckBurst),
		)
	}
}

func waitForRestoredNodeCheck(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (b *branchENIProvider) runRestoredNodeChecks(
	ctx context.Context,
	restoredNodes []restoredNode,
	gracePeriod time.Duration,
	retryPeriod time.Duration,
	limiter *rate.Limiter,
) error {
	if !waitForRestoredNodeCheck(ctx, gracePeriod) {
		return nil
	}
	podDataStoreSynced := b.conditions.GetPodDataStoreSyncStatus()
	if !podDataStoreSynced {
		b.log.Info("waiting to check restored nodes until pod datastore is synced")
	}
	for !podDataStoreSynced {
		if !waitForRestoredNodeCheck(ctx, retryPeriod) {
			return nil
		}
		podDataStoreSynced = b.conditions.GetPodDataStoreSyncStatus()
	}
	pendingNodes := restoredNodes
	for len(pendingNodes) != 0 {
		failedNodes := make([]restoredNode, 0, len(pendingNodes))
		for _, node := range pendingNodes {
			state, trunkENI := b.classifyRestoredNode(node)
			if state == restoredNodeNeedsPreparation {
				if err := limiter.Wait(ctx); err != nil {
					return nil
				}
				state, trunkENI = b.classifyRestoredNode(node)
			}
			switch state {
			case restoredNodeInitializationPending:
				failedNodes = append(failedNodes, node)
			case restoredNodeNeedsPreparation:
				if err := b.prepareTrunkForAllocation(node.name, trunkENI); err != nil {
					failedNodes = append(failedNodes, node)
				}
			}
		}
		pendingNodes = failedNodes
		if len(pendingNodes) != 0 && !waitForRestoredNodeCheck(ctx, retryPeriod) {
			return nil
		}
	}
	return nil
}

func (b *branchENIProvider) classifyRestoredNode(node restoredNode) (restoredNodeState, trunk.TrunkENI) {
	currentNode, err := b.apiWrapper.K8sAPI.GetNode(node.name)
	if apierrors.IsNotFound(err) {
		return restoredNodeRemoved, nil
	}
	if err != nil {
		b.log.Error(err, "waiting to verify restored node", "node", node.name)
		return restoredNodeInitializationPending, nil
	}
	currentInstanceID := utils.GetNodeInstanceID(currentNode)
	switch {
	case currentInstanceID == "":
		return restoredNodeInitializationPending, nil
	case currentInstanceID != node.instanceID:
		return restoredNodeReplaced, nil
	}
	limits, instanceTypeSupported := vpc.Limits[node.instanceType]
	if utils.GetNodeOS(currentNode) != config.OSLinux || !instanceTypeSupported || !limits.IsTrunkingCompatible {
		return restoredNodeNotEligibleForBranchProvider, nil
	}
	// Keep in sync with canAttachTrunk in pkg/node/manager.
	if _, trunkAttached := currentNode.Labels[config.HasTrunkAttachedLabel]; !trunkAttached {
		cniNode, err := b.apiWrapper.K8sAPI.GetCNINode(types.NamespacedName{Name: node.name})
		switch {
		case apierrors.IsNotFound(err):
			return restoredNodeNotEligibleForBranchProvider, nil
		case err != nil:
			b.log.Error(err, "waiting to verify restored node eligibility", "node", node.name)
			return restoredNodeInitializationPending, nil
		}
		if !slices.ContainsFunc(cniNode.Spec.Features, func(feature rcv1alpha1.Feature) bool {
			return feature.Name == rcv1alpha1.SecurityGroupsForPods
		}) {
			return restoredNodeNotEligibleForBranchProvider, nil
		}
	}
	trunkENI, present := b.getTrunkFromCache(node.name)
	switch {
	case !present:
		return restoredNodeInitializationPending, nil
	case trunkENI.InstanceID() != node.instanceID:
		return restoredNodeReplaced, nil
	case trunkENI.NeedsPreparation():
		return restoredNodeNeedsPreparation, trunkENI
	default:
		return restoredNodePrepared, nil
	}
}
