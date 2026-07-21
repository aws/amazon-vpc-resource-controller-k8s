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
	"time"

	"github.com/aws/amazon-vpc-cni-k8s/pkg/apis/crd/v1alpha1"
	rcv1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"

	appv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// cniNodeStatusUpdateBackoff bounds the retry of UpdateCNINodeStatus. Unlike
// retry.DefaultBackoff (which only exists to smooth out write conflicts over a
// few milliseconds), this also has to absorb the window where the CNINode was
// created moments earlier by AddNode, or was just deleted+recreated by the
// CNINode controller during node churn, so the object is briefly NotFound on a
// non-cached read. Steps/Duration/Factor are kept intentionally small so the
// onboarding worker is never blocked for more than a few seconds; if the object
// still isn't there the periodic self-heal (manager.CheckNodeForLeakedENIs)
// converges the status on a later reconcile.
var cniNodeStatusUpdateBackoff = wait.Backoff{
	Duration: 200 * time.Millisecond,
	Factor:   2.0,
	Jitter:   0.1,
	Steps:    4, // ~200ms + 400ms + 800ms + 1600ms => <=3s worst case
}

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
	GetDaemonSet(namespace, name string) (*appv1.DaemonSet, error)
	GetNode(nodeName string) (*v1.Node, error)
	AdvertiseCapacityIfNotSet(nodeName string, resourceName string, capacity int) error
	GetENIConfig(eniConfigName string) (*v1alpha1.ENIConfig, error)
	GetDeployment(namespace string, name string) (*appv1.Deployment, error)
	BroadcastEvent(obj runtime.Object, reason string, message string, eventType string)
	GetConfigMap(configMapName string, configMapNamespace string) (*v1.ConfigMap, error)
	ListNodes() (*v1.NodeList, error)
	AddLabelToManageNode(node *v1.Node, labelKey string, labelValue string) (bool, error)
	ListEvents(ops []client.ListOption) (*eventsv1.EventList, error)
	GetCNINode(namespacedName types.NamespacedName) (*rcv1alpha1.CNINode, error)
	CreateCNINode(node *v1.Node, clusterName string) error
	ListCNINodes() ([]*rcv1alpha1.CNINode, error)
	PatchCNINode(oldCNINode, newCNINode *rcv1alpha1.CNINode) error
	UpdateCNINodeStatus(nodeName string, status rcv1alpha1.CNINodeStatus) error
	DeleteCNINode(cniNode *rcv1alpha1.CNINode) error
}

// k8sWrapper is the wrapper object with the client
type k8sWrapper struct {
	// cacheClient MUST never be used for getting Pods. The Pods
	// can be retrieved using the separate Pod Wrapper. For all
	// other K8s Object use the cache client
	cacheClient client.Client
	// apiReader is a non-cached reader that reads directly from the API server.
	// It is used where the informer cache may be stale, e.g. reading back a
	// CNINode that was created moments earlier before patching its status.
	apiReader     client.Reader
	eventRecorder record.EventRecorder
	context       context.Context
}

// NewK8sWrapper returns a new K8sWrapper. apiReader is a non-cached reader
// (mgr.GetAPIReader()) used for reads that must not be served from a possibly
// stale informer cache. It may be nil, in which case those reads fall back to
// the cache client.
func NewK8sWrapper(client client.Client, apiReader client.Reader, coreV1 corev1.CoreV1Interface, ctx context.Context) K8sWrapper {
	if !prometheusRegistered {
		prometheusRegister()
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&corev1.EventSinkImpl{Interface: coreV1.Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{
		Component: config.ControllerName,
	})
	return &k8sWrapper{cacheClient: client, apiReader: apiReader, eventRecorder: recorder, context: ctx}
}

func (k *k8sWrapper) GetDaemonSet(name, namespace string) (*appv1.DaemonSet, error) {
	ds := &appv1.DaemonSet{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, ds)
	return ds, err
}

func (k *k8sWrapper) GetDeployment(namespace string, name string) (*appv1.Deployment, error) {
	deployment := &appv1.Deployment{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, deployment)
	return deployment, err
}

func (k *k8sWrapper) GetENIConfig(eniConfigName string) (*v1alpha1.ENIConfig, error) {
	eniConfig := &v1alpha1.ENIConfig{}
	err := k.cacheClient.Get(k.context, types.NamespacedName{
		Name: eniConfigName,
	}, eniConfig)

	return eniConfig, err
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

		// in case if the node is returned without initialized Capacity map for any reason
		// we need to handle the nil map gracefully and retry
		// metav1.Status{Reason: metav1.StatusReasonConflict} is an error that is retriable regarding
		// https://github.com/kubernetes/client-go/blob/v0.21.3/util/retry/util.go#L103-L105
		if node.Status.Capacity == nil {
			return &errors.StatusError{
				ErrStatus: metav1.Status{
					Reason: metav1.StatusReasonConflict,
				},
			}
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

func (k *k8sWrapper) AddLabelToManageNode(node *v1.Node, labelKey string, labelValue string) (bool, error) {
	if node.Labels[labelKey] == labelValue {
		return false, nil
	} else {
		newNode := node.DeepCopy()
		newNode.Labels[labelKey] = labelValue
		err := k.cacheClient.Status().Patch(k.context, newNode, client.MergeFrom(node))
		return err == nil, err
	}
}

func (k *k8sWrapper) ListEvents(ops []client.ListOption) (*eventsv1.EventList, error) {
	events := &eventsv1.EventList{}
	if err := k.cacheClient.List(k.context, events, ops...); err != nil {
		return nil, err
	}
	return events, nil
}

func (k *k8sWrapper) GetCNINode(namespacedName types.NamespacedName) (*rcv1alpha1.CNINode, error) {
	cninode := &rcv1alpha1.CNINode{}
	if err := k.cacheClient.Get(k.context, namespacedName, cninode); err != nil {
		return cninode, err
	}
	return cninode, nil
}

func (k *k8sWrapper) CreateCNINode(node *v1.Node, clusterName string) error {
	cniNode := &rcv1alpha1.CNINode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name,
			Namespace: "",
			// use the node as owner reference to let k8s clean up the CRD when the node is deleted
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: node.APIVersion,
					Kind:       node.Kind,
					Name:       node.Name,
					UID:        node.UID,
					Controller: lo.ToPtr(true),
				},
			},
			Labels: map[string]string{
				// OS is a standard label & is set by Kubernetes, so we can skip checking if it is set
				config.NodeLabelOS: node.ObjectMeta.Labels[config.NodeLabelOS],
			},
			Finalizers: []string{config.NodeTerminationFinalizer}, // finalizer to clean up leaked ENIs at node termination
		},
		Spec: rcv1alpha1.CNINodeSpec{
			Tags: map[string]string{
				fmt.Sprintf(config.VPCCNIClusterNameKey): clusterName,
			},
		},
	}

	// TODO: need think more if we should retry on "already exists" error.
	return client.IgnoreAlreadyExists(k.cacheClient.Create(k.context, cniNode))
}

func (k *k8sWrapper) DeleteCNINode(cniNode *rcv1alpha1.CNINode) error {
	return k.cacheClient.Delete(k.context, cniNode)
}

func (k *k8sWrapper) ListCNINodes() ([]*rcv1alpha1.CNINode, error) {
	cniNodes := &rcv1alpha1.CNINodeList{}
	if err := k.cacheClient.List(k.context, cniNodes); err != nil {
		return nil, err
	}
	return lo.ToSlicePtr(cniNodes.Items), nil
}

func (k *k8sWrapper) PatchCNINode(oldCNINode, newCNINode *rcv1alpha1.CNINode) error {
	return k.cacheClient.Patch(k.context, newCNINode, client.MergeFromWithOptions(oldCNINode, client.MergeFromWithOptimisticLock{}))
}

func (k *k8sWrapper) UpdateCNINodeStatus(nodeName string, status rcv1alpha1.CNINodeStatus) error {
	request := types.NamespacedName{Name: nodeName}

	// reader is the non-cached API server reader so we read back a CNINode that
	// may have been created moments earlier (and is not yet in the informer
	// cache) before patching its status. Fall back to the cache client only if
	// no apiReader was wired.
	reader := client.Reader(k.cacheClient)
	if k.apiReader != nil {
		reader = k.apiReader
	}

	// Retry on both Conflict (optimistic-lock write races) and NotFound (the
	// object was created moments ago by AddNode, or deleted+recreated by the
	// CNINode controller during node churn). The backoff is bounded so this
	// never blocks the onboarding worker for long; on exhaustion we return the
	// error to the best-effort caller and let the periodic self-heal converge.
	retriable := func(err error) bool {
		return errors.IsConflict(err) || errors.IsNotFound(err)
	}

	return retry.OnError(cniNodeStatusUpdateBackoff, retriable, func() error {
		cniNode := &rcv1alpha1.CNINode{}
		if err := reader.Get(k.context, request, cniNode); err != nil {
			return err
		}

		newCNINode := cniNode.DeepCopy()
		newCNINode.Status = status
		// Use an optimistic lock (keyed on resourceVersion) so a concurrent status
		// write reliably surfaces as a Conflict and is retried against a freshly
		// re-read object above, instead of silently clobbering the other writer.
		// The retry is bounded by cniNodeStatusUpdateBackoff, so this cannot churn.
		return k.cacheClient.Status().Patch(k.context, newCNINode, client.MergeFromWithOptions(cniNode, client.MergeFromWithOptimisticLock{}))
	})
}
