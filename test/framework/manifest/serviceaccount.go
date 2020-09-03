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

package manifest

import (
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ServiceAccountBuilder struct {
	namespace string
	name      string
	labels    map[string]string
}

func NewServiceAccountBuilder() *ServiceAccountBuilder {
	return &ServiceAccountBuilder{
		namespace: "default",
		name:      utils.ResourceNamePrefix + "sa",
	}
}

func (s *ServiceAccountBuilder) Name(name string) *ServiceAccountBuilder {
	s.name = name
	return s
}

func (s *ServiceAccountBuilder) Namespace(namespace string) *ServiceAccountBuilder {
	s.namespace = namespace
	return s
}

func (s *ServiceAccountBuilder) Label(labelKey string, labelValue string) *ServiceAccountBuilder {
	if s.labels == nil {
		s.labels = map[string]string{}
	}
	s.labels[labelKey] = labelValue
	return s
}

func (s *ServiceAccountBuilder) Build() *v1.ServiceAccount {
	return &v1.ServiceAccount{
		ObjectMeta: metaV1.ObjectMeta{
			Name:      s.name,
			Namespace: s.namespace,
			Labels:    s.labels,
		},
	}
}
