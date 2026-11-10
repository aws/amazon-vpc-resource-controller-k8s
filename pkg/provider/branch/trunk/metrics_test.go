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

package trunk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// The metrics agent skips a counter family on the first scrape that shows it, so each series it reads must be
// exposed from startup, before the first event.
func TestPrometheusRegister_ExposesAgentSeriesAtStartup(t *testing.T) {
	PrometheusRegister()

	assertSeriesExposed(t, "trunk_eni_operations_err_count", "operation",
		"restored_checkpoint_mismatch", "recover_branch_state", "get_vlan_from_tag",
		"deleted_pod_branch_eni_without_vlan", "orphan_branch_eni_vlan_in_use",
		"unusable_pod_eni_annotation", "branch_eni_unusable_vlan")
	assertSeriesExposed(t, "branch_eni_opeartions_success_count", "operation", "recover_branch_state")
}

func assertSeriesExposed(t *testing.T, family, label string, values ...string) {
	t.Helper()
	families, err := metrics.Registry.Gather()
	assert.NoError(t, err)
	exposed := map[string]bool{}
	for _, f := range families {
		if f.GetName() != family {
			continue
		}
		for _, metric := range f.GetMetric() {
			for _, pair := range metric.GetLabel() {
				if pair.GetName() == label {
					exposed[pair.GetValue()] = true
				}
			}
		}
	}
	for _, value := range values {
		assert.True(t, exposed[value], "%s{%s=%q} is not exposed", family, label, value)
	}
}
