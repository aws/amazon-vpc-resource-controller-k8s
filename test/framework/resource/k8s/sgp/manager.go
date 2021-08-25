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

package sgp

import (
	"context"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	DeleteAndWaitTillSecurityGroupIsDeleted(ctx context.Context, sgp *v1beta1.SecurityGroupPolicy) error
}

type defaultManager struct {
	k8sClient client.Client
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

func (d *defaultManager) DeleteAndWaitTillSecurityGroupIsDeleted(ctx context.Context, sgp *v1beta1.SecurityGroupPolicy) error {
	err := d.k8sClient.Delete(ctx, sgp)
	if err != nil {
		return err
	}

	observedSgp := &v1beta1.SecurityGroupPolicy{}
	return wait.PollUntil(utils.PollIntervalShort, func() (done bool, err error) {
		err = d.k8sClient.Get(ctx, utils.NamespacedName(sgp), observedSgp)
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, ctx.Done())
}
