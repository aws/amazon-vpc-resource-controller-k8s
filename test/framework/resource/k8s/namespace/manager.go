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

package namespace

import (
	"context"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	CreateNamespace(ctx context.Context, namespace string) error
	DeleteAndWaitTillNamespaceDeleted(ctx context.Context, namespace string) error
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

type defaultManager struct {
	k8sClient client.Client
}

func (m *defaultManager) CreateNamespace(ctx context.Context, namespace string) error {
	if namespace == "default" {
		return nil
	}
	return m.k8sClient.Create(ctx, &v1.Namespace{ObjectMeta: metaV1.ObjectMeta{Name: namespace}})
}

func (m *defaultManager) DeleteAndWaitTillNamespaceDeleted(ctx context.Context, namespace string) error {
	if namespace == "default" {
		return nil
	}
	namespaceObj := &v1.Namespace{ObjectMeta: metaV1.ObjectMeta{Name: namespace, Namespace: ""}}
	err := m.k8sClient.Delete(ctx, namespaceObj)
	if err != nil {
		return err
	}

	observedNamespace := &v1.Namespace{}
	return wait.PollUntil(utils.PollIntervalShort, func() (done bool, err error) {
		err = m.k8sClient.Get(ctx, utils.NamespacedName(namespaceObj), observedNamespace)
		if errors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	}, ctx.Done())
}
