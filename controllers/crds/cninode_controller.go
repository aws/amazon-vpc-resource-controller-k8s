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
	"fmt"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	ec2API "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api/cleanup"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/awslabs/operatorpkg/singleton"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
)

func prometheusRegister() {
	prometheusRegistered = true

	metrics.Registry.MustRegister(
		recreateCNINodeCallCount,
		recreateCNINodeErrCount)

	prometheusRegistered = true
}

// CNINodeReconciler reconciles a CNINode object
type CNINodeReconciler struct {
	client.Client
	scheme           *runtime.Scheme
	context          context.Context
	log              logr.Logger
	eC2Wrapper       ec2API.EC2Wrapper
	k8sAPI           k8s.K8sWrapper
	clusterName      string
	vpcId            string
	finalizerManager k8s.FinalizerManager
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
) *CNINodeReconciler {
	return &CNINodeReconciler{
		Client:           client,
		scheme:           scheme,
		context:          ctx,
		log:              logger,
		eC2Wrapper:       ec2Wrapper,
		k8sAPI:           k8sWrapper,
		clusterName:      clusterName,
		vpcId:            vpcId,
		finalizerManager: finalizerManager,
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
		if errors.IsNotFound(err) {
			nodeFound = false
		} else {
			r.log.Error(err, "failed to get the node object in CNINode reconciliation, will retry")
			// Requeue request so it can be retried
			return ctrl.Result{}, err
		}
	}

	if cniNode.GetDeletionTimestamp().IsZero() {
		shouldPatch := false
		cniNodeCopy := cniNode.DeepCopy()
		// Add cluster name tag if it does not exist
		val, ok := cniNode.Spec.Tags[config.CNINodeClusterNameKey]
		if !ok || val != r.clusterName {
			if len(cniNodeCopy.Spec.Tags) != 0 {
				cniNodeCopy.Spec.Tags[config.CNINodeClusterNameKey] = r.clusterName
			} else {
				cniNodeCopy.Spec.Tags = map[string]string{
					config.CNINodeClusterNameKey: r.clusterName,
				}
			}
			shouldPatch = true
		}
		// if node exists, get & add OS label if it does not exist on CNINode
		if nodeFound {
			nodeLabelOS := node.ObjectMeta.Labels[config.NodeLabelOS]
			val, ok = cniNode.ObjectMeta.Labels[config.NodeLabelOS]
			if !ok || val != nodeLabelOS {
				if len(cniNodeCopy.ObjectMeta.Labels) != 0 {
					cniNodeCopy.ObjectMeta.Labels[config.NodeLabelOS] = nodeLabelOS
				} else {
					cniNodeCopy.ObjectMeta.Labels = map[string]string{
						config.NodeLabelOS: nodeLabelOS,
					}
				}
				shouldPatch = true
			}
		}

		if shouldPatch {
			r.log.Info("patching CNINode to add required fields Tags and Labels", "cninode", cniNode.Name)
			return ctrl.Result{}, r.Client.Patch(ctx, cniNodeCopy, client.MergeFromWithOptions(cniNode, client.MergeFromWithOptimisticLock{}))
		}

		// Add finalizer if it does not exist
		if err := r.finalizerManager.AddFinalizers(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
			r.log.Error(err, "failed to add finalizer on CNINode, will retry", "cniNode", cniNode.Name, "finalizer", config.NodeTerminationFinalizer)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	} else { // CNINode is marked for deletion
		if !nodeFound {
			//  node is also deleted, proceed with running the cleanup routine and remove the finalizer

			// run cleanup for Linux nodes only
			if val, ok := cniNode.ObjectMeta.Labels[config.NodeLabelOS]; ok && val == config.OSLinux {
				r.log.Info("running the finalizer routine on cniNode", "cniNode", cniNode.Name)
				cleaner := &cleanup.NodeTerminationCleaner{
					NodeID: cniNode.Spec.Tags[config.NetworkInterfaceNodeIDKey],
				}
				cleaner.ENICleaner = &cleanup.ENICleaner{
					EC2Wrapper: r.eC2Wrapper,
					Manager:    cleaner,
					VpcId:      r.vpcId,
					Log:        ctrl.Log.WithName("eniCleaner").WithName("node"),
				}

				if err := cleaner.DeleteLeakedResources(); err != nil {
					r.log.Error(err, "failed to cleanup resources during node termination")
					ec2API.NodeTerminationENICleanupFailure.Inc()
				}
			}

			if err := r.finalizerManager.RemoveFinalizers(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
				r.log.Error(err, "failed to remove finalizer on CNINode, will retry", "cniNode", cniNode.Name, "finalizer", config.NodeTerminationFinalizer)
				return ctrl.Result{}, err
			}
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

			if err := r.finalizerManager.RemoveFinalizers(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
				r.log.Error(err, "failed to remove finalizer on CNINode, will retry")
				return ctrl.Result{}, err
			}
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
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CNINode{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxNodeConcurrentReconciles}).
		Complete(r)
}

// waitTillCNINodeDeleted waits for CNINode to be deleted with timeout and returns error
func (r *CNINodeReconciler) waitTillCNINodeDeleted(nameSpacedCNINode types.NamespacedName) error {
	oldCNINode := &v1alpha1.CNINode{}

	return wait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, time.Second*3, true, func(ctx context.Context) (bool, error) {
		if err := r.Client.Get(ctx, nameSpacedCNINode, oldCNINode); err != nil && errors.IsNotFound(err) {
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

type CNINodeCleaner struct {
	k8sClient k8s.K8sWrapper
	log       logr.Logger
}

func NewCNINodeCleaner(client k8s.K8sWrapper, log logr.Logger) *CNINodeCleaner {
	return &CNINodeCleaner{
		k8sClient: client,
		log:       log,
	}
}

func (c *CNINodeCleaner) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("cninode-cleaner").
		WithOptions(controller.Options{RateLimiter: reasonable.RateLimiter()}).
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(c))
}

func (c *CNINodeCleaner) Reconcile(ctx context.Context) (reconcile.Result, error) {
	cniNodeList, err := c.k8sClient.ListCNINodes()
	c.log.Info("get cninodes", "cninodes", len(cniNodeList))
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("listing cni nodes, %w", err)
	}

	for _, oldCNINode := range cniNodeList {
		newCNINode := oldCNINode.DeepCopy()
		// if the cninode has finalizer, remove it and then delete the resource
		// otherwise just delete the resource
		if yes := controllerutil.RemoveFinalizer(newCNINode, config.NodeTerminationFinalizer); yes {
			if err := c.k8sClient.PatchCNINode(oldCNINode, newCNINode); err != nil {
				c.log.Info("patch cninode failed", "cninode", newCNINode.Name, "error", err.Error())
				continue
			}
		}
		if err := c.k8sClient.DeleteCNINode(newCNINode); err != nil {
			c.log.Info("delete cninode failed", "cninode", newCNINode.Name, "error", err.Error())
		}
		c.log.Info("deleted cninode", "cninode", oldCNINode.Name)
	}

	return reconcile.Result{RequeueAfter: 1 * time.Hour}, nil
}
