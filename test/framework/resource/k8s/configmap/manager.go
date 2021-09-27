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

package configmap

import (
	"context"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Manager interface {
	CreateConfigMap(ctx context.Context, configMap *v1.ConfigMap) error
	GetConfigMap(ctx context.Context, configMap *v1.ConfigMap) (*v1.ConfigMap, error)
	UpdateConfigMap(ctx context.Context, configMap *v1.ConfigMap) error
	DeleteConfigMap(ctx context.Context, configMap *v1.ConfigMap) error
}

type defaultManager struct {
	k8sClient client.Client
}

func NewManager(k8sClient client.Client) Manager {
	return &defaultManager{k8sClient: k8sClient}
}

func (d *defaultManager) GetConfigMap(ctx context.Context, configMap *v1.ConfigMap) (*v1.ConfigMap, error) {
	// Create configmap if it doesn't exist and if flag not true
	observedConfigMap := &v1.ConfigMap{}
	err := d.k8sClient.Get(ctx, utils.NamespacedName(configMap), observedConfigMap)
	return observedConfigMap, err

}

func (d *defaultManager) CreateConfigMap(ctx context.Context, configMap *v1.ConfigMap) error {
	return d.k8sClient.Create(ctx, configMap)
}

func (d *defaultManager) UpdateConfigMap(ctx context.Context, configMap *v1.ConfigMap) error {
	return d.k8sClient.Update(ctx, configMap)
}

func (d *defaultManager) DeleteConfigMap(ctx context.Context, configMap *v1.ConfigMap) error {
	return d.k8sClient.Delete(ctx, configMap)
}
