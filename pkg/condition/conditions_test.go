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

package condition

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func Test_WaitTillPodDataStoreSynced(t *testing.T) {
	syncVariable := new(bool)
	c := condition{
		log:                zap.New(zap.UseDevMode(true)),
		hasDataStoreSynced: syncVariable,
	}

	var hasSynced bool
	go func() {
		c.WaitTillPodDataStoreSynced()
		hasSynced = true
	}()

	time.Sleep(CheckDataStoreSyncedInterval * 2)
	// If hasSynced is True then the function didn't wait for the
	// cache to be synced
	assert.False(t, hasSynced)

	// Set the variable so the previous go routine stops
	// and next call to the function doesn't block the test
	*syncVariable = true

	// If the code was buggy, this execution would block and
	// timeout the test
	c.WaitTillPodDataStoreSynced()
}
