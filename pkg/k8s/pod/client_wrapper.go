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

package pod

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
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
)

type PodClientAPIWrapper interface {
	GetPod(namespace string, name string) (*v1.Pod, error)
	ListPods(nodeName string) (*v1.PodList, error)
	AnnotatePod(podNamespace string, podName string, key string, val string) error
	GetPodFromAPIServer(ctx context.Context, namespace string, name string) (*v1.Pod, error)
	GetRunningPodsOnNode(nodeName string) ([]v1.Pod, error)
}

type podClientAPIWrapper struct {
	// All cache operations for Pod must be done using the data store.
	// This data store is maintained using the custom controller
	dataStore cache.Indexer
	// !!WARNING!!
	// Don't use this client for performing Read operation on Pods
	client client.Client
	// coreV1 for performing Read operations directly from API Server
	coreV1 corev1.CoreV1Interface
}

func prometheusRegister() {
	prometheusRegistered = true

	metrics.Registry.MustRegister(
		annotatePodRequestCallCount,
		annotatePodRequestErrCount,
		getPodFromAPIServeCallCount,
		getPodFromAPIServeErrCount)

	prometheusRegistered = true
}

func NewPodAPIWrapper(dataStore cache.Indexer, client client.Client,
	coreV1 corev1.CoreV1Interface) PodClientAPIWrapper {

	if !prometheusRegistered {
		prometheusRegister()
	}

	return &podClientAPIWrapper{
		dataStore: dataStore,
		client:    client,
		coreV1:    coreV1,
	}
}

func (p *podClientAPIWrapper) GetRunningPodsOnNode(nodeName string) ([]v1.Pod, error) {
	allPodList, err := p.ListPods(nodeName)
	if err != nil {
		return nil, err
	}

	var runningPods []v1.Pod
	for _, pod := range allPodList.Items {
		if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
			// Since we only want the running Pods we will skip adding
			// the failed/succeeded Pods to the returned list
			continue
		}
		runningPods = append(runningPods, pod)
	}
	return runningPods, nil
}

// ListPods lists the pod for a given node name by querying the API server cache
func (p *podClientAPIWrapper) ListPods(nodeName string) (*v1.PodList, error) {
	items, err := p.dataStore.ByIndex(NodeNameSpec, nodeName)
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
func (p *podClientAPIWrapper) AnnotatePod(podNamespace string, podName string, key string, val string) error {
	annotatePodRequestCallCount.WithLabelValues(key).Inc()
	ctx := context.Background()

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// Get the latest copy of the pod from cache
		var pod *v1.Pod
		var err error
		if pod, err = p.GetPod(podNamespace, podName); err != nil {
			return err
		}
		newPod := pod.DeepCopy()
		newPod.Annotations[key] = val

		return p.client.Patch(ctx, newPod, client.MergeFrom(pod))
	})

	if err != nil {
		annotatePodRequestErrCount.WithLabelValues(key).Inc()
	}

	return err
}

// GetPod returns the pod object using the client cache
func (p *podClientAPIWrapper) GetPod(namespace string, name string) (*v1.Pod, error) {
	nsName := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}.String()
	obj, exists, err := p.dataStore.GetByKey(nsName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("failed to find pod %s", nsName)
	}
	return obj.(*v1.Pod), nil
}

// GetPodFromAPIServer returns the pod details by querying the API Server directly
func (p *podClientAPIWrapper) GetPodFromAPIServer(ctx context.Context, namespace string, name string) (*v1.Pod, error) {
	getPodFromAPIServeCallCount.Inc()
	pod, err := p.coreV1.Pods(namespace).Get(ctx, name, metav1.GetOptions{
		TypeMeta:        metav1.TypeMeta{},
		ResourceVersion: "",
	})
	if err != nil {
		getPodFromAPIServeErrCount.Inc()
	}

	return pod, err
}
