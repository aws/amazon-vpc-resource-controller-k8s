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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	vpcresourceconfig "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
)

const resourceLimit = "1"
const fargatePodSgAnnotKey = "fargate.amazonaws.com/pod-sg"
const fargatePodLabel = "eks.amazonaws.com/fargate-profile"

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=ignore,groups="",resources=pods,verbs=create,versions=v1,name=mpod.vpc.k8s.aws

// PodResourceInjector injects resources into Pods
type PodResourceInjector struct {
	Client  client.Client
	decoder *admission.Decoder
	SGPAPI  utils.SecurityGroupForPodsAPI
	Log     logr.Logger
}

func (i *PodResourceInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := i.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Ignore pod that is scheduled on host network.
	if pod.Spec.HostNetwork {
		return admission.Allowed("Pod on HostNetwork will not be injected with resources.")
	}

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

	webhookLog := i.Log.WithValues("Pod name", pod.Name, "Pod namespace", pod.Namespace)

	// Attach private ip to Windows pod which is not running on Host Network.
	// Attach ENI to non-Windows pod which is not running on Host Network.

	isFargatePod := false
	if _, ok := pod.ObjectMeta.Labels[fargatePodLabel]; ok {
		isFargatePod = true
	}
	annotationMap := pod.ObjectMeta.Annotations
	if annotationMap == nil {
		annotationMap = make(map[string]string)
	}

	// TODO: Stop processing events if ENABLE_POD_ENI is set to true + No trunk Node
	if sgList, cacheErr := i.SGPAPI.GetMatchingSecurityGroupForPods(pod); cacheErr != nil {
		webhookLog.Error(cacheErr, "Webhook client failed to Get or List objects from cache.")
		return admission.Denied("Webhood encountered error to Get or List object from k8s cache.")
	} else if len(sgList) > 0 && !isFargatePod {
		webhookLog.Info("Injecting resource to the first container of the pod",
			"resource name", vpcresourceconfig.ResourceNamePodENI, "resource count", resourceLimit)
		pod.Spec.Containers[0].Resources.Limits[vpcresourceconfig.ResourceNamePodENI] = resource.MustParse(resourceLimit)
		pod.Spec.Containers[0].Resources.Requests[vpcresourceconfig.ResourceNamePodENI] = resource.MustParse(resourceLimit)
	} else if len(sgList) > 0 && isFargatePod {
		// add pod-sg annotation if there is any matching security group in defined SGP (CRD)
		annotationMap[fargatePodSgAnnotKey] = strings.Join(sgList, ",")
		pod.ObjectMeta.Annotations = annotationMap
		webhookLog.Info("Annotating fargate pod with security groups defined in CRD",
			"Pod Security Group Annotation", annotationMap)
	} else if len(sgList) == 0 && isFargatePod {
		// Mutating Webhook catches the CREATE event when pod-sg annotation is added before this component
		// Mutating Webhook will override pod-sg annotation if it is added before mutating webhook
		// Annotation Validation Webhook catches the UPDATE event where pod-sg annotation is modified by any resource
		if _, ok := pod.Annotations[fargatePodSgAnnotKey]; ok {
			delete(annotationMap, fargatePodSgAnnotKey)
			pod.ObjectMeta.Annotations = annotationMap
			webhookLog.Info("Overriding pod-sg annotation added outside of mutating webhook",
				"Pod Security Group Annotation", annotationMap)
		} else {
			return admission.Allowed("Fargate pod will not be annotated with security group.")
		}
	} else if shouldInjectPrivateIP(pod) {
		webhookLog.Info("injecting resource to the first container of the pod",
			"resource name", vpcresourceconfig.ResourceNameIPAddress, "resource count", resourceLimit)
		pod.Spec.Containers[0].Resources.Limits[vpcresourceconfig.ResourceNameIPAddress] = resource.MustParse(resourceLimit)
		pod.Spec.Containers[0].Resources.Requests[vpcresourceconfig.ResourceNameIPAddress] = resource.MustParse(resourceLimit)
	} else {
		return admission.Allowed("Pod will not be injected with resources limits.")
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		webhookLog.Error(err, "Marshalling pod failed:")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	webhookLog.V(1).Info("Mutating Pod finished.",
		"Resources Limits", pod.Spec.Containers[0].Resources.Limits)

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func shouldInjectPrivateIP(pod *corev1.Pod) bool {
	return hasWindowsNodeSelector(pod) || hasWindowsNodeAffinity(pod)
}

func hasWindowsNodeSelector(pod *corev1.Pod) bool {
	osLabel := pod.Spec.NodeSelector[vpcresourceconfig.NodeLabelOS]

	// Version Beta is going to be deprecated soon.
	osLabelBeta := pod.Spec.NodeSelector[vpcresourceconfig.NodeLabelOSBeta]

	if osLabel != vpcresourceconfig.OSWindows && osLabelBeta != vpcresourceconfig.OSWindows {
		return false
	}

	return true
}

func hasWindowsNodeAffinity(pod *corev1.Pod) bool {
	// TODO: implement node affinity for Windows pod
	return false
}

func containerHasCustomizedLimit(pod *corev1.Pod) bool {
	// TODO: implement container limits user input
	return false
}

// PodResourceInjector implements admission.DecoderInjector.
// A decoder will be automatically injected.

// InjectDecoder injects the decoder.
func (i *PodResourceInjector) InjectDecoder(d *admission.Decoder) error {
	i.decoder = d
	return nil
}
