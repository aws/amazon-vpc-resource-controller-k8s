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

package metrics

const (
	describeENICallCount   = "ec2_describe_network_interface_api_req_count"
	totalMemAllocatedBytes = "go_memstats_alloc_bytes_total"
	goRoutineCount         = "go_goroutines"
)

type TestMetric struct {
	MetricsName      string
	MetricsThreshold float64
}

// NewTestingMetrics is used to add more metrics and monitoring thresholds for regression tests.
// To add another metrics
//   1, add the metrics name into constant variable
//   2, add expected upper threshold into this map
func NewTestingMetrics() map[string]TestMetric {
	return map[string]TestMetric{
		describeENICallCount: {
			MetricsName:      "ec2_describe_network_interface_api_req_count",
			MetricsThreshold: 10.0,
		},
		totalMemAllocatedBytes: {
			MetricsName:      "go_memstats_alloc_bytes_total",
			MetricsThreshold: 1.5,
		},
		goRoutineCount: {
			MetricsName:      "go_goroutines",
			MetricsThreshold: 1.25,
		},
	}
}
