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

package v1alpha1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTrunkInterfaceSubnetIDWireFormat pins the serialized field name and the
// omitempty behaviour. Node-side components read this off the CNINode by its
// wire name, so renaming it is a breaking change, and emitting an empty
// subnetID would fail the CRD's pattern validation on objects that legitimately
// have no subnet recorded.
func TestTrunkInterfaceSubnetIDWireFormat(t *testing.T) {
	withSubnet, err := json.Marshal(TrunkInterface{ID: "eni-1a2b3c4d", SubnetID: "subnet-0123456789abcdef0"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"eni-1a2b3c4d","subnetID":"subnet-0123456789abcdef0"}`, string(withSubnet))

	// Unset must be omitted entirely, not serialized as "".
	withoutSubnet, err := json.Marshal(TrunkInterface{ID: "eni-1a2b3c4d"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"eni-1a2b3c4d"}`, string(withoutSubnet))

	var round TrunkInterface
	require.NoError(t, json.Unmarshal(withSubnet, &round))
	assert.Equal(t, "subnet-0123456789abcdef0", round.SubnetID)
}

// TestTrunkInterfaceDeepCopyCarriesSubnetID guards against the generated
// deepcopy dropping the field, which would silently lose it on any controller
// that mutates a cached CNINode copy.
func TestTrunkInterfaceDeepCopyCarriesSubnetID(t *testing.T) {
	original := &TrunkInterface{
		ID:       "eni-1a2b3c4d",
		SubnetID: "subnet-0123456789abcdef0",
		Branches: []BranchInterface{{ID: "eni-9f8e7d6c", VlanID: 1}},
	}

	copied := original.DeepCopy()
	assert.Equal(t, original, copied)
	assert.Equal(t, "subnet-0123456789abcdef0", copied.SubnetID)

	// Mutating the copy must not touch the original (independent memory).
	copied.SubnetID = "subnet-1a2b3c4d"
	assert.Equal(t, "subnet-0123456789abcdef0", original.SubnetID)
}
