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

package vpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var exludedTypes = map[string]bool{"g5g.4xlarge": true, "i4i.16xlarge": true, "g5g.xlarge": true}

func TestInstanceTypesHavingNetworkMetadata(t *testing.T) {
	for instanceType, instance := range Limits {
		assert.Truef(t, len(instance.NetworkCards) > 0, "Every instance should have network card in %s", instanceType)
	}
}

func TestInstanceInterfacesMatchNetworkCards(t *testing.T) {
	for instanceType, instance := range Limits {
		// currently we are seeing some instance types has unmatched max ENIs between instance and sum of network cards
		// have reached out to EC2 for the inconsistency
		if _, ok := exludedTypes[instanceType]; ok {
			continue
		}
		totalInterfaces := instance.Interface
		for _, nc := range instance.NetworkCards {
			totalInterfaces -= int(nc.MaximumNetworkInterfaces)
		}
		assert.Truef(t, totalInterfaces == 0, "Instance's max interfaces should match the sum of network cards' interfaces in %s", instanceType)
	}
}

func TestBranchInterfaceMatchSupportFlag(t *testing.T) {
	for instanceType, instance := range Limits {
		if instance.IsTrunkingCompatible {
			assert.Truef(t, instance.BranchInterface > 0, "if instance support trunk it should have more than one branch interface in %s", instanceType)
		}
	}
}

func TestDefaultNetworkCardIndex(t *testing.T) {
	for instanceType, instance := range Limits {
		interfaces := 0
		for _, nc := range instance.NetworkCards {
			if nc.NetworkCardIndex == int64(instance.DefaultNetworkCardIndex) {
				interfaces = int(nc.MaximumNetworkInterfaces)
			}
		}
		assert.Truef(t, interfaces > 0, "instance default network card should be valid in %s", instanceType)
	}
}
