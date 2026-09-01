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
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCRDsUnmarshal verifies the embedded CRD YAML parses into valid objects
// with the fields downstream installers depend on.
func TestCRDsUnmarshal(t *testing.T) {
	require.Len(t, CRDs, 2)
	assert.Equal(t, "cninodes.vpcresources.k8s.aws", CNINodeCRD.Name)
	assert.Equal(t, "securitygrouppolicies.vpcresources.k8s.aws", SecurityGroupPolicyCRD.Name)
	assert.Contains(t, CRDs, CNINodeCRD)
	assert.Contains(t, CRDs, SecurityGroupPolicyCRD)

	require.Len(t, CNINodeCRD.Spec.Versions, 1)
	version := CNINodeCRD.Spec.Versions[0]

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

// TestTrunkInterfaceSubnetIDSchema pins the trunk subnetID contract: a
// controller reads this field instead of calling EC2 DescribeNetworkInterfaces
// to learn where to create branch ENIs, so both the property name and the
// server-side validation are part of the API surface consumers depend on.
func TestTrunkInterfaceSubnetIDSchema(t *testing.T) {
	require.Len(t, CNINodeCRD.Spec.Versions, 1)
	trunk := CNINodeCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.
		Properties["status"].Properties["trunkInterface"]

	subnetID, ok := trunk.Properties["subnetID"]
	require.True(t, ok, "status.trunkInterface.subnetID must be in the schema")
	assert.Equal(t, "string", subnetID.Type)
	assert.Equal(t, `^subnet-([0-9a-f]{8}|[0-9a-f]{17})$`, subnetID.Pattern)
	require.NotNil(t, subnetID.MaxLength)
	assert.EqualValues(t, 24, *subnetID.MaxLength)

	// Optional: only the trunk's own id is required, so existing objects and
	// controllers that never set a subnet stay valid.
	assert.NotContains(t, trunk.Required, "subnetID")
	assert.Equal(t, []string{"id"}, trunk.Required)

	// Exercise the pattern the API server will enforce, so a typo in it cannot
	// silently reject real subnet ids. EC2 issues both the legacy 8-hex and
	// the current 17-hex forms.
	re, err := regexp.Compile(subnetID.Pattern)
	require.NoError(t, err)
	for _, valid := range []string{"subnet-1a2b3c4d", "subnet-0123456789abcdef0", "subnet-00000000"} {
		assert.True(t, re.MatchString(valid), "%q must be accepted", valid)
	}
	for _, invalid := range []string{
		"", "subnet-", "subnet-1a2b3c4", "subnet-1a2b3c4de", // wrong hex length
		"subnet-0123456789ABCDEF0",     // uppercase hex
		"eni-1a2b3c4d", "vpc-1a2b3c4d", // wrong resource type
		"subnet-1a2b3c4d ", " subnet-1a2b3c4d", // surrounding whitespace
	} {
		assert.False(t, re.MatchString(invalid), "%q must be rejected", invalid)
	}
}

func TestNodeNetworkStateSchema(t *testing.T) {
	require.Len(t, CNINodeCRD.Spec.Versions, 1)
	status := CNINodeCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"]
	state, ok := status.Properties["nodeNetworkState"]
	require.True(t, ok, "status.nodeNetworkState must be in the schema")

	assert.NotContains(t, status.Required, "nodeNetworkState")
	assert.ElementsMatch(t, []string{
		"instanceID",
		"subnetID",
		"subnetCIDRBlock",
		"primaryNetworkInterfaceSecurityGroups",
	}, state.Required)

	expectedFields := []string{
		"instanceID",
		"subnetID",
		"subnetCIDRBlock",
		"subnetV6CIDRBlock",
		"primaryNetworkInterfaceSecurityGroups",
		"connectionTracking",
	}
	assert.Len(t, state.Properties, len(expectedFields))
	for _, field := range expectedFields {
		assert.Contains(t, state.Properties, field)
	}

	instanceID := state.Properties["instanceID"]
	assert.Equal(t, `^i-([0-9a-f]{8}|[0-9a-f]{17})$`, instanceID.Pattern)
	require.NotNil(t, instanceID.MaxLength)
	assert.EqualValues(t, 19, *instanceID.MaxLength)

	subnetID := state.Properties["subnetID"]
	assert.Equal(t, `^subnet-([0-9a-f]{8}|[0-9a-f]{17})$`, subnetID.Pattern)
	require.NotNil(t, subnetID.MaxLength)
	assert.EqualValues(t, 24, *subnetID.MaxLength)

	securityGroups := state.Properties["primaryNetworkInterfaceSecurityGroups"]
	assert.Equal(t, "array", securityGroups.Type)
	require.NotNil(t, securityGroups.MinItems)
	assert.EqualValues(t, 1, *securityGroups.MinItems)
	require.NotNil(t, securityGroups.XListType)
	assert.Equal(t, "atomic", *securityGroups.XListType)

	connectionTracking := state.Properties["connectionTracking"]
	assert.Contains(t, connectionTracking.Properties, "tcpEstablishedTimeout")
	assert.Contains(t, connectionTracking.Properties, "udpStreamTimeout")
	assert.Contains(t, connectionTracking.Properties, "udpTimeout")
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
