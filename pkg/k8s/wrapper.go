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

	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	prometheusRegistered = false

	annotateRequestReceivedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "annotate_request_count",
			Help: "The number of request made to annotate the pod",
		},
		[]string{"annotate_key"},
	)
	annotateRequestSuccessfulCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "annotate_request_completed_successfully_count",
			Help: "The number of request that successfully annotated the pod",
		},
		[]string{"annotate_key"},
	)
)

func prometheusRegister() {
	prometheus.MustRegister(annotateRequestReceivedCount)
	prometheus.MustRegister(annotateRequestSuccessfulCount)
}

// K8sWrapper represents an interface with all the common operations on K8s objects
type K8sWrapper interface {
	AnnotatePod(pod *v1.Pod, mapKey string, mapVal string) error
	GetPod(namespace string, name string) (*v1.Pod, error)
}

// k8sWrapper is the wrapper object with the client
type k8sWrapper struct {
	client client.Client
}

// NewK8sWrapper returns a new K8sWrapper
func NewK8sWrapper(client client.Client) K8sWrapper {
	prometheusRegister()
	return &k8sWrapper{client: client}
}

// AnnotatePod annotates the pod with the provided key and value
func (k *k8sWrapper) AnnotatePod(pod *v1.Pod, key string, val string) error {
	annotateRequestReceivedCount.WithLabelValues(key).Inc()
	ctx := context.Background()

	oldPod := pod.DeepCopy()

	pod.Annotations[key] = val

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		return k.client.Patch(ctx, pod, client.MergeFrom(oldPod))
	})

	if err != nil {
		return err
	}

	annotateRequestSuccessfulCount.WithLabelValues(key).Inc()
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
