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

package utils

import (
	"context"
	"time"

	"github.com/awslabs/operatorpkg/reasonable"
	"github.com/awslabs/operatorpkg/singleton"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type WebhookManager struct {
	client *kubernetes.Clientset
	logger logr.Logger
}

const (
	mutatingWebhookName   = "vpc-resource-mutating-webhook"
	validatingWebhookName = "vpc-resource-validating-webhook"
)

// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingwebhookconfigurations,verbs=delete
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=delete

func NewWebhookManager(client *kubernetes.Clientset, logger logr.Logger) *WebhookManager {
	return &WebhookManager{
		client: client,
		logger: logger,
	}
}

func (m *WebhookManager) deleteMutatingWebhookFailurePolicy(ctx context.Context, name string) error {
	return m.client.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(ctx, name, v1.DeleteOptions{})
}

func (m *WebhookManager) deleteValidatingWebhookFailurePolicy(ctx context.Context, name string) error {
	return m.client.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(ctx, name, v1.DeleteOptions{})
}

func (m *WebhookManager) DeleteMutatingWebhookFailurePolicyWithRetry(ctx context.Context, name string) error {
	err := retry.OnError(retry.DefaultBackoff,
		func(err error) bool { return !errors.IsNotFound(err) },
		func() error { return m.deleteMutatingWebhookFailurePolicy(ctx, name) })
	return err
}

func (m *WebhookManager) DeleteValidatingWebhookFailurePolicyWithRetry(ctx context.Context, name string) error {
	err := retry.OnError(retry.DefaultBackoff,
		func(err error) bool { return !errors.IsNotFound(err) },
		func() error { return m.deleteValidatingWebhookFailurePolicy(ctx, name) })
	return err
}

func (m *WebhookManager) Reconcile(ctx context.Context) (reconcile.Result, error) {
	if err := client.IgnoreNotFound(
		m.client.AdmissionregistrationV1().MutatingWebhookConfigurations().Delete(ctx, mutatingWebhookName, v1.DeleteOptions{})); err != nil {
		m.logger.Info("removing webhook failed", "name", mutatingWebhookName, "error", err)
		return ctrl.Result{}, err
	}
	if err := client.IgnoreNotFound(
		m.client.AdmissionregistrationV1().ValidatingWebhookConfigurations().Delete(ctx, validatingWebhookName, v1.DeleteOptions{})); err != nil {
		m.logger.Info("removing webhook failed", "name", validatingWebhookName, "error", err)
		return ctrl.Result{}, err
	}
	return reconcile.Result{RequeueAfter: 1 * time.Hour}, nil
}

func (m *WebhookManager) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("webhook-cleaner").
		WithOptions(controller.Options{RateLimiter: reasonable.RateLimiter()}).
		WatchesRawSource(singleton.Source()).
		Complete(singleton.AsReconciler(m))
}
