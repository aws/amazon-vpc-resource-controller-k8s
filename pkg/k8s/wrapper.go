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

package k8s

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/controllers/custom"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/prometheus/client_golang/prometheus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	NodeNameSpec = "nodeName"
)

var (
	prometheusRegistered = false

	annotatePodRequestCallCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "annotate_pod_request_call_count",
			Help: "The number of request to annotate pod object",
		},
		[]string{"annotate_key"},
	)

	annotatePodRequestErrCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "annotate_pod_request_err_count",
			Help: "The number of request that failed to annotate the pod",
		},
		[]string{"annotate_key"},
	)

	advertiseResourceRequestCallCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "advertise_resource_request_call_count",
			Help: "The number of request to advertise extended resource",
		},
		[]string{"resource_name"},
	)

	advertiseResourceRequestErrCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "advertise_resource_request_err_count",
			Help: "The number of request that failed to advertise extended resource",
		},
		[]string{"resource_name"},
	)

	getPodFromAPIServeCallCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "get_pod_from_api_server_call_count",
			Help: "The number of requests to get the pod directly from API Server",
		},
	)

	getPodFromAPIServeErrCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "get_pod_from_api_server_err_count",
			Help: "The number of requests that failed to get the pod directly from API Server",
		},
	)

	broadcastPodEventCallCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "broadcast_pod_event_call_count",
			Help: "The number of request made to broadcast pod event",
		},
	)

	broadcastPodEventErrCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "broadcast_pod_event_err_count",
			Help: "The number of errors encountered while broadcasting pod event",
		},
	)
)

func prometheusRegister() {
	metrics.Registry.MustRegister(
		annotatePodRequestCallCount,
		annotatePodRequestErrCount,
		advertiseResourceRequestErrCount,
		advertiseResourceRequestCallCount,
		getPodFromAPIServeCallCount,
		getPodFromAPIServeErrCount)

	prometheusRegistered = true
}

// K8sWrapper represents an interface with all the common operations on K8s objects
type K8sWrapper interface {
	GetPod(namespace string, name string) (*v1.Pod, error)
	ListPods(nodeName string) (*v1.PodList, error)
	GetPodFromAPIServer(namespace string, name string) (*v1.Pod, error)
	AnnotatePod(podNamespace string, podName string, key string, val string) error
	AdvertiseCapacityIfNotSet(nodeName string, resourceName string, capacity int) error
	GetENIConfig(eniConfigName string) (*v1alpha1.ENIConfig, error)
	BroadcastPodEvent(pod *v1.Pod, reason string, message string, eventType string)
}

// k8sWrapper is the wrapper object with the client
type k8sWrapper struct {
	// podDataStore to do get/list operation on pod objects
	podController custom.Controller
	// cacheClient for all get/list operation on non pod object types
	cacheClient   client.Client
	coreV1        corev1.CoreV1Interface
	eventRecorder record.EventRecorder
}

// NewK8sWrapper returns a new K8sWrapper
func NewK8sWrapper(client client.Client, coreV1 corev1.CoreV1Interface,
	podController custom.Controller) K8sWrapper {
	if !prometheusRegistered {
		prometheusRegister()
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: coreV1.Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{
		Component: config.ControllerName,
	})
	return &k8sWrapper{cacheClient: client, coreV1: coreV1, eventRecorder: recorder,
		podController: podController}
}

func (k *k8sWrapper) GetENIConfig(eniConfigName string) (*v1alpha1.ENIConfig, error) {
	sgp := &v1alpha1.ENIConfig{}
	err := k.cacheClient.Get(context.Background(), types.NamespacedName{
		Name: eniConfigName,
	}, sgp)

	return sgp, err
}

// GetPodFromAPIServer returns the pod details by querying the API Server directly
func (k *k8sWrapper) GetPodFromAPIServer(namespace string, name string) (*v1.Pod, error) {
	getPodFromAPIServeCallCount.Inc()
	pod, err := k.coreV1.Pods(namespace).Get(name, metav1.GetOptions{
		TypeMeta:        metav1.TypeMeta{},
		ResourceVersion: "",
	})
	if err != nil {
		getPodFromAPIServeErrCount.Inc()
	}

	return pod, err
}

// ListPods lists the pod for a given node name by querying the API server cache
func (k *k8sWrapper) ListPods(nodeName string) (*v1.PodList, error) {
	items, err := k.podController.GetDataStore().ByIndex(NodeNameSpec, nodeName)
	if err != nil {
		return nil, err
	}
	podList := &v1.PodList{}
	for _, item := range items {
		podList.Items = append(podList.Items, *item.(*v1.Pod))
	}
	return podList, nil
}

// AnnotatePod annotates the pod with the provided key and value
func (k *k8sWrapper) AnnotatePod(podNamespace string, podName string, key string, val string) error {
	annotatePodRequestCallCount.WithLabelValues(key).Inc()
	ctx := context.Background()

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Get the latest copy of the pod from cache
		var pod *v1.Pod
		var err error
		if pod, err = k.GetPod(podNamespace, podName); err != nil {
			return err
		}
		newPod := pod.DeepCopy()
		newPod.Annotations[key] = val

		return k.cacheClient.Patch(ctx, newPod, client.MergeFrom(pod))
	})

	if err != nil {
		annotatePodRequestErrCount.WithLabelValues(key).Inc()
	}

	return err
}

// GetPod returns the pod object using the client cache
func (k *k8sWrapper) GetPod(namespace string, name string) (*v1.Pod, error) {
	nsName := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}.String()
	obj, exists, err := k.podController.GetDataStore().GetByKey(nsName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("failed to find pod %s", nsName)
	}
	return obj.(*v1.Pod), nil
}

func (k *k8sWrapper) BroadcastPodEvent(pod *v1.Pod, reason string, message string, eventType string) {
	k.eventRecorder.Event(pod, eventType, reason, message)
}

// AdvertiseCapacity advertises the resource capacity for the given resource
func (k *k8sWrapper) AdvertiseCapacityIfNotSet(nodeName string, resourceName string, capacity int) error {
	ctx := context.Background()

	request := types.NamespacedName{
		Name: nodeName,
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		node := &v1.Node{}
		if err := k.cacheClient.Get(ctx, request, node); err != nil {
			return err
		}

		existingCapacity := node.Status.Capacity[v1.ResourceName(resourceName)]
		if !existingCapacity.IsZero() && existingCapacity.Value() == int64(capacity) {
			return nil
		}

		// Capacity doesn't match the expected capacity, need to advertise again
		advertiseResourceRequestCallCount.WithLabelValues(resourceName).Inc()

		newNode := node.DeepCopy()
		newNode.Status.Capacity[v1.ResourceName(resourceName)] = resource.MustParse(strconv.Itoa(capacity))

		return k.cacheClient.Status().Patch(ctx, newNode, client.MergeFrom(node))
	})

	if err != nil {
		advertiseResourceRequestErrCount.WithLabelValues(resourceName).Inc()
	}

	return err
}
