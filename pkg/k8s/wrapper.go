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
	"strconv"

	"github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/prometheus/client_golang/prometheus"

	appV1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	prometheusRegistered = false

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
	metrics.Registry.MustRegister(
		advertiseResourceRequestErrCount,
		advertiseResourceRequestCallCount)

	prometheusRegistered = true
}

// K8sWrapper represents an interface with all the common operations on K8s objects
type K8sWrapper interface {
	GetDaemonSet(namespace, name string) (*appV1.DaemonSet, error)
	GetNode(nodeName string) (*v1.Node, error)
	AdvertiseCapacityIfNotSet(nodeName string, resourceName string, capacity int) error
	GetENIConfig(eniConfigName string) (*v1alpha1.ENIConfig, error)
	GetDeployment(namespace string, name string) (*appV1.Deployment, error)
	BroadcastEvent(obj runtime.Object, reason string, message string, eventType string)
	GetConfigMap(configMapName string, configMapNamespace string) (*v1.ConfigMap, error)
	ListNodes() (*v1.NodeList, error)
	AddLabelToManageNode(node *v1.Node, labelKey string, labelValue string) error
	ListEvents(ops []client.ListOption) (*apiv1.EventList, error)
}

// k8sWrapper is the wrapper object with the client
type k8sWrapper struct {
	// cacheClient MUST never be used for getting Pods. The Pods
	// can be retrieved using the separate Pod Wrapper. For all
	// other K8s Object use the cache client
	cacheClient   client.Client
	eventRecorder record.EventRecorder
	context       context.Context
}

// NewK8sWrapper returns a new K8sWrapper
func NewK8sWrapper(client client.Client, coreV1 corev1.CoreV1Interface, ctx context.Context) K8sWrapper {
	if !prometheusRegistered {
		prometheusRegister()
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: coreV1.Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{
		Component: config.ControllerName,
	})
	return &k8sWrapper{cacheClient: client, eventRecorder: recorder, context: ctx}
}

func (k *k8sWrapper) GetDaemonSet(name, namespace string) (*appV1.DaemonSet, error) {
	ds := &appV1.DaemonSet{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, ds)
	return ds, err
}

func (k *k8sWrapper) GetDeployment(namespace string, name string) (*appV1.Deployment, error) {
	deployment := &appV1.Deployment{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, deployment)
	return deployment, err
}

func (k *k8sWrapper) GetENIConfig(eniConfigName string) (*v1alpha1.ENIConfig, error) {
	sgp := &v1alpha1.ENIConfig{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Name: eniConfigName,
	}, sgp)

	return sgp, err
}

func (k *k8sWrapper) GetNode(nodeName string) (*v1.Node, error) {
	node := &v1.Node{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Name: nodeName,
	}, node)
	return node, err
}

func (k *k8sWrapper) BroadcastEvent(object runtime.Object, reason string, message string, eventType string) {
	k.eventRecorder.Event(object, eventType, reason, message)
}

// AdvertiseCapacity advertises the resource capacity for the given resource
func (k *k8sWrapper) AdvertiseCapacityIfNotSet(nodeName string, resourceName string, capacity int) error {

	request := types.NamespacedName{
		Name: nodeName,
	}

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		node := &v1.Node{}
		if err := k.cacheClient.Get(k.context, request, node); err != nil {
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

		return k.cacheClient.Status().Patch(k.context, newNode, client.MergeFrom(node))
	})

	if err != nil {
		advertiseResourceRequestErrCount.WithLabelValues(resourceName).Inc()
	}

	return err
}

func (k *k8sWrapper) GetConfigMap(configMapName string, configMapNamespace string) (*v1.ConfigMap, error) {
	configMap := &v1.ConfigMap{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Name:      configMapName,
		Namespace: configMapNamespace,
	}, configMap)
	return configMap, err
}

func (k *k8sWrapper) ListNodes() (*v1.NodeList, error) {
	nodeList := &v1.NodeList{}
	err := k.cacheClient.List(k.context, nodeList)
	return nodeList, err
}

func (k *k8sWrapper) AddLabelToManageNode(node *v1.Node, labelKey string, labelValue string) error {
	if node.Labels[labelKey] == labelValue {
		return nil
	} else {
		newNode := node.DeepCopy()
		newNode.Labels[labelKey] = labelValue
		return k.cacheClient.Status().Patch(k.context, newNode, client.MergeFrom(node))
	}
}

func (k *k8sWrapper) ListEvents(ops []client.ListOption) (*apiv1.EventList, error) {
	events := &apiv1.EventList{}
	if err := k.cacheClient.List(k.context, events, ops...); err != nil {
		return nil, err
	}
	return events, nil
}
