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

package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/provider/branch/trunk"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	CreateAndWaitTillPodIsRunning(context context.Context, pod *v1.Pod) (*v1.Pod, error)
	DeleteAndWaitTillPodIsDeleted(context context.Context, pod *v1.Pod) error
	GetENIDetailsFromPodAnnotation(podAnnotation map[string]string) ([]*trunk.ENIDetails, error)
	GetPodsWithLabel(context context.Context, namespace string, labelKey string, labelValue string) ([]v1.Pod, error)
}

type defaultManager struct {
	k8sClient client.Client
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

func (d *defaultManager) CreateAndWaitTillPodIsRunning(context context.Context, pod *v1.Pod) (*v1.Pod, error) {
	err := d.k8sClient.Create(context, pod)
	if err != nil {
		return nil, err
	}
	// Allow the cache to sync, without the interval cache may be stale and return an error
	time.Sleep(utils.PollIntervalShort)

	updatedPod := &v1.Pod{}
	err = wait.PollImmediateUntil(utils.PollIntervalShort, func() (done bool, err error) {
		err = d.k8sClient.Get(context, utils.NamespacedName(pod), updatedPod)
		if err != nil {
			return true, err
		}
		return isPodReady(updatedPod), nil
	}, context.Done())

	return updatedPod, err
}

func (d *defaultManager) GetPodsWithLabel(context context.Context, namespace string,
	labelKey string, labelValue string) ([]v1.Pod, error) {

	podList := &v1.PodList{}
	err := d.k8sClient.List(context, podList, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{labelKey: labelValue}),
		Namespace:     namespace,
	})

	return podList.Items, err
}

func (d *defaultManager) DeleteAndWaitTillPodIsDeleted(context context.Context, pod *v1.Pod) error {
	err := d.k8sClient.Delete(context, pod)
	if err != nil {
		return err
	}

	observedPod := &v1.Pod{}
	return wait.PollImmediateUntil(utils.PollIntervalShort, func() (done bool, err error) {
		err = d.k8sClient.Get(context, utils.NamespacedName(pod), observedPod)
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, context.Done())
}

func (d *defaultManager) GetENIDetailsFromPodAnnotation(podAnnotation map[string]string) ([]*trunk.ENIDetails, error) {
	branchDetails, hasAnnotation := podAnnotation[config.ResourceNamePodENI]
	if !hasAnnotation {
		return nil, fmt.Errorf("failed to find annotation on pod %v", podAnnotation)
	}
	eniDetails := []*trunk.ENIDetails{}
	json.Unmarshal([]byte(branchDetails), &eniDetails)

	return eniDetails, nil
}

func isPodReady(pod *v1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Status == v1.ConditionTrue && condition.Type == v1.PodReady {
			return true
		}
	}
	return false
}
