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

package crds

import (
	"context"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	ec2API "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api/cleanup"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	prometheusRegistered     = false
	recreateCNINodeCallCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "recreate_cniNode_call_count",
			Help: "The number of requests made by controller to recreate CNINode when node exists",
		},
	)
	recreateCNINodeErrCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "recreate_cniNode_err_count",
			Help: "The number of requests that failed when controller tried to recreate the CNINode",
		},
	)
	cninodeOperationLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "cninode_operation_latency",
		Help:    "The latency of CNINode operation",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation"})
)

type CleanupTask struct {
	cniNode    *v1alpha1.CNINode
	retryAfter time.Duration
	hasRetried int
}

const (
	cleanupTaskRetryFactor = 2
	cleanupTaskMaxRetry    = 5
	initalRetryDelay       = 20
)

func prometheusRegister() {
	prometheusRegistered = true

	metrics.Registry.MustRegister(
		recreateCNINodeCallCount,
		recreateCNINodeErrCount,
		cninodeOperationLatency,
	)

	prometheusRegistered = true
}

// CNINodeReconciler reconciles a CNINode object
type CNINodeReconciler struct {
	client.Client
	scheme             *runtime.Scheme
	context            context.Context
	log                logr.Logger
	eC2Wrapper         ec2API.EC2Wrapper
	k8sAPI             k8s.K8sWrapper
	clusterName        string
	vpcId              string
	finalizerManager   k8s.FinalizerManager
	deletePool         *semaphore.Weighted
	newResourceCleaner func(nodeID string, eC2Wrapper ec2API.EC2Wrapper, vpcID string, log logr.Logger) cleanup.ResourceCleaner
	cleanupChan        chan any
}

func NewCNINodeReconciler(
	client client.Client,
	scheme *runtime.Scheme,
	ctx context.Context,
	logger logr.Logger,
	ec2Wrapper ec2API.EC2Wrapper,
	k8sWrapper k8s.K8sWrapper,
	clusterName string,
	vpcId string,
	finalizerManager k8s.FinalizerManager,
	maxConcurrentWorkers int,
	newResourceCleaner func(nodeID string, eC2Wrapper ec2API.EC2Wrapper, vpcID string, log logr.Logger) cleanup.ResourceCleaner,
) *CNINodeReconciler {
	return &CNINodeReconciler{
		Client:             client,
		scheme:             scheme,
		context:            ctx,
		log:                logger,
		eC2Wrapper:         ec2Wrapper,
		k8sAPI:             k8sWrapper,
		clusterName:        clusterName,
		vpcId:              vpcId,
		finalizerManager:   finalizerManager,
		deletePool:         semaphore.NewWeighted(int64(maxConcurrentWorkers)),
		newResourceCleaner: newResourceCleaner,
		// use 200% workers to high throughput
		// TODO: tune this value based on UX
		cleanupChan: make(chan any, maxConcurrentWorkers*2),
	}
}

//+kubebuilder:rbac:groups=vpcresources.k8s.aws,resources=cninodes,verbs=get;list;watch;create;update;patch;

// Reconcile handles CNINode create/update/delete events
// Reconciler will add the finalizer and cluster name tag if it does not exist and finalize on CNINode on deletion to clean up leaked resource on node
func (r *CNINodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cniNode := &v1alpha1.CNINode{}
	if err := r.Client.Get(ctx, req.NamespacedName, cniNode); err != nil {
		// Ignore not found error
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	nodeFound := true
	node := &v1.Node{}
	if err := r.Client.Get(ctx, req.NamespacedName, node); err != nil {
		if apierrors.IsNotFound(err) {
			nodeFound = false
		} else {
			r.log.Error(err, "failed to get the node object in CNINode reconciliation, will retry")
			// Requeue request so it can be retried
			return ctrl.Result{}, err
		}
	}

	if cniNode.GetDeletionTimestamp().IsZero() {
		cniNodeCopy := cniNode.DeepCopy()
		shouldPatchTags, err := r.ensureTagsAndLabels(cniNodeCopy, node)
		shouldPatchFinalizer := controllerutil.AddFinalizer(cniNodeCopy, config.NodeTerminationFinalizer)
		createAt := time.Now()

		if shouldPatchTags || shouldPatchFinalizer {
			r.log.Info("patching CNINode to add fields Tags, Labels and finalizer", "cninode", cniNode.Name)
			if err := r.Client.Patch(ctx, cniNodeCopy, client.MergeFromWithOptions(cniNode, client.MergeFromWithOptimisticLock{})); err != nil {
				if apierrors.IsConflict(err) {
					r.log.Info("failed to update cninode", "cninode", cniNode.Name, "error", err)
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, err
			}
			if shouldPatchTags {
				cninodeOperationLatency.WithLabelValues("add_tag").Observe(time.Since(createAt).Seconds())
			}
			if shouldPatchFinalizer {
				cninodeOperationLatency.WithLabelValues("add_finalizer").Observe(time.Since(createAt).Seconds())
			}
		}
		return ctrl.Result{}, err
	} else { // CNINode is marked for deletion
		startAt := time.Now()
		if !nodeFound {
			//  node is also deleted, proceed with running the cleanup routine and remove the finalizer
			// run cleanup for Linux nodes only
			if val, ok := cniNode.ObjectMeta.Labels[config.NodeLabelOS]; ok && val == config.OSLinux {
				// add the CNINode to the cleanup channel to run the cleanup routine
				r.cleanupChan <- CleanupTask{
					cniNode:    cniNode,
					retryAfter: initalRetryDelay * time.Millisecond,
					hasRetried: 0,
				}
			}

			if err := r.removeFinalizer(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
				r.log.Error(err, "failed to remove finalizer on CNINode, will retry", "cniNode", cniNode.Name, "finalizer", config.NodeTerminationFinalizer)
				if apierrors.IsConflict(err) {
					return ctrl.Result{Requeue: true}, nil
				}
				return ctrl.Result{}, err
			}
			cninodeOperationLatency.WithLabelValues("remove_finalizer").Observe(time.Since(startAt).Seconds())
			return ctrl.Result{}, nil
		} else {
			// node exists, do not run the cleanup routine(periodic cleanup routine will delete leaked ENIs), remove the finalizer,
			// delete old CNINode and recreate CNINode from the object

			// Create a copy to recreate CNINode object
			newCNINode := &v1alpha1.CNINode{
				ObjectMeta: metav1.ObjectMeta{
					Name:            cniNode.Name,
					Namespace:       "",
					OwnerReferences: cniNode.OwnerReferences,
					// TODO: should we include finalizers at object creation or let controller patch it on Create/Update event?
					Finalizers: cniNode.Finalizers,
				},
				Spec: cniNode.Spec,
			}

			if err := r.removeFinalizer(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
				r.log.Error(err, "failed to remove finalizer on CNINode, will retry")
				return ctrl.Result{}, err
			}
			cninodeOperationLatency.WithLabelValues("remove_finalizer").Observe(time.Since(startAt).Seconds())

			// wait till CNINode is deleted before recreation as the new object will be created with same name to avoid "object already exists" error
			if err := r.waitTillCNINodeDeleted(client.ObjectKeyFromObject(newCNINode)); err != nil {
				// raise event if CNINode was not deleted after removing the finalizer
				r.k8sAPI.BroadcastEvent(cniNode, utils.CNINodeDeleteFailed, "CNINode delete failed, will be retried",
					v1.EventTypeWarning)
				// requeue to retry CNINode deletion if node exists
				return ctrl.Result{}, err
			}

			r.log.Info("creating CNINode after it has been deleted as node still exists", "cniNode", newCNINode.Name)
			recreateCNINodeCallCount.Inc()
			if err := r.createCNINodeFromObj(ctx, newCNINode); err != nil {
				recreateCNINodeErrCount.Inc()
				// raise event on if CNINode is deleted and could not be recreated by controller
				utils.SendNodeEventWithNodeName(r.k8sAPI, node.Name, utils.CNINodeCreateFailed,
					"CNINode was deleted and failed to be recreated by the vpc-resource-controller", v1.EventTypeWarning, r.log)
				// return nil as object is deleted and we cannot recreate the object now
				return ctrl.Result{}, nil
			}
			cninodeOperationLatency.WithLabelValues("re_create").Observe(time.Since(startAt).Seconds())
			r.log.Info("successfully recreated CNINode", "cniNode", newCNINode.Name)
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CNINodeReconciler) SetupWithManager(mgr ctrl.Manager, maxNodeConcurrentReconciles int) error {
	if !prometheusRegistered {
		prometheusRegister()
	}

	// start a watching goroutine for taking cninode cleanup tasks
	go r.watchCleanupTasks()

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CNINode{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxNodeConcurrentReconciles}).
		Complete(r)
}

func (r *CNINodeReconciler) watchCleanupTasks() {
	for {
		select {
		case task := <-r.cleanupChan:
			r.processCleanupTasks(r.context, task.(CleanupTask))
		case <-r.context.Done():
			r.log.Info("context cancelled and stop cninodes cleanup task")
			return
		}
	}
}

func (r *CNINodeReconciler) processCleanupTasks(ctx context.Context, task CleanupTask) {
	log := r.log.WithValues("cniNode", task.cniNode.Name)
	log.Info("running the finalizer routine on cniNode", "cniNode", task.cniNode.Name)
	// run cleanup when node id is present
	if nodeID, ok := task.cniNode.Spec.Tags[config.NetworkInterfaceNodeIDKey]; ok && nodeID != "" {
		if !r.deletePool.TryAcquire(1) {
			if task.hasRetried >= cleanupTaskMaxRetry {
				log.Info("will not requeue request as max retries are already done")
				return
			}
			log.Info("will requeue request after", "after", task.retryAfter)
			time.Sleep(task.retryAfter)
			task.retryAfter *= cleanupTaskRetryFactor
			task.hasRetried += 1
			r.cleanupChan <- task
			return
		}
		go func(nodeID string) {
			defer r.deletePool.Release(1)
			childCtx, cancel := context.WithTimeout(ctx, config.NodeTerminationTimeout)
			defer cancel()
			if err := r.newResourceCleaner(nodeID, r.eC2Wrapper, r.vpcId, r.log).DeleteLeakedResources(childCtx); err != nil {
				log.Error(err, "failed to cleanup resources during node termination")
				ec2API.NodeTerminationENICleanupFailure.Inc()
			}
			log.Info("successfully cleaned up resources during node termination", "nodeID", nodeID)
		}(nodeID)
	}
}

// waitTillCNINodeDeleted waits for CNINode to be deleted with timeout and returns error
func (r *CNINodeReconciler) waitTillCNINodeDeleted(nameSpacedCNINode types.NamespacedName) error {
	oldCNINode := &v1alpha1.CNINode{}

	return wait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, time.Second*3, true, func(ctx context.Context) (bool, error) {
		if err := r.Client.Get(ctx, nameSpacedCNINode, oldCNINode); err != nil && apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, nil
	})
}

// createCNINodeFromObj will create CNINode with backoff and returns error if CNINode is not recreated
func (r *CNINodeReconciler) createCNINodeFromObj(ctx context.Context, newCNINode client.Object) error {
	return retry.OnError(retry.DefaultBackoff, func(error) bool { return true },
		func() error {
			return r.Client.Create(ctx, newCNINode)
		})
}

func (r *CNINodeReconciler) ensureTagsAndLabels(cniNode *v1alpha1.CNINode, node *v1.Node) (bool, error) {
	shouldPatch := false
	var err error
	if cniNode.Spec.Tags == nil {
		cniNode.Spec.Tags = make(map[string]string)
	}
	// add cluster name tag if it does not exist
	if cniNode.Spec.Tags[config.VPCCNIClusterNameKey] != r.clusterName {
		cniNode.Spec.Tags[config.VPCCNIClusterNameKey] = r.clusterName
		shouldPatch = true
	}
	if node != nil {
		var nodeID string
		nodeID, err = utils.GetNodeID(node)

		if nodeID != "" && cniNode.Spec.Tags[config.NetworkInterfaceNodeIDKey] != nodeID {
			cniNode.Spec.Tags[config.NetworkInterfaceNodeIDKey] = nodeID
			shouldPatch = true
		}

		// add node label if it does not exist
		if cniNode.ObjectMeta.Labels == nil {
			cniNode.ObjectMeta.Labels = make(map[string]string)
		}
		if cniNode.ObjectMeta.Labels[config.NodeLabelOS] != node.ObjectMeta.Labels[config.NodeLabelOS] {
			cniNode.ObjectMeta.Labels[config.NodeLabelOS] = node.ObjectMeta.Labels[config.NodeLabelOS]
			shouldPatch = true
		}
	}
	return shouldPatch, err
}

func (r *CNINodeReconciler) removeFinalizer(ctx context.Context, cniNode *v1alpha1.CNINode, finalizer string) error {
	cniNodeCopy := cniNode.DeepCopy()

	if controllerutil.RemoveFinalizer(cniNodeCopy, finalizer) {
		r.log.Info("removing finalizer for cninode", "name", cniNode.GetName(), "finalizer", finalizer)
		return r.Client.Patch(ctx, cniNodeCopy, client.MergeFromWithOptions(cniNode, client.MergeFromWithOptimisticLock{}))
	}
	return nil
}
