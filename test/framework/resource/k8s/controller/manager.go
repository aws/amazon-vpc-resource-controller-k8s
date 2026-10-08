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

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	coordinationv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DeploymentName  = "vpc-resource-local-controller"
	Namespace       = "kube-system"
	LeaderLeaseName = "cp-vpc-resource-controller"

	PodLabelKey = "app"
	PodLabelVal = "vpc-resource-controller"

	ClusterRoleName = "vpc-resource-controller-role"
)

type LeaderLease struct {
	HolderIdentity string
	PodName        string
	AcquisitionID  string
}

type Manager interface {
	WaitForActiveLeader(ctx context.Context, previousAcquisitionID string) (LeaderLease, error)
	WaitForLeaderLeaseExpiry(ctx context.Context) error
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

type defaultManager struct {
	k8sClient client.Client
}

func (d *defaultManager) WaitForActiveLeader(
	ctx context.Context,
	previousAcquisitionID string,
) (LeaderLease, error) {
	activeLeader := LeaderLease{}
	err := wait.PollUntilContextCancel(
		ctx,
		utils.PollIntervalShort,
		true,
		func(ctx context.Context) (bool, error) {
			lease := &coordinationv1.Lease{}
			if err := d.k8sClient.Get(ctx, types.NamespacedName{
				Namespace: Namespace,
				Name:      LeaderLeaseName,
			}, lease); err != nil {
				return false, err
			}
			expired, err := leaderLeaseExpired(lease, time.Now())
			if err != nil || expired {
				return false, err
			}
			acquisitionID, err := leaderLeaseAcquisitionID(lease)
			if err != nil || acquisitionID == previousAcquisitionID {
				return false, err
			}

			controllerPods := &v1.PodList{}
			if err := d.k8sClient.List(
				ctx,
				controllerPods,
				client.InNamespace(Namespace),
				client.MatchingLabels{PodLabelKey: PodLabelVal},
			); err != nil {
				return false, err
			}
			for _, pod := range controllerPods.Items {
				if strings.HasPrefix(*lease.Spec.HolderIdentity, pod.Name+"_") {
					activeLeader = LeaderLease{
						HolderIdentity: *lease.Spec.HolderIdentity,
						PodName:        pod.Name,
						AcquisitionID:  acquisitionID,
					}
					return true, nil
				}
			}
			return false, nil
		},
	)
	return activeLeader, err
}

func (d *defaultManager) WaitForLeaderLeaseExpiry(ctx context.Context) error {
	return wait.PollUntilContextCancel(
		ctx,
		utils.PollIntervalShort,
		true,
		func(ctx context.Context) (bool, error) {
			lease := &coordinationv1.Lease{}
			if err := d.k8sClient.Get(ctx, types.NamespacedName{
				Namespace: Namespace,
				Name:      LeaderLeaseName,
			}, lease); err != nil {
				return false, err
			}
			return leaderLeaseExpired(lease, time.Now())
		},
	)
}

func leaderLeaseExpired(lease *coordinationv1.Lease, now time.Time) (bool, error) {
	if lease.Spec.RenewTime == nil || lease.Spec.LeaseDurationSeconds == nil {
		return false, fmt.Errorf("controller leader lease has no renewal deadline")
	}
	expiresAt := lease.Spec.RenewTime.Add(
		time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second,
	)
	return !expiresAt.After(now), nil
}

func leaderLeaseAcquisitionID(lease *coordinationv1.Lease) (string, error) {
	if lease.Spec.HolderIdentity == nil ||
		lease.Spec.AcquireTime == nil ||
		lease.Spec.LeaseTransitions == nil {
		return "", fmt.Errorf("controller leader lease has no acquisition identity")
	}
	return fmt.Sprintf(
		"%s|%s|%d",
		*lease.Spec.HolderIdentity,
		lease.Spec.AcquireTime.UTC().Format(time.RFC3339Nano),
		*lease.Spec.LeaseTransitions,
	), nil
}
