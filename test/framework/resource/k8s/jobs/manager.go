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

package jobs

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	batchV1 "k8s.io/api/batch/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	CreateAndWaitForJobToComplete(ctx context.Context, jobs *batchV1.Job) (*batchV1.Job, error)
	CreateJobAndWaitForJobToRun(ctx context.Context, jobs *batchV1.Job) (*batchV1.Job, error)
	DeleteAndWaitTillJobIsDeleted(ctx context.Context, jobs *batchV1.Job) error
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

type defaultManager struct {
	k8sClient client.Client
}

func (m *defaultManager) CreateJobAndWaitForJobToRun(ctx context.Context,
	jobs *batchV1.Job) (*batchV1.Job, error) {
	err := m.k8sClient.Create(ctx, jobs)
	if err != nil {
		return nil, err
	}

	observedJob := &batchV1.Job{}
	return observedJob, wait.PollUntil(utils.PollIntervalShort, func() (bool, error) {
		podList := &v1.PodList{}
		err = m.k8sClient.List(ctx, podList, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(jobs.Spec.Template.Labels),
		})
		if err != nil {
			return false, err
		}
		if err := m.k8sClient.Get(ctx, utils.NamespacedName(jobs), observedJob); err != nil {
			return false, err
		}
		if observedJob.Status.Succeeded > 0 || observedJob.Status.Failed > 0 {
			return false, fmt.Errorf("some jobs has either"+
				" succeeded or failed :%v", observedJob.Status)
		} else if observedJob.Status.Active > 0 {
			var count int
			for _, pod := range podList.Items {
				if pod.Status.Phase == v1.PodRunning && strings.Contains(pod.Name, jobs.Name) {
					count++
				}
			}
			return count == int(*jobs.Spec.Parallelism), nil
		}
		return false, nil
	}, ctx.Done())
}

func (m *defaultManager) CreateAndWaitForJobToComplete(ctx context.Context,
	jobs *batchV1.Job) (*batchV1.Job, error) {

	err := m.k8sClient.Create(ctx, jobs)
	if err != nil {
		return nil, err
	}

	observedJob := &batchV1.Job{}
	return observedJob, wait.PollUntil(utils.PollIntervalShort, func() (bool, error) {
		if err := m.k8sClient.Get(ctx, utils.NamespacedName(jobs), observedJob); err != nil {
			return false, err
		}
		if observedJob.Status.Failed > 0 {
			return false, fmt.Errorf("failed to execute job :%v", observedJob.Status)
		} else if observedJob.Status.Succeeded == (*jobs.Spec.Parallelism) {
			return true, nil
		}
		return false, nil
	}, ctx.Done())
}

func (m *defaultManager) DeleteAndWaitTillJobIsDeleted(ctx context.Context, job *batchV1.Job) error {
	err := m.k8sClient.Delete(ctx, job)
	if err != nil {
		return err
	}
	observedJob := &batchV1.Job{}

	err = wait.PollUntil(utils.PollIntervalShort, func() (bool, error) {
		if err := m.k8sClient.Get(ctx, utils.NamespacedName(job), observedJob); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	}, ctx.Done())
	if err != nil {
		return err
	}

	// Deleting the Job will not removed the Job Pods, they have to be manually deleted
	jobPods := &v1.PodList{}
	err = m.k8sClient.List(ctx, jobPods, &client.ListOptions{
		LabelSelector: labels.SelectorFromSet(job.ObjectMeta.Labels),
		Namespace:     v1.NamespaceAll,
	})
	if err != nil {
		return err
	}
	for _, pod := range jobPods.Items {
		err = m.k8sClient.Delete(ctx, &pod)
		if err != nil {
			return err
		}
	}
	return nil
}
