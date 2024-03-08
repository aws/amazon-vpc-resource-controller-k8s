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

	"github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	ec2API "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api/cleanup"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// CNINodeReconciler reconciles a CNINode object
type CNINodeReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Context     context.Context
	Log         logr.Logger
	EC2Wrapper  ec2API.EC2Wrapper
	K8sAPI      k8s.K8sWrapper
	ClusterName string
	VPCID       string
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
		// Ignore not found error; CNINode will be re-created on node reconcile if the node is not terminated
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

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

		fmt.Println("patching CNINode to add tags")
		if err := r.Client.Patch(ctx, cniNodeCopy, client.MergeFrom(cniNode)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch CNINode %v", err)
		}

		// Re-fetch the CNINode after updating the Tag so we have the latest state of the CRD before updating it again in later operations
		if err := r.Client.Get(ctx, req.NamespacedName, cniNode); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Add the finalizer if it does not exist, so we can define cleanup routine which should be run before CNINode can be deleted
	if !controllerutil.ContainsFinalizer(cniNode, config.NodeTerminationFinalizer) {
		r.Log.Info("adding finalizer for CNINode", "cniNode", cniNode.Name)
		if ok := controllerutil.AddFinalizer(cniNode, config.NodeTerminationFinalizer); !ok {
			r.Log.Info("failed to add finalizer for CNINode, requeuing the request", "cniNode", cniNode.Name)
			return ctrl.Result{Requeue: true}, nil
		}
		if err := r.Client.Update(ctx, cniNode); err != nil {
			// Requeue request so update can be retried
			return ctrl.Result{}, err
		}
	}

	if cniNode.GetDeletionTimestamp() != nil {
		// CNINode is marked for deletion
		if controllerutil.ContainsFinalizer(cniNode, config.NodeTerminationFinalizer) {
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
						return ctrl.Result{}, err
					}
					// Remove finalizer and update CNINode to continue deletion
					if ok := controllerutil.RemoveFinalizer(cniNode, config.NodeTerminationFinalizer); !ok {
						r.Log.Info("failed to remove finalizer for CNINode, requeuing the request", "cniNode", cniNode.Name)
						return ctrl.Result{Requeue: true}, nil
					}
					if err := r.Client.Update(ctx, cniNode); err != nil {
						// Return err so update can be retried
						return ctrl.Result{}, err
					}
				} else {
					r.Log.Info("failed to get the node object in CNINode reconciliation, will retry")
					// Requeue request so it can be retried
					return ctrl.Result{}, err
				}
			} else {
				// node exists, do not run the cleanup routine(periodic cleanup routine will anyway delete leaked ENIs), remove the finalizer
				// and let object be deleted and re-create similar object

				// Create a copy without deletion timestamp for creation
				newCNINode := &v1alpha1.CNINode{
					ObjectMeta: metav1.ObjectMeta{
						Name:            cniNode.Name,
						Namespace:       "",
						OwnerReferences: cniNode.OwnerReferences,
						// TODO: should we include finalizers at object creation or let finalizer add it on Create event?
						Finalizers: cniNode.Finalizers,
					},
					Spec: cniNode.Spec,
				}
				if ok := controllerutil.RemoveFinalizer(cniNode, config.NodeTerminationFinalizer); !ok {
					r.Log.Info("failed to remove finalizer for CNINode, requeuing the request", "cniNode", cniNode.Name)
					return ctrl.Result{Requeue: true}, nil
				}
				if err := r.Client.Update(ctx, cniNode); err != nil {
					// Return err so update can be retried
					return ctrl.Result{}, err
				}

				// Re-fetch CNINode to verify it is deleted before re-creating it as we use the same name
				// TODO: do we need to wait till CNINode deletion here?
				if err := r.Client.Get(ctx, req.NamespacedName, cniNode); err != nil && errors.IsNotFound(err) {
					if err = retry.OnError(retry.DefaultBackoff, func(error) bool {
						if err != nil {
							return true
						}
						return false
					}, func() error {
						r.Log.Info("creating CNINode after it has been deleted as node still exists", "cniNode", newCNINode.Name)
						return r.Client.Create(ctx, newCNINode)
					}); err != nil {
						// The CNINode is deleted at this point, we cannot re-create the same object
						// raise node object to publish warning that CNINode is deleted
						utils.SendNodeEventWithNodeName(r.K8sAPI, node.Name, utils.CNINodeDeleted,
							fmt.Sprint("CNINode was deleted and could not be re-created by controller"), v1.EventTypeWarning, r.Log)

					}
					// Requeue request to ensure the state of the object
					return ctrl.Result{Requeue: true}, nil
				}
			}
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CNINodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.CNINode{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: config.MaxNodeConcurrentReconciles}).
		Complete(r)
}
