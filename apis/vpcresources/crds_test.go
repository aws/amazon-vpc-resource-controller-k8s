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

package vpcresources

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCRDsUnmarshal verifies the embedded CRD YAML parses into valid objects
// with the fields downstream installers depend on.
func TestCRDsUnmarshal(t *testing.T) {
	require.Len(t, CRDs, 2)

	byName := map[string]int{}
	for i, crd := range CRDs {
		byName[crd.Name] = i
	}
	require.Contains(t, byName, "cninodes.vpcresources.k8s.aws")
	require.Contains(t, byName, "securitygrouppolicies.vpcresources.k8s.aws")

	cniNode := CRDs[byName["cninodes.vpcresources.k8s.aws"]]
	require.Len(t, cniNode.Spec.Versions, 1)
	version := cniNode.Spec.Versions[0]

	// Fields downstream controllers check to decide whether the installed CRD
	// needs upgrading to this module's schema.
	assert.NotNil(t, version.Subresources.Status, "status subresource must be enabled")
	statusProps := version.Schema.OpenAPIV3Schema.Properties["status"].Properties
	assert.Contains(t, statusProps, "trunkInterface")
	specProps := version.Schema.OpenAPIV3Schema.Properties["spec"].Properties
	assert.Contains(t, specProps, "managedBy")
	require.Len(t, version.SelectableFields, 1)
	assert.Equal(t, ".spec.managedBy", version.SelectableFields[0].JSONPath)
}

// TestEmbeddedCRDsMatchGenerated fails if the embedded copies drift from the
// controller-gen output in config/crd/bases (make verify keeps them in sync).
func TestEmbeddedCRDsMatchGenerated(t *testing.T) {
	for file, embedded := range map[string][]byte{
		"vpcresources.k8s.aws_cninodes.yaml":              CNINodeCRDBytes,
		"vpcresources.k8s.aws_securitygrouppolicies.yaml": SecurityGroupPolicyCRDBytes,
	} {
		generated, err := os.ReadFile("../../config/crd/bases/" + file)
		require.NoError(t, err)
		assert.Equal(t, string(generated), string(embedded),
			"%s drifted from config/crd/bases; run make verify", file)
	}
}
