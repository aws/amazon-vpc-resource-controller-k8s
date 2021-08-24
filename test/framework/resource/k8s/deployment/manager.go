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

package deployment

import (
	"context"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	"github.com/aws/aws-sdk-go/aws"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	CreateAndWaitUntilDeploymentReady(ctx context.Context, dp *appsv1.Deployment) (*appsv1.Deployment, error)
	DeleteAndWaitUntilDeploymentDeleted(ctx context.Context, dp *appsv1.Deployment) error
	ScaleDeploymentAndWaitTillReady(ctx context.Context, namespace string, name string, replicas int32) error
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

type defaultManager struct {
	k8sClient client.Client
}

func (m *defaultManager) ScaleDeployment(ctx context.Context, name string, namespace string, replicas int) error {
	var deployment *appsv1.Deployment
	err := m.k8sClient.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, deployment)
	if err != nil {
		return err
	}

	scaledDeployment := deployment.DeepCopy()
	scaledDeployment.Spec.Replicas = aws.Int32(int32(replicas))

	err = m.k8sClient.Patch(ctx, scaledDeployment, client.MergeFrom(deployment))
	if err != nil {
		return err
	}

	observedDP := &appsv1.Deployment{}
	return wait.PollImmediateUntil(utils.PollIntervalShort, func() (bool, error) {
		if err := m.k8sClient.Get(ctx, utils.NamespacedName(scaledDeployment), observedDP); err != nil {
			return false, err
		}
		if observedDP.Status.UpdatedReplicas == (*scaledDeployment.Spec.Replicas) &&
			observedDP.Status.Replicas == (*scaledDeployment.Spec.Replicas) &&
			observedDP.Status.AvailableReplicas == (*scaledDeployment.Spec.Replicas) &&
			observedDP.Status.ObservedGeneration >= scaledDeployment.Generation {
			return true, nil
		}
		return false, nil
	}, ctx.Done())
}

func (m *defaultManager) CreateAndWaitUntilDeploymentReady(ctx context.Context, dp *appsv1.Deployment) (*appsv1.Deployment, error) {
	err := m.k8sClient.Create(ctx, dp)
	if err != nil {
		return nil, err
	}

	// Wait till the cache is refreshed
	time.Sleep(utils.PollIntervalShort)

	observedDP := &appsv1.Deployment{}
	return observedDP, wait.PollImmediateUntil(utils.PollIntervalShort, func() (bool, error) {
		if err := m.k8sClient.Get(ctx, utils.NamespacedName(dp), observedDP); err != nil {
			return false, err
		}
		if observedDP.Status.UpdatedReplicas == (*dp.Spec.Replicas) &&
			observedDP.Status.Replicas == (*dp.Spec.Replicas) &&
			observedDP.Status.AvailableReplicas == (*dp.Spec.Replicas) &&
			observedDP.Status.ObservedGeneration >= dp.Generation {
			return true, nil
		}
		return false, nil
	}, ctx.Done())
}

func (m *defaultManager) DeleteAndWaitUntilDeploymentDeleted(ctx context.Context, dp *appsv1.Deployment) error {
	err := m.k8sClient.Delete(ctx, dp)
	if err != nil {
		return err
	}
	observedDP := &appsv1.Deployment{}
	return wait.PollImmediateUntil(utils.PollIntervalShort, func() (bool, error) {
		if err := m.k8sClient.Get(ctx, utils.NamespacedName(dp), observedDP); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	}, ctx.Done())
}

func (m *defaultManager) ScaleDeploymentAndWaitTillReady(ctx context.Context, namespace string, name string, replicas int32) error {
	deployment := &appsv1.Deployment{}
	namespacedName := types.NamespacedName{Namespace: namespace, Name: name}
	err := m.k8sClient.Get(ctx, namespacedName, deployment)
	if err != nil {
		return err
	}
	deploymentCopy := deployment.DeepCopy()
	deploymentCopy.Spec.Replicas = &replicas

	err = m.k8sClient.Patch(ctx, deploymentCopy, client.MergeFrom(deployment))
	if err != nil {
		return err
	}

	return wait.PollImmediateUntil(utils.PollIntervalShort, func() (bool, error) {
		if err := m.k8sClient.Get(ctx, namespacedName, deploymentCopy); err != nil {
			return false, err
		}
		if deploymentCopy.Status.AvailableReplicas == replicas &&
			deploymentCopy.Status.ObservedGeneration >= deployment.Generation {
			return true, nil
		}
		return false, nil
	}, ctx.Done())
}
