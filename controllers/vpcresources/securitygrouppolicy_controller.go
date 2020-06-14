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

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	vpcresourcesv1beta1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1beta1"
)

// SecurityGroupPolicyReconciler reconciles a SecurityGroupPolicy object
type SecurityGroupPolicyReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=vpcresources.k8s.aws,resources=securitygrouppolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=vpcresources.k8s.aws,resources=securitygrouppolicies/status,verbs=get;update;patch

func (r *SecurityGroupPolicyReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	sgp := &vpcresourcesv1beta1.SecurityGroupPolicy{}

	if err := r.Client.Get(ctx, req.NamespacedName, sgp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger := r.Log.WithValues("securitygrouppolicy", req.NamespacedName)

	logger.Info("security group policy event received",
		"label selector", sgp.Spec.PodSelector,
		"service account selector", sgp.Spec.ServiceAccountSelector,
		"security groups", sgp.Spec.SecurityGroups,
	)

	// Cache Implementation logic

	return ctrl.Result{}, nil
}

func (r *SecurityGroupPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&vpcresourcesv1beta1.SecurityGroupPolicy{}).
		Complete(r)
}
