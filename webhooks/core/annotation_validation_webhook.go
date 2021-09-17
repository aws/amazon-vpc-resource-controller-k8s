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

package core

import (
	"context"
	"fmt"
	"net/http"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-v1-pod,mutating=false,failurePolicy=ignore,groups="",resources=pods,verbs=create;update,versions=v1,name=vpod.vpc.k8s.aws,sideEffects=None,admissionReviewVersions=v1

// AnnotationValidator injects resources into Pods
type AnnotationValidator struct {
	decoder *admission.Decoder
	Log     logr.Logger
}

const validUserInfo = "system:serviceaccount:kube-system:vpc-resource-controller"
const newValidUserInfo = "system:serviceaccount:kube-system:eks-vpc-resource-controller"

// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch

func (a *AnnotationValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	var response admission.Response

	a.Log.V(1).Info("annotation validating webhook request",
		"request", req)

	switch req.Operation {
	case admissionv1.Create:
		response = a.handleCreate(req)
	case admissionv1.Update:
		response = a.handleUpdate(req)
	default:
		response = admission.Allowed("")
	}

	a.Log.V(1).Info("annotation validating webhook response",
		"response", response)

	return response
}

func (a *AnnotationValidator) handleCreate(req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := a.decoder.DecodeRaw(req.Object, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	// The annotation is added by vpc-resource-controller which will come as an update event
	// so we should block all request on create event
	if val, ok := pod.Annotations[config.ResourceNamePodENI]; ok {
		a.Log.Info("blocking request", "event", "create",
			"annotation key", config.ResourceNamePodENI, "annotation value", val)
		return admission.Denied(
			fmt.Sprintf("pod cannot be created with %s annotation", config.ResourceNamePodENI))
	}
	return admission.Allowed("")
}

func (a *AnnotationValidator) handleUpdate(req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	if err := a.decoder.DecodeRaw(req.Object, pod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	oldPod := &corev1.Pod{}
	if err := a.decoder.DecodeRaw(req.OldObject, oldPod); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	logger := a.Log.WithValues("name", pod.Name, "namespace", pod.Namespace, "uid", pod.UID)

	// This will block any update on the specific annotation from non vpc resource controller
	// service accounts
	if pod.Annotations[config.ResourceNamePodENI] !=
		oldPod.Annotations[config.ResourceNamePodENI] {
		if (req.UserInfo.Username != validUserInfo) && (req.UserInfo.Username != newValidUserInfo) {
			logger.Info("denying annotation", "username", req.UserInfo.Username,
				"annotation key", config.ResourceNamePodENI)
			return admission.Denied("annotation is not set by vpc-resource-controller")
		}
		return admission.Allowed("")
	}

	if pod.Annotations[FargatePodSGAnnotationKey] !=
		oldPod.Annotations[FargatePodSGAnnotationKey] {
		logger.Info("denying annotation", "username", req.UserInfo.Username,
			"annotation key", FargatePodSGAnnotationKey)
		return admission.Denied("annotation is not set by mutating webhook")
	}

	return admission.Allowed("")
}

// InjectDecoder injects the decoder.
func (a *AnnotationValidator) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}
