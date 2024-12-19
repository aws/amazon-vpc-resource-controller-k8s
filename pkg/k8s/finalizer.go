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

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type FinalizerManager interface {
	AddFinalizers(ctx context.Context, object client.Object, finalizers ...string) error
	RemoveFinalizers(ctx context.Context, object client.Object, finalizers ...string) error
}

func NewDefaultFinalizerManager(k8sClient client.Client, log logr.Logger) FinalizerManager {
	return &defaultFinalizerManager{
		k8sClient: k8sClient,
		log:       log,
	}
}

type defaultFinalizerManager struct {
	k8sClient client.Client
	log       logr.Logger
}

func (m *defaultFinalizerManager) AddFinalizers(ctx context.Context, obj client.Object, finalizers ...string) error {
	if err := m.k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return err
	}

	oldObj := obj.DeepCopyObject().(client.Object)
	needsUpdate := false
	for _, finalizer := range finalizers {
		if !controllerutil.ContainsFinalizer(obj, finalizer) {
			m.log.Info("adding finalizer", "object", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName(), "finalizer", finalizer)
			controllerutil.AddFinalizer(obj, finalizer)
			needsUpdate = true
		}
	}
	if !needsUpdate {
		return nil
	}
	return m.k8sClient.Patch(ctx, obj, client.MergeFromWithOptions(oldObj, client.MergeFromWithOptimisticLock{}))
}

func (m *defaultFinalizerManager) RemoveFinalizers(ctx context.Context, obj client.Object, finalizers ...string) error {
	if err := m.k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return err
	}

	oldObj := obj.DeepCopyObject().(client.Object)
	needsUpdate := false
	for _, finalizer := range finalizers {
		if controllerutil.ContainsFinalizer(obj, finalizer) {
			m.log.Info("removing finalizer", "object", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName(), "finalizer", finalizer)
			controllerutil.RemoveFinalizer(obj, finalizer)
			needsUpdate = true
		}
	}
	if !needsUpdate {
		return nil
	}
	return m.k8sClient.Patch(ctx, obj, client.MergeFromWithOptions(oldObj, client.MergeFromWithOptimisticLock{}))
}
