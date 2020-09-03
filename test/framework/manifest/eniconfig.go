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
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	"github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ENIConfigBuilder struct {
	name          string
	subnetID      string
	securityGroup []string
}

func NewENIConfigBuilder() *ENIConfigBuilder {
	return &ENIConfigBuilder{
		name: utils.ResourceNamePrefix + "eniConfig",
	}
}

func (e *ENIConfigBuilder) Name(name string) *ENIConfigBuilder {
	e.name = name
	return e
}

func (e *ENIConfigBuilder) SubnetID(subnetID string) *ENIConfigBuilder {
	e.subnetID = subnetID
	return e
}

func (e *ENIConfigBuilder) SecurityGroup(securityGroup []string) *ENIConfigBuilder {
	e.securityGroup = securityGroup
	return e
}

func (e *ENIConfigBuilder) Build() (*v1alpha1.ENIConfig, error) {
	if e.subnetID == "" {
		return nil, fmt.Errorf("subnet id is a required field")
	}

	return &v1alpha1.ENIConfig{
		ObjectMeta: v1.ObjectMeta{
			Name: e.name,
		},
		Spec: v1alpha1.ENIConfigSpec{
			SecurityGroups: e.securityGroup,
			Subnet:         e.subnetID,
		},
	}, nil
}
