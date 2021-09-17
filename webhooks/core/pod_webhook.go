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
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
)

const (
	DefaultResourceLimit         = "1"
	FargatePodSGAnnotationKey    = "fargate.amazonaws.com/pod-sg"
	FargatePodIdentifierLabelKey = "eks.amazonaws.com/fargate-profile"
)

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=ignore,groups="",resources=pods,verbs=create,versions=v1,name=mpod.vpc.k8s.aws,sideEffects=None,admissionReviewVersions=v1

// PodResourceInjector injects resources into Pods
type PodMutationWebHook struct {
	decoder *admission.Decoder
	SGPAPI  utils.SecurityGroupForPodsAPI
	Log     logr.Logger
}

type PodType string

var (
	Fargate        = PodType("Fargate")
	Windows        = PodType("Windows")
	Linux          = PodType("Linux")
	HostNetworking = PodType("HostNetworking")
)

func (i *PodMutationWebHook) Handle(_ context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	var response admission.Response
	err := i.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	log := i.Log.WithValues("namespace", pod.Namespace, "name", pod.Name)

	i.InitializeEmptyFields(req, pod)

	switch WhichPod(pod) {
	case HostNetworking:
		response = admission.Allowed("SGP not supported on Pod running on HostNetwork")
	case Fargate:
		response = i.HandleFargatePod(req, pod, log)
	case Linux:
		response = i.HandleLinuxPod(req, pod, log)
	case Windows:
		response = i.HandleWindowsPod(req, pod, log)
	default:
		response = admission.Allowed("No criteria met injecting resource limit to Pod")
	}

	return response
}

// WhichPod returns the PodType, each PodType will be handled differently
func WhichPod(pod *corev1.Pod) PodType {
	// Ignore pod that is scheduled on host network.
	if pod.Spec.HostNetwork {
		return HostNetworking
	}

	// This should be the condition before Linux Pods
	if _, ok := pod.ObjectMeta.Labels[FargatePodIdentifierLabelKey]; ok {
		return Fargate
	}
	// Windows Pod
	if hasWindowsNodeSelector(pod) {
		return Windows
	}
	return Linux
}

// HandleFargatePod mutates the Fargate Pod if the Pod Matches a SGP. This also acts like
// a validation WebHook by removing any existing Annotation on the Pod on Create Event.
func (i *PodMutationWebHook) HandleFargatePod(req admission.Request, pod *corev1.Pod,
	log logr.Logger) (response admission.Response) {
	sgList, err := i.SGPAPI.GetMatchingSecurityGroupForPods(pod)
	if err != nil {
		i.Log.Error(err, "failed to get matching SGP for Pods",
			"namespace", pod.Namespace, "name", pod.Name)
		return admission.Denied("Failed to get Matching SGP for Pods, rejecting event")
	}

	switch len(sgList) {
	case 0:
		// If Pod is created, with the annotation then this event should be rejected. Only
		// the controller is allowed to modify this key. This webhook blocks such Create
		// Events and the validation Webhook blocks all Update events on this key.
		if _, ok := pod.Annotations[FargatePodSGAnnotationKey]; ok {
			delete(pod.Annotations, FargatePodSGAnnotationKey)
			log.Info("Overriding pod-sg annotation added outside of mutating webhook",
				" Annotation", pod.Annotations)
			response = i.GetPatchResponse(req, pod, log)
		} else {
			// If there's no matching SG for the given Pod
			response = admission.Allowed("Fargate pod not matching any SGP")
		}
	default:
		// If more than 1 SG match for the Pod then add all matching SG to the Annotation
		pod.Annotations[FargatePodSGAnnotationKey] = strings.Join(sgList, ",")
		log.Info("annotating Fargate pod with matching security groups",
			"Annotations", pod.Annotations)
		response = i.GetPatchResponse(req, pod, log)
	}

	return response
}

// HandleWindowsPod mutates the Windows Pod by injecting a secondary IPv4 Address
// Limit to the Pod when the Windows IPAM feature is enabled via ConfigMap
func (i *PodMutationWebHook) HandleWindowsPod(req admission.Request, pod *corev1.Pod,
	log logr.Logger) (response admission.Response) {

	// TODO: Don't Process event if feature is disabled

	i.Log.Info("injecting resource to the first container of the pod",
		"resource name", config.ResourceNameIPAddress, "resource count", DefaultResourceLimit)
	pod.Spec.Containers[0].
		Resources.Limits[config.ResourceNameIPAddress] = resource.MustParse(DefaultResourceLimit)
	pod.Spec.Containers[0].
		Resources.Requests[config.ResourceNameIPAddress] = resource.MustParse(DefaultResourceLimit)

	return i.GetPatchResponse(req, pod, log)
}

// HandleLinuxPod mutates the Linux Pod by injecting pod-eni limit if the Linux Pod
// matches any SGP
func (i *PodMutationWebHook) HandleLinuxPod(req admission.Request, pod *corev1.Pod,
	log logr.Logger) (response admission.Response) {

	sgList, err := i.SGPAPI.GetMatchingSecurityGroupForPods(pod)
	if err != nil {
		i.Log.Error(err, "failed to get matching SGP for Pods",
			"namespace", pod.Namespace, "name", pod.Name)
		return admission.Denied("Failed to get Matching SGP for Pods, rejecting event")
	}
	if len(sgList) == 0 {
		return admission.Allowed("Pod didn't match any SGP")
	}

	log.Info("injecting resource to the first container of the pod", "resource name",
		config.ResourceNamePodENI, "resource count", DefaultResourceLimit)

	pod.Spec.Containers[0].Resources.
		Limits[config.ResourceNamePodENI] = resource.MustParse(DefaultResourceLimit)
	pod.Spec.Containers[0].Resources.
		Requests[config.ResourceNamePodENI] = resource.MustParse(DefaultResourceLimit)

	return i.GetPatchResponse(req, pod, log)
}

// InitializeEmptyFields inits the empty fields in the request
func (i *PodMutationWebHook) InitializeEmptyFields(req admission.Request, pod *corev1.Pod) {
	if pod.Spec.Containers[0].Resources.Limits == nil {
		pod.Spec.Containers[0].Resources.Limits = make(corev1.ResourceList)
	}
	if pod.Spec.Containers[0].Resources.Requests == nil {
		pod.Spec.Containers[0].Resources.Requests = make(corev1.ResourceList)
	}
	// To avoid empty string namespace failing client retrieving service account later.
	if pod.Namespace == "" {
		pod.Namespace = req.Namespace
	}
	annotationMap := pod.ObjectMeta.Annotations
	if annotationMap == nil {
		annotationMap = make(map[string]string)
		pod.ObjectMeta.Annotations = annotationMap
	}
}

// Returns the Response by patching the updated object with raw object from request
func (i *PodMutationWebHook) GetPatchResponse(req admission.Request, pod *corev1.Pod, log logr.Logger) admission.Response {
	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		i.Log.Error(err, "failed to convert Pod to JSON")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	log.V(1).Info("mutated the pod with resource limit",
		"Injected Limits", pod.Spec.Containers[0].Resources.Limits)

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func hasWindowsNodeSelector(pod *corev1.Pod) bool {
	osLabel := pod.Spec.NodeSelector[config.NodeLabelOS]

	// Beta will be removed in v1.18.
	osLabelBeta := pod.Spec.NodeSelector[config.NodeLabelOSBeta]

	if osLabel != config.OSWindows && osLabelBeta != config.OSWindows {
		return false
	}
	return true
}

// InjectDecoder injects the decoder.
func (i *PodMutationWebHook) InjectDecoder(d *admission.Decoder) error {
	i.decoder = d
	return nil
}
