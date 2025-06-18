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

import "time"

const (
	PollIntervalShort  = 2 * time.Second
	PollIntervalMedium = 10 * time.Second
	PollIntervalLong   = 20 * time.Second
	PollTimeout        = 30 * time.Second
	// ResourceOperationTimeout is the number of seconds till the controller waits
	// for the resource creation to complete
	ResourceOperationTimeout = 180 * time.Second
	// Windows Container Images are much larger in size and pulling them the first
	// time takes much longer, so have higher timeout for Windows Pod to be Ready
	WindowsPodsCreationTimeout = 240 * time.Second
	WindowsPodsDeletionTimeout = 60 * time.Second
)
