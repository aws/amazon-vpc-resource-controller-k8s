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
	"golang.org/x/exp/constraints"
)

func Minimum[T constraints.Ordered](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func Maximum[T constraints.Ordered](a, b T) T {
	if a > b {
		return a
	}
	return b
}

func MinOf[T constraints.Ordered](vars ...T) T {
	result := vars[0]
	for _, v := range vars {
		if result > v {
			result = v
		}
	}
	return result
}

func MaxOf[T constraints.Ordered](vars ...T) T {
	result := vars[0]
	for _, v := range vars {
		if result < v {
			result = v
		}
	}
	return result
}

// CeilDivision returns the ceiling result of numerator x divided by denominator y. y should be non-zero
func CeilDivision(x, y int) int {
	return (x + y - 1) / y
}

// IntPower calculates n to the mth power. m should be a non-negative power to have an int return value
func IntPower(n, m int) int {
	if m == 0 {
		return 1
	}
	result := n
	for i := 1; i < m; i++ {
		result *= n
	}
	return result
}
