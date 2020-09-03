/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deployment

import (
	"context"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	CreateAndWaitUntilDeploymentReady(ctx context.Context, dp *appsv1.Deployment) (*appsv1.Deployment, error)
	DeleteAndWaitUntilDeploymentDeleted(ctx context.Context, dp *appsv1.Deployment) error
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

type defaultManager struct {
	k8sClient client.Client
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
