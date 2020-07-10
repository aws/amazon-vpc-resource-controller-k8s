package core

import (
	"context"
	"net/http"

	"k8s.io/apimachinery/pkg/types"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/go-logr/logr"
	"k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-v1-pod,mutating=false,failurePolicy=ignore,groups="",resources=pods,verbs=create;update,versions=v1,name=vpod.vpc.k8s.aws

// AnnotationValidator injects resources into Pods
type AnnotationValidator struct {
	Client  client.Client
	decoder *admission.Decoder
	Log     logr.Logger
}

const validUserInfo = "system:serviceaccount:kube-system:default"

// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=serviceaccounts/status,verbs=get

func (av *AnnotationValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := av.decoder.Decode(req, pod)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	webhookLog := av.Log.WithValues("Pod name", pod.Name, "Pod namespace", pod.Namespace)

	// Ignore pod that is scheduled on host network.
	if pod.Spec.HostNetwork {
		return admission.Allowed("Pod using host network will not have a pod ENI")
	}

	if podEniJSON, ok := pod.Annotations[config.ResourceNamePodENI]; ok {
		webhookLog.Info("Got annotation:", config.ResourceNamePodENI, podEniJSON)
		if req.Operation == v1beta1.Create {
			// Check who is setting the annotation
			if req.UserInfo.Username != validUserInfo {
				webhookLog.Info("Denying annotation creation", "Username", req.UserInfo.Username)
				return admission.Denied("Validation failed. Pod ENI not set by VPC Resource Controller")
			}
		} else if req.Operation == v1beta1.Update {
			// Check if the pod-eni annotation has been changed
			webhookLog.Info("Operation is update, checking that pod-eni annotation wasn't modified")
			oldPod := &corev1.Pod{}
			objectKey := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
			err := av.Client.Get(ctx, objectKey, oldPod)
			if err != nil {
				webhookLog.Error(err, "Failed to fetch pod in update request")
				return admission.Denied("Validation failed. Trying to update annotation on a pod that can't be found")
			}
			if oldPodAnnotationValue, ok := oldPod.Annotations[config.ResourceNamePodENI]; ok {
				// This is an update trying to change the pod annotation
				if oldPodAnnotationValue != podEniJSON && req.UserInfo.Username != validUserInfo {
					webhookLog.Info("Denying annotation change", "Username", req.UserInfo.Username)
					return admission.Denied("Validation failed. Pod ENI annotation changed outside of VPC Resource Controller")
				}
			} else {
				// This is an update trying to add the pod annotation
				if req.UserInfo.Username != validUserInfo {
					webhookLog.Info("Denying adding annotation", "Username", req.UserInfo.Username)
					return admission.Denied("Validation failed. Pod ENI annotation added outside of VPC Resource Controller")
				}
			}
		}
	}
	webhookLog.Info("Validating pod finished.")
	return admission.Allowed("Validation succeeded")
}

// PodResourceInjector implements admission.DecoderInjector.
// A decoder will be automatically injected.

// InjectDecoder injects the decoder.
func (av *AnnotationValidator) InjectDecoder(d *admission.Decoder) error {
	av.decoder = d
	return nil
}
