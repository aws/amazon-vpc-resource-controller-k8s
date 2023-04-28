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

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type TestResource struct {
	GroupID    string
	ResourceID string
}

func TestDifference(t *testing.T) {
	a := []string{"X", "Y", "Z"}
	b := []string{"X", "Z", "Q"}

	assert.ElementsMatch(t, Difference(a, b), []string{"Y"})
	assert.ElementsMatch(t, Difference(b, a), []string{"Q"})
	assert.ElementsMatch(t, Difference(a, a), []string{})
}

func TestDifferenceResource(t *testing.T) {
	res1 := TestResource{GroupID: "res1", ResourceID: "res1"}
	res2 := TestResource{GroupID: "res2", ResourceID: "res2"}
	res3 := TestResource{GroupID: "res3", ResourceID: "res3"}
	a := []TestResource{res1, res2}
	b := []TestResource{res1, res3}
	c := []TestResource{res3}
	var d []TestResource

	assert.ElementsMatch(t, Difference(a, b), []TestResource{res2})
	assert.ElementsMatch(t, Difference(b, a), []TestResource{res3})
	assert.ElementsMatch(t, Difference(a, a), []TestResource{})
	assert.ElementsMatch(t, Difference(a, c), []TestResource{res1, res2})
	assert.ElementsMatch(t, Difference(c, a), []TestResource{res3})
	assert.ElementsMatch(t, Difference(d, a), d)
	assert.ElementsMatch(t, Difference(a, d), a)
	assert.ElementsMatch(t, Difference(d, d), d)
}

func TestGetKeySet(t *testing.T) {
	keys := []string{"a", "b", "c"}
	m := map[string]string{}

	for _, key := range keys {
		m[key] = key
	}

	k, v := GetKeyValSlice(m)
	assert.ElementsMatch(t, k, keys)
	assert.ElementsMatch(t, v, keys)
}
