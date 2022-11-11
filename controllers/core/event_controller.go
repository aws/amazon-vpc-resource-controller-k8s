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
)

const (
	MaxEventConcurrentReconciles = 4
	EventCreatedPastMinutes      = 2
	EventFilterKey               = "reportingComponent"
)

// +kubebuilder:rbac:groups="",resources=events,verbs=create;update;patch;list;watch

type EventReconciler struct {
	Log    logr.Logger
	Scheme *runtime.Scheme
	K8sAPI k8s.K8sWrapper
}

var (
	eventControllerTotalEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "total_events_reconciled_by_event_controller",
			Help: "The number of events that were reconcile by the controller",
		},
		[]string{"operation"},
	)

	eventControllerSGPEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "security_group_pod_events_reconciled_by_event_controller",
			Help: "The number of SGP events that were reconcile by the controller",
		},
		[]string{"operation"},
	)

	eventControllerCNEventsCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "custom_networking_events_reconciled_by_event_controller",
			Help: "The number of custom networking events that were reconcile by the controller",
		},
		[]string{"operation"},
	)

	eventControllerEventFailureCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "failed_get_events__by_event_controller",
			Help: "The number of failures on getting events by the controller",
		},
		[]string{"operation"},
	)

	eventReconcileOperationLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "event_reconcile_operation_latency",
			Help: "Event controller reconcile events latency in ms",
		},
		[]string{"time"},
	)

	prometheusRegistered = false
	reconcileEvents      = "reconcile_events"
)

func PrometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(eventControllerTotalEventsCount)
		metrics.Registry.MustRegister(eventControllerSGPEventsCount)
		metrics.Registry.MustRegister(eventControllerEventFailureCount)
		metrics.Registry.MustRegister(eventControllerCNEventsCount)
		metrics.Registry.MustRegister(eventReconcileOperationLatency)
		prometheusRegistered = true
	}
}

func (r *EventReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	var err error

	logger := r.Log.WithValues("event", req.Name)

	eventControllerTotalEventsCount.WithLabelValues("reconcile").Inc()

	ops := []client.ListOption{
		client.MatchingFields{
			EventFilterKey: config.VpcCNIReportingAgent,
		},
	}

	events, err := r.K8sAPI.ListEvents(ops)
	if err != nil {
		logger.Error(err, "List events failed")
		eventControllerEventFailureCount.WithLabelValues("failing_list").Inc()
		return ctrl.Result{}, err
	}

	for _, event := range events.Items {
		// only check and process events were created in the past EventCreatedPastMinutes minutes
		if !event.GetCreationTimestamp().After(time.Now().Add(-time.Minute * EventCreatedPastMinutes)) {
			continue
		}
		if r.isValidEventForSGP(event) || r.isValidEventForCustomNetworking(event) {
			nodeName := string(event.InvolvedObject.UID)

			if node, err := r.K8sAPI.GetNode(nodeName); err != nil {
				// if not found the node, we don't requeue and just wait VPC CNI to send another request
				r.Log.V(1).Info("Event reconciler didn't find the node and wait for next request for the node", "Node", node.Name)
				return ctrl.Result{}, nil
			} else {
				if r.isEventToManageNode(event) && r.isValidEventForSGP(event) {
					r.K8sAPI.AddLabelToManageNode(node, config.HasTrunkAttachedLabel, "true")
					eventControllerSGPEventsCount.WithLabelValues("trunk_labelled").Inc()
					r.Log.Info("Lable node with trunk label as true", "Node", node.Name, "Label", config.HasTrunkAttachedLabel)
				}
				if r.isValidEventForCustomNetworking(event) {
					configKey, configName := parseEventMsg(event.Message)
					if configKey == config.CustomNetworkingLabel {
						r.K8sAPI.AddLabelToManageNode(node, config.CustomNetworkingLabel, configName)
						eventControllerCNEventsCount.WithLabelValues("eniconfig_labelled").Inc()
						r.Log.Info("Label node with eniconfig label with configured name", "Node", node.Name, "Label", config.CustomNetworkingLabel)
					}
				}
			}
		}
	}
	eventReconcileOperationLatency.WithLabelValues(reconcileEvents).Observe(float64(time.Since(start).Milliseconds()))
	return ctrl.Result{}, nil
}

func (r *EventReconciler) isValidEventForSGP(event corev1.Event) bool {
	return event.ReportingController == config.VpcCNIReportingAgent &&
		event.Reason == config.VpcCNINodeEventReason && event.Action == config.VpcCNINodeEventActionForTrunk
}

func (r *EventReconciler) isValidEventForCustomNetworking(event corev1.Event) bool {
	return event.ReportingController == config.VpcCNIReportingAgent &&
		event.Reason == config.VpcCNINodeEventReason && event.Action == config.VpcCNINodeEventActionForEniConfig
}

func (r *EventReconciler) isEventToManageNode(event corev1.Event) bool {
	return event.Message == config.TrunkNotAttached || event.Message == config.TrunkAttached
}

func (r *EventReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Event{}, EventFilterKey, func(raw client.Object) []string {
		event := raw.(*corev1.Event)
		return []string{event.ReportingController}
	}); err != nil {
		return err
	}

	r.Log.Info("Event manager is using settings", "ConcurrentReconciles", MaxEventConcurrentReconciles, "MinutesInPast", EventCreatedPastMinutes)
	PrometheusRegister()
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Event{}).
		WithEventFilter(predicate.Funcs{
			DeleteFunc:  func(de event.DeleteEvent) bool { return false },
			UpdateFunc:  func(ue event.UpdateEvent) bool { return false },
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
