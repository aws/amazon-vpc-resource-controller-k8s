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
	"context"
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"

	bigcache "github.com/allegro/bigcache/v3"
)

const (
	MaxEventConcurrentReconciles  = 4
	EventCreatedPastMinutes       = 2
	EventFilterKey                = "reason"
	LoggerName                    = "event"
	TotalEventsCountMetrics       = "reconcile"
	EventFailureCountMetrics      = "failing_list"
	SGPEventsFailureCountMetrics  = "trunk_label_failed"
	SGPEventsCountMetrics         = "trunk_labelled"
	CNEventsFailureCountMetrics   = "eniconfig_label_failed"
	CNEventsCountMetrics          = "eniconfig_labelled"
	ReconcileEventsLatencyMetrics = "reconcile_events"
	LabelOperation                = "operation"
	LabelTime                     = "time"
	LabelCacheHit                 = "cache_hit"
	LabelCacheMiss                = "cache_miss"
	LabelCacheAdd                 = "cache_add"
	LabelCacheSetErr              = "cache_set_error"
	CacheHitMetricsValue          = "NODE_CACHED"
	CacheMissMetricsValue         = "NODE_NOT_CACHED"
	CacheAddMetricsValue          = "NODE_ADDED"
	CacheErrMetricsValue          = "NODE_CACHE_ERROR"
	EventRegardingKind            = "Node"
)

// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;update;patch;list;watch

type EventReconciler struct {
	Log    logr.Logger
	Scheme *runtime.Scheme
	K8sAPI k8s.K8sWrapper
	cache  *bigcache.BigCache
}

type Feature int

const (
	EnableSGP Feature = iota
	EnableCN
)

var (
	eventControllerTotalEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "total_events_reconciled_by_event_controller",
			Help: "The number of events that were reconcile by the controller",
		},
		[]string{LabelOperation},
	)

	eventControllerSGPEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "security_group_pod_events_reconciled_by_event_controller",
			Help: "The number of SGP events that were reconcile by the controller",
		},
		[]string{LabelOperation},
	)

	eventControllerSGPEventsFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_security_group_pod_events_reconciled_by_event_controller",
			Help: "The number of SGP events that were reconcile by the controller but failed on label node",
		},
		[]string{LabelOperation},
	)

	eventControllerCNEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "custom_networking_events_reconciled_by_event_controller",
			Help: "The number of custom networking events that were reconcile by the controller",
		},
		[]string{LabelOperation},
	)

	eventControllerCNEventsFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_custom_networking_events_reconciled_by_event_controller",
			Help: "The number of custom networking events that were reconcile by the controller but failed on label node",
		},
		[]string{LabelOperation},
	)

	eventControllerEventFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_get_events__by_event_controller",
			Help: "The number of failures on getting events by the controller",
		},
		[]string{LabelOperation},
	)

	eventReconcileOperationLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "event_reconcile_operation_latency",
			Help: "Event controller reconcile events latency in ms",
		},
		[]string{LabelTime},
	)

	eventControllerEventNodeCacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "event_reconcile_node_cache_size",
			Help: "Event controller node cache size",
		},
	)

	eventControllerEventNodeCacheOpsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "event_reconcile_node_cache_operations",
			Help: "The number of ops on retrieving node from cache",
		},
		[]string{LabelCacheHit, LabelCacheMiss, LabelCacheAdd, LabelCacheSetErr},
	)

	prometheusRegistered = false
)

func NewEventReconciler(log logr.Logger, scheme *runtime.Scheme, k8sAPI k8s.K8sWrapper, nodeCache *bigcache.BigCache) *EventReconciler {
	return &EventReconciler{
		Log:    log,
		Scheme: scheme,
		K8sAPI: k8sAPI,
		cache:  nodeCache,
	}
}

func PrometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(eventControllerTotalEventsCount)
		metrics.Registry.MustRegister(eventControllerSGPEventsCount)
		metrics.Registry.MustRegister(eventControllerEventFailureCount)
		metrics.Registry.MustRegister(eventControllerCNEventsCount)
		metrics.Registry.MustRegister(eventReconcileOperationLatency)
		metrics.Registry.MustRegister(eventControllerSGPEventsFailureCount)
		metrics.Registry.MustRegister(eventControllerCNEventsFailureCount)
		metrics.Registry.MustRegister(eventControllerEventNodeCacheSize)
		metrics.Registry.MustRegister(eventControllerEventNodeCacheOpsCount)
		prometheusRegistered = true
	}
}

func (r *EventReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	var err error

	logger := r.Log.WithValues(LoggerName, req.Name)

	eventControllerTotalEventsCount.WithLabelValues(TotalEventsCountMetrics).Inc()

	ops := []client.ListOption{
		client.MatchingFields{
			EventFilterKey: config.VpcCNINodeEventReason,
		},
	}

	events, err := r.K8sAPI.ListEvents(ops)
	if err != nil {
		logger.Error(err, "List events failed")
		eventControllerEventFailureCount.WithLabelValues(EventFailureCountMetrics).Inc()
		return ctrl.Result{}, err
	}

	for _, event := range events.Items {
		// only check and process events were created in the past EventCreatedPastMinutes minutes
		if !event.GetCreationTimestamp().After(time.Now().Add(-time.Minute*EventCreatedPastMinutes)) || event.Regarding.Kind != EventRegardingKind {
			continue
		}
		eventForSGP := r.isValidEventForSGP(event)
		eventForCN := r.isValidEventForCustomNetworking(event)
		if eventForSGP || eventForCN {
			nodeName := event.Regarding.Name
			// use instance ID as cache key since they are guaranteed regionally unique
			hostID := string(event.Regarding.UID)

			// index 0 is the flag for SecurityGroupForPod, index 1 is the flag for Custom Networking
			cacheValue := []byte{0, 0}

			// use cache to avoid unnecessary calls to API server/ClientCache and node controller cache
			// due to using List, the redundant call number can be large
			if hitValue, cacheErr := r.cache.Get(hostID); cacheErr == nil {
				if (eventForSGP && hitValue[EnableSGP] == 1) || (eventForCN && hitValue[EnableCN] == 1) {
					eventControllerEventNodeCacheOpsCount.WithLabelValues([]string{CacheHitMetricsValue, "", "", ""}...).Inc()
					logger.V(1).Info("Node has been processed and found in cache, will skip this event", "NodeName", nodeName, "InstanceID", hostID)
					continue
				}
				// if found the instance but the feature value is not updated, we need keep the other updated feature value
				if eventForSGP {
					cacheValue[EnableSGP] = 1
					cacheValue[EnableCN] = hitValue[EnableCN]
				} else {
					cacheValue[EnableCN] = 1
					cacheValue[EnableSGP] = hitValue[EnableSGP]
				}
				logger.V(1).Info("Cache hit", "InstanceID", hostID, "SGPFeature", eventForSGP, "Value_SGP", hitValue[EnableSGP], "CNFeature", eventForCN, "Value_CN", hitValue[EnableCN])
				logger.V(1).Info("Cache hit -- Will update Cache as", "InstanceID", hostID, "SGPFeature", eventForSGP, "Value_SGP", cacheValue[EnableSGP], "CNFeature", eventForCN, "Value_CN", cacheValue[EnableCN])
			} else {
				// no hit, init regarding feature's value to 1, and the other feature will be 0
				if eventForSGP {
					cacheValue[EnableSGP] = 1
				} else {
					cacheValue[EnableCN] = 1
				}
				logger.V(1).Info("Cache miss -- Will update Cache as", "InstanceID", hostID, "SGPFeature", eventForSGP, "Value_SGP", cacheValue[EnableSGP], "CNFeature", eventForCN, "Value_CN", cacheValue[EnableCN])
			}
			// we use the GetNode() as a safeguard to avoid repeating reconciling on deleted nodes
			if node, err := r.K8sAPI.GetNode(nodeName); err != nil {
				// if not found the node, we don't requeue and just wait VPC CNI to send another request
				r.Log.V(1).Info("Event reconciler didn't find the node and wait for next request for the node", "Node", node.Name)
				return ctrl.Result{}, nil
			} else {
				eventControllerEventNodeCacheOpsCount.WithLabelValues([]string{"", CacheMissMetricsValue, "", ""}...).Inc()
				eventControllerEventNodeCacheSize.Set(float64(r.cache.Len()))
				if eventForSGP {
					// make the label value to false that indicates the node is ok to be proceeded by providers
					// provider will decide if the node can be attached with trunk interface
					labelled, err := r.K8sAPI.AddLabelToManageNode(node, config.HasTrunkAttachedLabel, config.BooleanFalse)
					if err != nil {
						// by returning an error, we request that our controller will get Reconcile() called again
						// TODO: we can consider let VPC CNI re-send event instead of requeueing the request
						eventControllerSGPEventsFailureCount.WithLabelValues(SGPEventsFailureCountMetrics).Inc()
						return ctrl.Result{}, err
					}
					if labelled {
						eventControllerSGPEventsCount.WithLabelValues(SGPEventsCountMetrics).Inc()
						r.Log.Info("Label node with trunk label as false", "Node", node.Name, "InstanceID", hostID, "Label", config.HasTrunkAttachedLabel)
					}
					// We don't process labelled == false because when labelled == false but err == nil, meaning the key value pair is present in labels
					// but the cache missed the instance for some reason, we want to go ahead to add the instance into cache
				}

				if eventForCN {
					configKey, configName := parseEventMsg(event.Note)
					if configKey == config.CustomNetworkingLabel {
						labelled, err := r.K8sAPI.AddLabelToManageNode(node, config.CustomNetworkingLabel, configName)
						if err != nil {
							// by returning an error, we request Reconcile() being called again
							// TODO: we can consider let VPC CNI re-send event instead of requeueing the request
							eventControllerCNEventsFailureCount.WithLabelValues(CNEventsFailureCountMetrics).Inc()
							return ctrl.Result{}, err
						}
						if labelled {
							eventControllerCNEventsCount.WithLabelValues(CNEventsCountMetrics).Inc()
							r.Log.V(1).Info("Label node with eniconfig label with configured name", "Node", node.Name, "Label", config.CustomNetworkingLabel)
						}
						// We don't process labelled == false because when labelled == false but err == nil, meaning the key value pair is present in labels
						// but the cache missed the instance for some reason, we want to go ahead to add the instance into cache
					}
				}
				// Only update cache after necessary labelling succeeds
				if cacheErr := r.cache.Set(hostID, cacheValue); cacheErr != nil {
					eventControllerEventNodeCacheOpsCount.WithLabelValues([]string{"", "", "", CacheErrMetricsValue}...).Inc()
					logger.Error(err, "Adding new node name to event controller cache failed")
				} else {
					eventControllerEventNodeCacheOpsCount.WithLabelValues([]string{"", "", CacheAddMetricsValue, ""}...).Inc()
					logger.Info("Added a new node into event cache", "NodeName", nodeName, "InstanceID", hostID, "CacheSize", r.cache.Len())
				}
			}
		}
	}
	eventReconcileOperationLatency.WithLabelValues(ReconcileEventsLatencyMetrics).Observe(float64(time.Since(start).Milliseconds()))
	return ctrl.Result{}, nil
}

func (r *EventReconciler) isValidEventForSGP(event eventsv1.Event) bool {
	return event.ReportingController == config.VpcCNIReportingAgent &&
		event.Reason == config.VpcCNINodeEventReason && event.Action == config.VpcCNINodeEventActionForTrunk
}

func (r *EventReconciler) isValidEventForCustomNetworking(event eventsv1.Event) bool {
	return event.ReportingController == config.VpcCNIReportingAgent &&
		event.Reason == config.VpcCNINodeEventReason && event.Action == config.VpcCNINodeEventActionForEniConfig
}

func (r *EventReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &eventsv1.Event{}, EventFilterKey, func(raw client.Object) []string {
		event := raw.(*eventsv1.Event)
		return []string{event.Reason}
	}); err != nil {
		return err
	}

	r.Log.Info("Event manager is using settings", "ConcurrentReconciles", MaxEventConcurrentReconciles, "MinutesInPast", EventCreatedPastMinutes)
	PrometheusRegister()
	return ctrl.NewControllerManagedBy(mgr).
		For(&eventsv1.Event{}).Owns(&corev1.Node{}).
		WithEventFilter(predicate.Funcs{
			DeleteFunc: func(de event.DeleteEvent) bool { return false },
			UpdateFunc: func(ue event.UpdateEvent) bool {
				return ue.ObjectOld.GetGeneration() != ue.ObjectNew.GetGeneration()
			},
			GenericFunc: func(ge event.GenericEvent) bool { return false },
			CreateFunc: func(ce event.CreateEvent) bool {
				now := time.Now()
				then := now.Add(-time.Minute * EventCreatedPastMinutes)
				return ce.Object.GetCreationTimestamp().After(then)
			},
		}).
		WithOptions(controller.Options{MaxConcurrentReconciles: MaxEventConcurrentReconciles}).Complete(r)
}

func parseEventMsg(msg string) (string, string) {
	splittedMsg := strings.Split(msg, "=")
	if len(splittedMsg) != 2 {
		return "", ""
	}
	key := splittedMsg[0]
	value := splittedMsg[1]
	return key, value
}
