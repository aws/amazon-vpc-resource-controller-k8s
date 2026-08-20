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

package ec2

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Collision detection sources for cninode_instance_id_collision_total: CollisionSourceBind is
// recorded every time the CNINode controller reconciles a node's CNINode (steady-state
// signal); CollisionSourceHydrate is recorded on the restart/leader-change hydrate fast path
// (HydrateFromCNINodeStatus). Both feed the same counter so the two signals can be told apart
// via the source label while still summing to a single "how often does this happen" number
// (design-cn.md §2.7, §4.2 E7; issue #515).
const (
	CollisionSourceBind    = "bind"
	CollisionSourceHydrate = "hydrate"
)

// InstanceIDCollisionCount is exported so both detection sites (the CNINode controller's bind
// check and the hydrate fast path in this package) increment the same counter, and so tests
// outside this package can assert on it directly.
var (
	InstanceIDCollisionCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cninode_instance_id_collision_total",
			Help: "The number of times a CNINode's recorded instance ID did not match the instance ID it was checked against, indicating a leaked CNINode name reused by a different EC2 instance",
		},
		[]string{"source"},
	)

	prometheusRegisterOnce sync.Once
)

// RecordInstanceIDCollision increments the CNINode instance-ID collision counter for the given
// detection source. Registers the metric lazily on first use so callers never need to know
// about controller startup ordering.
func RecordInstanceIDCollision(source string) {
	prometheusRegister()
	InstanceIDCollisionCount.WithLabelValues(source).Inc()
}

func prometheusRegister() {
	prometheusRegisterOnce.Do(func() {
		metrics.Registry.MustRegister(InstanceIDCollisionCount)
	})
}
