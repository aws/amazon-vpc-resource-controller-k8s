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

	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
)

func prometheusRegister() {
	prometheus.MustRegister(annotatePodRequestErrCount)
	prometheus.MustRegister(annotatePodRequestCallCount)
	prometheus.MustRegister(advertiseResourceRequestErrCount)
	prometheus.MustRegister(advertiseResourceRequestCallCount)

	prometheusRegistered = true
}

// K8sWrapper represents an interface with all the common operations on K8s objects
type K8sWrapper interface {
	AnnotatePod(pod *v1.Pod, mapKey string, mapVal string) error
	GetPod(namespace string, name string) (*v1.Pod, error)
	AdvertiseCapacity(nodeName string, resourceName string, capacity int) error
}

// k8sWrapper is the wrapper object with the client
type k8sWrapper struct {
	client client.Client
}

// NewK8sWrapper returns a new K8sWrapper
func NewK8sWrapper(client client.Client) K8sWrapper {
	if !prometheusRegistered {
		prometheusRegister()
	}
	return &k8sWrapper{client: client}
}

// AnnotatePod annotates the pod with the provided key and value
func (k *k8sWrapper) AnnotatePod(pod *v1.Pod, key string, val string) error {
	annotatePodRequestCallCount.WithLabelValues(key).Inc()
	ctx := context.Background()

	newPod := pod.DeepCopy()
	newPod.Annotations[key] = val

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		return k.client.Patch(ctx, newPod, client.MergeFrom(pod))
	})

	if err != nil {
		annotatePodRequestErrCount.WithLabelValues(key).Inc()
		return err
	}

	return nil
}

// GetPod returns the pod object using the client cache
func (k *k8sWrapper) GetPod(namespace string, name string) (*v1.Pod, error) {
	ctx := context.Background()

	pod := &v1.Pod{}
	if err := k.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

// AdvertiseCapacity advertises the resource capacity for the given resource
func (k *k8sWrapper) AdvertiseCapacity(nodeName string, resourceName string, capacity int) error {
	advertiseResourceRequestCallCount.WithLabelValues(resourceName).Inc()
	ctx := context.Background()

	node := &v1.Node{}
	if err := k.client.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		return err
	}

	newNode := node.DeepCopy()
	newNode.Status.Capacity[v1.ResourceName(resourceName)] = resource.MustParse(strconv.Itoa(capacity))

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		return k.client.Patch(ctx, newNode, client.MergeFrom(node))
	})

	if err != nil {
		advertiseResourceRequestErrCount.WithLabelValues(resourceName).Inc()
		return err
	}

	return nil
}
