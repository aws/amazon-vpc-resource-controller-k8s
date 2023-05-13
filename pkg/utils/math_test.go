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

func TestTwoElements(t *testing.T) {
	a := 10
	b := 11
	assert.True(t, Minimum(a, b) == a)
	assert.True(t, Maximum(a, b) == b)

	x := "ab"
	y := "ba"
	assert.True(t, Minimum(x, y) == x)
	assert.True(t, Maximum(x, y) == y)
}

func TestMultipleElements(t *testing.T) {
	a := 10
	b := 11
	c := 5
	vars := []int{a, b, c}
	assert.True(t, MinOf(vars...) == c)
	assert.True(t, MaxOf(vars...) == b)

	x := "ab"
	y := "aba"
	z := "aa"
	varStr := []string{x, y, z}

	assert.True(t, MinOf(varStr...) == z)
	assert.True(t, MaxOf(varStr...) == y)
}

func TestCeilDivision(t *testing.T) {
	assert.True(t, CeilDivision(0, 16) == 0)
	assert.True(t, CeilDivision(32, 1) == 32)
	assert.True(t, CeilDivision(32, 16) == 2)
	assert.True(t, CeilDivision(32, 30) == 2)
	assert.True(t, CeilDivision(50, 16) == 4)
}

func TestIntPower(t *testing.T) {
	assert.True(t, IntPower(2, 0) == 1)
	assert.True(t, IntPower(2, 4) == 16)
	assert.True(t, IntPower(-2, 2) == 4)
	assert.True(t, IntPower(-2, 3) == -8)
	assert.True(t, IntPower(-2, 0) == 1)
}
