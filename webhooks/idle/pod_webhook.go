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

package idle

import (
	"context"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,matchPolicy=Equivalent,failurePolicy=ignore,groups="",resources=pods,verbs=create,versions=v1,name=mpod.vpc.k8s.aws,sideEffects=None,admissionReviewVersions=v1

type PodMutationWebHook struct {
}

func NewPodMutationWebHook(log logr.Logger) *PodMutationWebHook {
	return &PodMutationWebHook{}
}

func (i *PodMutationWebHook) Handle(_ context.Context, req admission.Request) admission.Response {
	return admission.Allowed("the controller is disabled")
}

type NodeUpdateWebhook struct {
}

func NewNodeUpdateWebhook(log logr.Logger) *NodeUpdateWebhook {
	return &NodeUpdateWebhook{}
}

// +kubebuilder:webhook:path=/validate-v1-node,mutating=false,matchPolicy=Equivalent,failurePolicy=ignore,groups="",resources=nodes,verbs=update,versions=v1,name=vnode.vpc.k8s.aws,sideEffects=None,admissionReviewVersions=v1

func (a *NodeUpdateWebhook) Handle(_ context.Context, req admission.Request) admission.Response {
	return admission.Allowed("the controller is disabled")
}

// +kubebuilder:webhook:path=/validate-v1-pod,mutating=false,matchPolicy=Equivalent,failurePolicy=ignore,groups="",resources=pods,verbs=create;update,versions=v1,name=vpod.vpc.k8s.aws,sideEffects=None,admissionReviewVersions=v1

type AnnotationValidator struct {
}

func NewAnnotationValidator(log logr.Logger) *AnnotationValidator {
	return &AnnotationValidator{}
}

// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch

func (a *AnnotationValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	return admission.Allowed("the controller is disabled")
}
