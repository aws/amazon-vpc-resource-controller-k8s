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

package node

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// A restart restores every node within one scrape, and the metrics agent skips a counter family on the first scrape
// that shows it, so every restore series must be exposed from startup or the restart's restores are lost.
func TestRegisterMetrics_ExposesRestoreSeriesAtStartup(t *testing.T) {
	RegisterMetrics()

	families, err := metrics.Registry.Gather()
	assert.NoError(t, err)
	exposed := map[[2]string]bool{}
	for _, family := range families {
		if family.GetName() != "cninode_checkpoint_restore_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			var result, reason string
			for _, pair := range metric.GetLabel() {
				switch pair.GetName() {
				case "result":
					result = pair.GetValue()
				case "reason":
					reason = pair.GetValue()
				}
			}
			exposed[[2]string{result, reason}] = true
		}
	}
	want := [][2]string{
		{checkpointRestoreResultHit, checkpointRestoreReasonNone},
		{checkpointRestoreResultError, checkpointRestoreReasonNetworkUpdate},
		{checkpointRestoreResultFallback, checkpointRestoreReasonReadError},
		{checkpointRestoreResultFallback, checkpointRestoreReasonForeignManager},
		{checkpointRestoreResultFallback, checkpointRestoreReasonMissingState},
		{checkpointRestoreResultFallback, checkpointRestoreReasonMissingField},
		{checkpointRestoreResultFallback, checkpointRestoreReasonInstanceIDMismatch},
		{checkpointRestoreResultFallback, checkpointRestoreReasonInvalidState},
	}
	for _, series := range want {
		assert.True(t, exposed[series], "cninode_checkpoint_restore_total{result=%q,reason=%q} is not exposed", series[0], series[1])
	}
}
