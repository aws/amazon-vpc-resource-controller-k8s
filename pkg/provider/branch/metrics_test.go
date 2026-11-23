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

package branch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// The metrics agent skips a counter family on the first scrape that shows it, so each series it reads must be
// exposed from startup, before the first error.
func TestPrometheusRegister_ExposesAgentSeriesAtStartup(t *testing.T) {
	prometheusRegister()

	families, err := metrics.Registry.Gather()
	assert.NoError(t, err)
	exposed := map[string]bool{}
	for _, family := range families {
		if family.GetName() != "branch_provider_operations_err_count" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, pair := range metric.GetLabel() {
				if pair.GetName() == operationLabel {
					exposed[pair.GetValue()] = true
				}
			}
		}
	}
	for _, operation := range []string{"prepare_trunk_for_allocation", "drop_unprepared_trunk"} {
		assert.True(t, exposed[operation], "branch_provider_operations_err_count{%s=%q} is not exposed", operationLabel, operation)
	}
}
