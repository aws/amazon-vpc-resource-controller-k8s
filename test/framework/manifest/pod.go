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

	"github.com/aws/aws-sdk-go/aws"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PodBuilder struct {
	namespace              string
	name                   string
	serviceAccountName     string
	container              v1.Container
	os                     string
	labels                 map[string]string
	terminationGracePeriod int
}

func (p *PodBuilder) Build() (*v1.Pod, error) {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      p.name,
			Namespace: p.namespace,
			Labels:    p.labels,
		},
		Spec: v1.PodSpec{
			ServiceAccountName:            p.serviceAccountName,
			Containers:                    []v1.Container{p.container},
			NodeSelector:                  map[string]string{"kubernetes.io/os": p.os},
			TerminationGracePeriodSeconds: aws.Int64(int64(p.terminationGracePeriod)),
		},
	}, nil
}

func NewDefaultPodBuilder() *PodBuilder {
	return &PodBuilder{
		namespace:              "default",
		name:                   utils.ResourceNamePrefix + "pod",
		container:              BusyBoxContainer,
		os:                     "linux",
		labels:                 map[string]string{},
		terminationGracePeriod: 0,
	}
}

func (p *PodBuilder) Namespace(namespace string) *PodBuilder {
	p.namespace = namespace
	return p
}

func (p *PodBuilder) Name(name string) *PodBuilder {
	p.name = name
	return p
}

func (p *PodBuilder) Container(container v1.Container) *PodBuilder {
	p.container = container
	return p
}

func (p *PodBuilder) OS(os string) *PodBuilder {
	p.os = os
	return p
}

func (p *PodBuilder) Labels(labels map[string]string) *PodBuilder {
	p.labels = labels
	return p
}

func (p *PodBuilder) ServiceAccount(serviceAccountName string) *PodBuilder {
	p.serviceAccountName = serviceAccountName
	return p
}

func (p *PodBuilder) TerminationGracePeriod(terminationGracePeriod int) *PodBuilder {
	p.terminationGracePeriod = terminationGracePeriod
	return p
}
