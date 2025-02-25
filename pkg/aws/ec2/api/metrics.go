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

package api

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	vpccniAvailableENICnt = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vpc_cni_created_available_eni_count",
			Help: "The number of available ENIs created by VPC-CNI that controller will try to delete in each cleanup cycle",
		},
	)

	vpcrcAvailableENICnt = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vpc_rc_created_available_eni_count",
			Help: "The number of available ENIs created by VPC-RC that controller will try to delete in each cleanup cycle",
		},
	)

	leakedENICnt = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "leaked_eni_count",
			Help: "The number of available ENIs that failed to be deleted by the controller in each cleanup cycle",
		},
	)
)

func init() {
	metrics.Registry.MustRegister(
		vpccniAvailableENICnt,
		vpcrcAvailableENICnt,
		leakedENICnt,
	)
}
