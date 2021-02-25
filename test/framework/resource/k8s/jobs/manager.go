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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	batchV1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	CreateAndWaitForJobToComplete(ctx context.Context, jobs *batchV1.Job) (*batchV1.Job, error)
	DeleteAndWaitTillJobIsDeleted(ctx context.Context, jobs *batchV1.Job) error
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

type defaultManager struct {
	k8sClient client.Client
}

func (m *defaultManager) CreateAndWaitForJobToComplete(ctx context.Context,
	jobs *batchV1.Job) (*batchV1.Job, error) {

	err := m.k8sClient.Create(ctx, jobs)
	if err != nil {
		return nil, err
	}

	// Wait till the cache is refreshed
	time.Sleep(utils.PollIntervalShort)

	observedJob := &batchV1.Job{}
	return observedJob, wait.PollImmediateUntil(utils.PollIntervalShort, func() (bool, error) {
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
	return wait.PollImmediateUntil(utils.PollIntervalShort, func() (bool, error) {
		if err := m.k8sClient.Get(ctx, utils.NamespacedName(job), observedJob); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	}, ctx.Done())
}
