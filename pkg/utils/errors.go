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
	"errors"
	"strings"
)

var (
	ErrNotFound                   = errors.New("resource was not found")
	ErrInsufficientCidrBlocks     = errors.New("InsufficientCidrBlocks: The specified subnet does not have enough free cidr blocks to satisfy the request")
	ErrMsgProviderAndPoolNotFound = "cannot find the instance provider and pool from the cache"
	NotRetryErrors                = []string{InsufficientCidrBlocksReason}
)

// ShouldRetryOnError returns true if the error is retryable, else returns false
func ShouldRetryOnError(err error) bool {
	for _, e := range NotRetryErrors {
		if strings.HasPrefix(err.Error(), e) {
			return false
		}
	}
	return true
}
