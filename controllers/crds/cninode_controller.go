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
	Scheme           *runtime.Scheme
	Context          context.Context
	Log              logr.Logger
	EC2Wrapper       ec2API.EC2Wrapper
	K8sAPI           k8s.K8sWrapper
	ClusterName      string
	VPCID            string
	FinalizerManager k8s.FinalizerManager
}

//+kubebuilder:rbac:groups=vpcresources.k8s.aws,resources=cninodes,verbs=get;list;watch;create;update;patch;

// Reconcile handles CNINode create/update/delete events
// Reconciler will add the finalizer and cluster name tag if it does not exist and finalize on CNINode on deletion to clean up leaked resource on node
func (r *CNINodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cniNode := &v1alpha1.CNINode{}
	if err := r.Client.Get(ctx, req.NamespacedName, cniNode); err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("CNINode is deleted", "CNINode", req.NamespacedName)
		}
		// Ignore not found error
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cniNode.GetDeletionTimestamp().IsZero() {
		// Add cluster name tag if it does not exist
		clusterNameTagKey := fmt.Sprintf(config.ClusterNameTagKeyFormat, r.ClusterName)
		val, ok := cniNode.Spec.Tags[clusterNameTagKey]
		if !ok || val != config.ClusterNameTagValue {
			cniNodeCopy := cniNode.DeepCopy()
			if len(cniNodeCopy.Spec.Tags) != 0 {
				cniNodeCopy.Spec.Tags[clusterNameTagKey] = config.ClusterNameTagValue
			} else {
				cniNodeCopy.Spec.Tags = map[string]string{
					clusterNameTagKey: config.ClusterNameTagValue,
				}
			}
			return ctrl.Result{}, r.Client.Patch(ctx, cniNodeCopy, client.MergeFromWithOptions(cniNode, client.MergeFromWithOptimisticLock{}))
		}
		if err := r.FinalizerManager.AddFinalizers(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
			r.Log.Error(err, "failed to add finalizer on CNINode, will retry", "cniNode", cniNode.Name, "finalizer", config.NodeTerminationFinalizer)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil

	} else { // CNINode is marked for deletion
		// check if node object exists
		node := &v1.Node{}
		if err := r.Client.Get(ctx, req.NamespacedName, node); err != nil {
			if errors.IsNotFound(err) {
				//  node is also deleted, proceed with running the cleanup routine and remove the finalizer
				r.Log.Info("running the finalizer routine on cniNode", "cniNode", cniNode.Name)
				cleaner := &cleanup.NodeTerminationCleaner{
					NodeName: cniNode.Name,
				}
				cleaner.ENICleaner = &cleanup.ENICleaner{
					EC2Wrapper: r.EC2Wrapper,
					Manager:    cleaner,
					VPCID:      r.VPCID,
					Log:        ctrl.Log.WithName("eniCleaner").WithName("node"),
				}
				// Return err if failed to delete leaked ENIs on node so it can be retried
				if err := cleaner.DeleteLeakedResources(); err != nil {
					r.Log.Error(err, "failed to cleanup resources during node termination, request will be requeued")
					return ctrl.Result{}, err
				}
				if err = r.FinalizerManager.RemoveFinalizers(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
					r.Log.Error(err, "failed to remove finalizer on CNINode, will retry", "cniNode", cniNode.Name, "finalizer", config.NodeTerminationFinalizer)
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			} else {
				r.Log.Error(err, "failed to get the node object in CNINode reconciliation, will retry")
				// Requeue request so it can be retried
				return ctrl.Result{}, err
			}
		} else {
			// node exists, do not run the cleanup routine(periodic cleanup routine will anyway delete leaked ENIs), remove the finalizer
			// to proceed with object deletion, and recreate similar object

			// Create a copy without deletion timestamp for creation
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

			if err := r.FinalizerManager.RemoveFinalizers(ctx, cniNode, config.NodeTerminationFinalizer); err != nil {
				r.Log.Error(err, "failed to remove finalizer on CNINode on node deletion, will retry")
				return ctrl.Result{}, err
			}
			// wait till CNINode is deleted before recreation as the new object will be created with same name to avoid "object already exists" error
			if err := r.waitTillCNINodeDeleted(ctx, k8s.NamespacedName(newCNINode)); err != nil {
				// raise event if CNINode could not be deleted after removing the finalizer
				r.K8sAPI.BroadcastEvent(cniNode, utils.CNINodeDeleteFailed, "CNINode deletion failed and object could not be recreated by the vpc-resource-controller",
					v1.EventTypeWarning)
				// requeue here to check if CNINode deletion is successful and retry CNINode deletion if node exists
				return ctrl.Result{}, err
			}

			recreateCNINodeCallCount.Inc()
			if err := r.createCNINodeFromObj(ctx, newCNINode); err != nil {
				recreateCNINodeErrCount.Inc()
				// raise event on node publish warning that CNINode is deleted and could not be recreated by controller
				utils.SendNodeEventWithNodeName(r.K8sAPI, node.Name, utils.CNINodeCreateFailed,
					fmt.Sprint("CNINode was deleted and failed to be recreated by the vpc-resource-controller"), v1.EventTypeWarning, r.Log)
				// return nil as deleted and we cannot recreate the object now
				return ctrl.Result{}, nil
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CNINodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if !prometheusRegistered {
		prometheusRegister()
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CNINode{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: config.MaxNodeConcurrentReconciles}).
		Complete(r)
}

// waitTillCNINodeDeleted waits for CNINode to be deleted with timeout and returns error
func (r *CNINodeReconciler) waitTillCNINodeDeleted(ctx context.Context, nameSpacedCNINode types.NamespacedName) error {
	oldCNINode := &v1alpha1.CNINode{}

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*10)
	defer cancel()

	return wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		if err := r.Client.Get(ctx, nameSpacedCNINode, oldCNINode); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			} else {
				return false, err
			}
		}
		return false, nil
	})
}

// createCNINodeFromObj will create CNINode with backoff and returns error if CNINode is not recreated
func (r *CNINodeReconciler) createCNINodeFromObj(ctx context.Context, newCNINode client.Object) error {
	r.Log.Info("creating CNINode after it has been deleted as node still exists", "cniNode", newCNINode.GetName())
	return retry.OnError(retry.DefaultBackoff, func(error) bool { return true },
		func() error {
			return r.Client.Create(ctx, newCNINode)
		})
}
