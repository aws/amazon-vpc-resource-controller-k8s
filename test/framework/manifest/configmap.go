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

package manifest

import (
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ConfigMapBuilder struct {
	namespace string
	name      string
	data      map[string]string
}

func NewConfigMapBuilder() *ConfigMapBuilder {
	return &ConfigMapBuilder{
		namespace: "kube-system",
		name:      "amazon-vpc-cni",
		data:      map[string]string{config.EnableWindowsIPAMKey: "true"},
	}
}
func (c *ConfigMapBuilder) Build() *v1.ConfigMap {
	return &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.name,
			Namespace: c.namespace,
		},
		Data: c.data,
	}
}

func (c *ConfigMapBuilder) Name(name string) *ConfigMapBuilder {
	c.name = name
	return c
}
func (c *ConfigMapBuilder) Namespace(namespace string) *ConfigMapBuilder {
	c.namespace = namespace
	return c
}
func (c *ConfigMapBuilder) Data(data map[string]string) *ConfigMapBuilder {
	c.data = data
	return c
}
