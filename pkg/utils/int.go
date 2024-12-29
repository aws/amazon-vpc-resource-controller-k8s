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

import "errors"

func Int64ToInt32(value int64) (int32, error) {
	const (
		minInt32 = -2147483648
		maxInt32 = 2147483647
	)

	if value < minInt32 || value > maxInt32 {
		return 0, errors.New("value out of int32 range")
	}

	return int32(value), nil
}

func IntToInt32(value int) (int32, error) {
	const (
		minInt32 = -2147483648
		maxInt32 = 2147483647
	)

	if value < minInt32 || value > maxInt32 {
		return 0, errors.New("value out of int32 range")
	}

	return int32(value), nil
}
