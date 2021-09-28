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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DeploymentName     = "vpc-resource-local-controller"
	Namespace          = "kube-system"
	LeaderLeaseMapName = "cp-vpc-resource-controller"

	PodLabelKey = "app"
	PodLabelVal = "vpc-resource-controller"

	ClusterRoleName = "vpc-resource-controller-role"
)

type LeaderLease struct {
	HolderIdentity string
}

type Manager interface {
	WaitTillControllerHasLeaderLease(ctx context.Context) (string, error)
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

type defaultManager struct {
	k8sClient client.Client
}

func (d *defaultManager) WaitTillControllerHasLeaderLease(ctx context.Context) (string, error) {
	leaderLeaseMap := &v1.ConfigMap{}

	controllerPods := &v1.PodList{}
	err := d.k8sClient.List(ctx, controllerPods, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{PodLabelKey: PodLabelVal}),
	})
	if err != nil {
		return "", err
	}

	var leaderPodName string
	err = wait.PollUntil(utils.PollIntervalMedium, func() (done bool, err error) {
		err = d.k8sClient.Get(ctx, types.NamespacedName{
			Namespace: Namespace,
			Name:      LeaderLeaseMapName,
		}, leaderLeaseMap)
		if err != nil {
			return false, err
		}
		key, found := leaderLeaseMap.ObjectMeta.
			Annotations["control-plane.alpha.kubernetes.io/leader"]
		if !found {
			return false, fmt.Errorf("failed to find leader election configmap")
		}

		lease := &LeaderLease{}
		err = json.Unmarshal([]byte(key), lease)
		if err != nil {
			return false, err
		}

		for _, pod := range controllerPods.Items {
			// The Holder identity consists of the Leader Pod name + random UID
			if strings.Contains(lease.HolderIdentity, pod.Name) {
				leaderPodName = pod.Name
				return true, nil
			}
		}
		return false, nil
	}, ctx.Done())

	return leaderPodName, err
}
