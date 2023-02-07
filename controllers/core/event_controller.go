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

	"github.com/allegro/bigcache/v3"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	eventsv1 "k8s.io/api/events/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	CniPodNamePrefix       = "aws-node"
	InitialWatchingEventRV = "1"

	MaxGoroutinesProcessEvents = 4
	RetryWatcherTimeoutMinutes = 5
)

type Feature int

const (
	EnableSGP Feature = iota
	EnableCN
)

type WatchedEventController struct {
	clientSet            kubernetes.Clientset
	logger               logr.Logger
	ctx                  context.Context
	k8sAPI               k8s.K8sWrapper
	cache                *bigcache.BigCache
	prometheusRegistered bool
	eventWatchTime       int
}

// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=get;watch

func NewWatchedEventController(ctx context.Context,
	log logr.Logger,
	client kubernetes.Clientset,
	k8sAPI k8s.K8sWrapper,
	nodeCache *bigcache.BigCache,
	eventWatchTime int) *WatchedEventController {
	return &WatchedEventController{
		clientSet:      client,
		logger:         log,
		ctx:            ctx,
		k8sAPI:         k8sAPI,
		cache:          nodeCache,
		eventWatchTime: eventWatchTime,
	}
}

func (wec *WatchedEventController) watchEvents() error {
	// client-go default timeout on watcher is 5-10 minutes, we use 5 minutes here
	timeOut := int64(RetryWatcherTimeoutMinutes * 60)
	watchFunc := func(opts v1.ListOptions) (watch.Interface, error) {
		return wec.clientSet.EventsV1().
			Events(config.KubeSystemNamespace).
			Watch(wec.ctx, v1.ListOptions{
				TimeoutSeconds: &timeOut,
				FieldSelector: fields.Set{
					"reason": config.VpcCNINodeEventReason,
				}.AsSelector().String(),
			})
	}

	watcher, err := wec.k8sAPI.GetRetryWatcher(InitialWatchingEventRV, watchFunc)

	if err != nil {
		wec.logger.Error(err, "Setup self watched event controller watcher failed")
		return err
	}

	// TODO: since this may cause panic on closing closed channel, we can evaluate this later again
	// since we can't let the watcher close itself, panic is not a bad thing until we have health check in place.
	defer watcher.Stop()

	for {
		select {
		case <-wec.ctx.Done():
			wec.logger.Info("Received done signal from context, shutting down the self managed event controller")
			return nil
		case <-watcher.Done():
			// since the watcher is a critical component to support SGP feature
			// we need to panic and restart the entire controller if retryWatcher stops for any reason
			// TODO: use health check to gracefully restart the entire controller
			wec.logger.Info("event watcher Done chan was closed, need panic to restart...")
			eventWatcherPanicCount.WithLabelValues(WatcherPanicCount).Inc()
			panic("event self watcher can't be closed, panic to restart")
		case event, open := <-watcher.ResultChan():
			if !open {
				// need safe guard the watcher
				wec.logger.Info("event watcher ResultChan was closed, need panic to restart...")
				eventWatcherPanicCount.WithLabelValues(WatcherPanicCount).Inc()
				// TODO: use health check to gracefully restart the entire controller
				panic("event self watcher can't be closed, panic to restart")
			}
			eventWatchedTotalCount.WithLabelValues(TotalEventWatchedCount).Inc()
			switch event.Type {
			// for this use case, no need to check on DELETE and BOOKMARK types
			case watch.Added, watch.Modified:
				wantedEvent, ok := event.Object.(*eventsv1.Event)
				if ok {
					wec.logger.V(1).Info("Get an event from watcher and add into processing channel", "EventName", wantedEvent.Name, "EventNamespace", wantedEvent.Namespace, "Event", wantedEvent)
					if wantedEvent.CreationTimestamp.After(time.Now().Add(-time.Duration(wec.eventWatchTime) * time.Minute)) {
						eventControllerTotalValidEventsCount.WithLabelValues(TotalValidEventsCount).Inc()
						// using short life goroutines to do the one-off processing
						// prefer short life goroutines than long life fixed number of goroutines for this work
						go wec.processEvent(*wantedEvent)
					}
				}
			case watch.Error:
				// for our use case, we ignore errors. if watcher itself decides to quit, we need restart the entire controller
				err := event.Object.(error)
				eventControllerEventWatcherFailureCount.WithLabelValues(EventFailureCountMetrics).Inc()
				wec.logger.Error(err, "watch error when receiving events from watcher channel")
			}
		}
	}
}

func (wec *WatchedEventController) processEvent(event eventsv1.Event) error {
	start := time.Now()
	defer eventWatchOperationLatency.WithLabelValues(WatchEventsLatencyMetrics).Observe(float64(time.Since(start).Seconds()))

	eventForSGP := isValidEventForSGP(event)
	eventForCN := isValidEventForCustomNetworking(event)
	if eventForSGP || eventForCN {
		nodeName := event.Related.Name
		// use instance ID as cache key since they are guaranteed regionally unique
		hostID := string(event.Related.UID)

		// index 0 is the flag for SecurityGroupForPod, index 1 is the flag for Custom Networking
		cacheValue := []byte{0, 0}

		// use cache to avoid unnecessary calls to API server/ClientCache and node controller cache
		if hitValue, cacheErr := wec.cache.Get(hostID); cacheErr == nil {
			if (eventForSGP && hitValue[EnableSGP] == 1) || (eventForCN && hitValue[EnableCN] == 1) {
				eventControllerNodeCacheOpsCount.WithLabelValues([]string{CacheHitMetricsValue, "", "", ""}...).Inc()
				wec.logger.V(1).Info("Node has been processed and found in cache, will skip this event", "NodeName", nodeName, "InstanceID", hostID)
				return nil
			}
			// if found the instance but the feature value is not updated, we need keep the other updated feature value
			if eventForSGP {
				cacheValue[EnableSGP] = 1
				cacheValue[EnableCN] = hitValue[EnableCN]
			} else {
				cacheValue[EnableCN] = 1
				cacheValue[EnableSGP] = hitValue[EnableSGP]
			}
			wec.logger.V(1).Info("Cache hit", "InstanceID", hostID, "SGPFeature", eventForSGP, "Value_SGP", hitValue[EnableSGP], "CNFeature", eventForCN, "Value_CN", hitValue[EnableCN])
			wec.logger.V(1).Info("Cache hit -- Will update Cache as", "InstanceID", hostID, "SGPFeature", eventForSGP, "Value_SGP", cacheValue[EnableSGP], "CNFeature", eventForCN, "Value_CN", cacheValue[EnableCN])
		} else {
			// no hit, init regarding feature's value to 1, and the other feature will be 0
			if eventForSGP {
				cacheValue[EnableSGP] = 1
			} else {
				cacheValue[EnableCN] = 1
			}
			wec.logger.V(1).Info("Cache miss -- Will update Cache as", "InstanceID", hostID, "SGPFeature", eventForSGP, "Value_SGP", cacheValue[EnableSGP], "CNFeature", eventForCN, "Value_CN", cacheValue[EnableCN])
		}
		// we use the GetNode() as a safeguard to avoid repeating reconciling on deleted nodes
		eventControllerCallGETToAPIServerCount.WithLabelValues(EventGetCallCountMetrics).Inc()
		if node, err := wec.k8sAPI.GetNode(nodeName); err != nil {
			// if not found the node, we don't requeue and just wait VPC CNI to send another request
			wec.logger.Info("Event reconciler didn't find the node and wait for next request for the node", "Node", node.Name)
			return nil
		} else {
			eventControllerNodeCacheOpsCount.WithLabelValues([]string{"", CacheMissMetricsValue, "", ""}...).Inc()
			eventControllerNodeCacheSize.Set(float64(wec.cache.Len()))

			// since we support aggresive events, multiple goroutines could race to the label.
			// don't try to label again if label key exists since here should be the first place to add label.
			_, sgpLabelled := node.Labels[config.HasTrunkAttachedLabel]
			if eventForSGP && !sgpLabelled {
				// make the label value to false that indicates the node is ok to be proceeded by providers
				// provider will decide if the node can be attached with trunk interface
				labelled, err := wec.k8sAPI.AddLabelToManageNode(node, config.HasTrunkAttachedLabel, config.BooleanFalse)
				if err != nil {
					// by returning an error, we request that our controller will get Reconcile() called again
					// TODO: we can consider let VPC CNI re-send event instead of requeueing the request
					eventControllerSGPEventsFailureCount.WithLabelValues(SGPEventsFailureCountMetrics).Inc()
					return err
				}
				if labelled {
					eventControllerSGPEventsCount.WithLabelValues(SGPEventsCountMetrics).Inc()
					wec.logger.Info("Label node with trunk label as false", "Node", node.Name, "InstanceID", hostID, "Label", config.HasTrunkAttachedLabel)
				}
				// We don't process labelled == false because when labelled == false but err == nil, meaning the key value pair is present in labels
				// but the cache missed the instance for some reason, we want to go ahead to add the instance into cache
			}

			// since we support aggresive events, multiple goroutines could race to the label.
			// don't try to label again if label key exists since here should be the first place to add label.
			_, cnLabelled := node.Labels[config.CustomNetworkingLabel]
			if eventForCN && !cnLabelled {
				configKey, configName := parseEventMsg(event.Note)
				if configKey == config.CustomNetworkingLabel {
					labelled, err := wec.k8sAPI.AddLabelToManageNode(node, config.CustomNetworkingLabel, configName)
					if err != nil {
						// by returning an error, we request Reconcile() being called again
						// TODO: we can consider let VPC CNI re-send event instead of requeueing the request
						eventControllerCNEventsFailureCount.WithLabelValues(CNEventsFailureCountMetrics).Inc()
						return err
					}
					if labelled {
						eventControllerCNEventsCount.WithLabelValues(CNEventsCountMetrics).Inc()
						wec.logger.V(1).Info("Label node with eniconfig label with configured name", "Node", node.Name, "Label", config.CustomNetworkingLabel)
					}
					// We don't process labelled == false because when labelled == false but err == nil, meaning the key value pair is present in labels
					// but the cache missed the instance for some reason, we want to go ahead to add the instance into cache
				}
			}
			// Only update cache after necessary labelling succeeds
			if cacheErr := wec.cache.Set(hostID, cacheValue); cacheErr != nil {
				eventControllerNodeCacheOpsCount.WithLabelValues([]string{"", "", "", CacheErrMetricsValue}...).Inc()
				wec.logger.Error(err, "Adding new node name to event controller cache failed")
			} else {
				eventControllerNodeCacheOpsCount.WithLabelValues([]string{"", "", CacheAddMetricsValue, ""}...).Inc()
				wec.logger.Info("Added a new node into event cache", "NodeName", nodeName, "InstanceID", hostID, "CacheSize", wec.cache.Len())
			}
		}
	}

	return nil
}

func isValidEventForSGP(event eventsv1.Event) bool {
	return event.ReportingController == config.VpcCNIReportingAgent &&
		event.Reason == config.VpcCNINodeEventReason && strings.HasPrefix(event.Action, config.VpcCNINodeEventActionForTrunk)
}

func isValidEventForCustomNetworking(event eventsv1.Event) bool {
	return event.ReportingController == config.VpcCNIReportingAgent &&
		event.Reason == config.VpcCNINodeEventReason && strings.HasPrefix(event.Action, config.VpcCNINodeEventActionForEniConfig)
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

func (wec *WatchedEventController) Start(mgr manager.Manager) error {
	var err error
	wec.prometheusRegister()
	go func() {
		<-mgr.Elected()

		if err = wec.watchEvents(); err != nil {
			wec.logger.Error(err, "couldn't start self managed event controller with watch")
		}
	}()

	// wait for 1 second in this goroutine to ensure the watcher being created without error
	time.Sleep(1 * time.Second)
	return err
}

func (wec *WatchedEventController) prometheusRegister() {
	if !wec.prometheusRegistered {
		metrics.Registry.MustRegister(eventControllerTotalValidEventsCount)
		metrics.Registry.MustRegister(eventControllerSGPEventsCount)
		metrics.Registry.MustRegister(eventControllerEventWatcherFailureCount)
		metrics.Registry.MustRegister(eventControllerCNEventsCount)
		metrics.Registry.MustRegister(eventWatchOperationLatency)
		metrics.Registry.MustRegister(eventControllerSGPEventsFailureCount)
		metrics.Registry.MustRegister(eventControllerCNEventsFailureCount)
		metrics.Registry.MustRegister(eventControllerNodeCacheSize)
		metrics.Registry.MustRegister(eventControllerNodeCacheOpsCount)
		metrics.Registry.MustRegister(eventControllerCallGETToAPIServerCount)
		metrics.Registry.MustRegister(eventWatcherPanicCount)
		metrics.Registry.MustRegister(eventWatchedTotalCount)
		wec.prometheusRegistered = true
		wec.logger.Info("Register metrics for event controller")
	}
}
