/*


 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package k8s

import (
	"context"
	"strconv"

	"github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/prometheus/client_golang/prometheus"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	NodeNameSpec = "spec.nodeName"
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
	cacheClient   client.Client
	coreV1        corev1.CoreV1Interface
	eventRecorder record.EventRecorder
}

// NewK8sWrapper returns a new K8sWrapper
func NewK8sWrapper(client client.Client, coreV1 corev1.CoreV1Interface) K8sWrapper {
	if !prometheusRegistered {
		prometheusRegister()
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: coreV1.Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{
		Component: config.ControllerName,
	})
	return &k8sWrapper{cacheClient: client, coreV1: coreV1, eventRecorder: recorder}
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
	ctx := context.Background()

	podList := &v1.PodList{}
	err := k.cacheClient.List(ctx, podList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(NodeNameSpec, nodeName)})

	return podList, err
}

// AnnotatePod annotates the pod with the provided key and value
func (k *k8sWrapper) AnnotatePod(podNamespace string, podName string, key string, val string) error {
	annotatePodRequestCallCount.WithLabelValues(key).Inc()
	ctx := context.Background()

	request := types.NamespacedName{
		Namespace: podNamespace,
		Name:      podName,
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Get the latest copy of the pod from cache
		pod := &v1.Pod{}
		if err := k.cacheClient.Get(ctx, request, pod); err != nil {
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
	ctx := context.Background()

	pod := &v1.Pod{}
	err := k.cacheClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod)
	return pod, err

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
