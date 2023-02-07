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

package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	EventFilterKey               = "reason"
	LoggerName                   = "event"
	TotalValidEventsCount        = "total_valid_event"
	EventFailureCountMetrics     = "failing_watch"
	EventGetCallCountMetrics     = "call_get_node"
	SGPEventsFailureCountMetrics = "trunk_label_failed"
	SGPEventsCountMetrics        = "trunk_labelled"
	CNEventsFailureCountMetrics  = "eniconfig_label_failed"
	CNEventsCountMetrics         = "eniconfig_labelled"
	WatchEventsLatencyMetrics    = "watch_events"
	LabelOperation               = "operation"
	LabelTime                    = "time"
	LabelCacheHit                = "cache_hit"
	LabelCacheMiss               = "cache_miss"
	LabelCacheAdd                = "cache_add"
	LabelCacheSetErr             = "cache_set_error"
	CacheHitMetricsValue         = "NODE_CACHED"
	CacheMissMetricsValue        = "NODE_NOT_CACHED"
	CacheAddMetricsValue         = "NODE_ADDED"
	CacheErrMetricsValue         = "NODE_CACHE_ERROR"
	WatcherPanicCount            = "event_watcher_panic_to_restart"
	TotalEventWatchedCount       = "event_watched_total"
)

var (
	eventControllerTotalValidEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "total_events_watched_by_event_controller",
			Help: "The number of events that were watched by the controller",
		},
		[]string{LabelOperation},
	)

	eventControllerCallGETToAPIServerCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "total_count_of_call_api_server_for_get_node",
			Help: "The number of get calls to api server for node",
		},
		[]string{LabelOperation},
	)

	eventControllerSGPEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "security_group_pod_events_watched_by_event_controller",
			Help: "The number of SGP events that were watched by the controller",
		},
		[]string{LabelOperation},
	)

	eventControllerSGPEventsFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_security_group_pod_events_watched_by_event_controller",
			Help: "The number of SGP events that were watched by the controller but failed on label node",
		},
		[]string{LabelOperation},
	)

	eventControllerCNEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "custom_networking_events_watched_by_event_controller",
			Help: "The number of custom networking events that were watched by the controller",
		},
		[]string{LabelOperation},
	)

	eventControllerCNEventsFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_custom_networking_events_watched_by_event_controller",
			Help: "The number of custom networking events that were watched by the controller but failed on label node",
		},
		[]string{LabelOperation},
	)

	eventControllerEventWatcherFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_watch_events_by_event_watcher",
			Help: "The number of failures on getting events by the controller",
		},
		[]string{LabelOperation},
	)

	eventWatchOperationLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "event_watch_operation_latency",
			Help: "Event controller watch events latency in second",
		},
		[]string{LabelTime},
	)

	eventControllerNodeCacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "event_watch_node_cache_size",
			Help: "Event controller node cache size",
		},
	)

	eventControllerNodeCacheOpsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "event_watch_node_cache_operations",
			Help: "The number of ops on retrieving node from cache",
		},
		[]string{LabelCacheHit, LabelCacheMiss, LabelCacheAdd, LabelCacheSetErr},
	)

	eventWatcherPanicCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "event_watcher_panic_to_restart_count",
			Help: "The number of event watcher has to panic to restart",
		},
		[]string{LabelOperation},
	)

	eventWatchedTotalCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "event_watched_total_count",
			Help: "The number of events watcher has watched",
		},
		[]string{LabelOperation},
	)
)
