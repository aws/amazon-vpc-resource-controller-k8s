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
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	annotations            map[string]string
	terminationGracePeriod int
	restartPolicy          v1.RestartPolicy
	nodeName               string
}

func (p *PodBuilder) Build() (*v1.Pod, error) {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        p.name,
			Namespace:   p.namespace,
			Labels:      p.labels,
			Annotations: p.annotations,
		},
		Spec: v1.PodSpec{
			NodeName:                      p.nodeName,
			ServiceAccountName:            p.serviceAccountName,
			Containers:                    []v1.Container{p.container},
			NodeSelector:                  map[string]string{"kubernetes.io/os": p.os},
			TerminationGracePeriodSeconds: aws.Int64(int64(p.terminationGracePeriod)),
			RestartPolicy:                 p.restartPolicy,
		},
	}, nil
}

func NewDefaultPodBuilder() *PodBuilder {
	return &PodBuilder{
		namespace:   "default",
		name:        utils.ResourceNamePrefix + "pod",
		container:   NewBusyBoxContainerBuilder().Build(),
		os:          "linux",
		labels:      map[string]string{},
		annotations: map[string]string{},
		// See https://github.com/aws/amazon-vpc-cni-k8s/issues/1313#issuecomment-901818609
		terminationGracePeriod: 10,
		restartPolicy:          v1.RestartPolicyNever,
	}
}

func NewWindowsPodBuilder() *PodBuilder {
	return &PodBuilder{
		namespace:              "windows-test",
		name:                   "windows-pod",
		container:              NewWindowsContainerBuilder().Build(),
		os:                     "windows",
		terminationGracePeriod: 0,
		restartPolicy:          v1.RestartPolicyNever,
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

func (p *PodBuilder) RestartPolicy(policy v1.RestartPolicy) *PodBuilder {
	p.restartPolicy = policy
	return p
}

func (p *PodBuilder) Labels(labels map[string]string) *PodBuilder {
	p.labels = labels
	return p
}

func (p *PodBuilder) Annotations(annotations map[string]string) *PodBuilder {
	p.annotations = annotations
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

func (p *PodBuilder) NodeName(nodeName string) *PodBuilder {
	p.nodeName = nodeName
	return p
}
