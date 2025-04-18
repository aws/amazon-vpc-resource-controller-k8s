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
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// Difference returns a-b, elements present in a and not in b
func Difference[T comparable](a, b []T) (diff []T) {
	m := make(map[T]struct{})

	for _, item := range b {
		m[item] = struct{}{}
	}
	for _, item := range a {
		if _, ok := m[item]; !ok {
			diff = append(diff, item)
		}
	}
	return
}

func GetKeyValSlice(m map[string]string) (key []string, val []string) {
	for k, v := range m {
		key = append(key, k)
		val = append(val, v)
	}
	return
}

func GetTagKeyValueMap(tagSet []ec2types.Tag) map[string]string {
	m := make(map[string]string)
	for _, tag := range tagSet {
		m[*tag.Key] = *tag.Value
	}
	return m
}
