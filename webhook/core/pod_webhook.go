package core

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	vpcresourceconfig "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	webhookutils "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
)

const resourceLimit = "1"

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=fail,groups="",resources=pods,verbs=create,versions=v1,name=mpod.vpc.k8s.aws

// PodResourceInjector injects resources into Pods
type PodResourceInjector struct {
	Client      client.Client
	decoder     *admission.Decoder
	CacheHelper *webhookutils.K8sCacheHelper
	Log         logr.Logger
}

// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=serviceaccounts/status,verbs=get

func (prj *PodResourceInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := prj.decoder.Decode(req, pod)
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

	webhookLog := prj.Log.WithValues("Pod name", pod.Name, "Pod namespace", pod.Namespace)

	// Attach private ip to Windows pod which is not running on Host Network.
	// Attach ENI to non-Windows pod which is not running on Host Network.
	if shouldInjectPrivateIP(pod) {
		webhookLog.Info("Injecting resource to the first container of the pod",
			"resource name", vpcresourceconfig.ResourceNameIPAddress, "resource count", resourceLimit)
		pod.Spec.Containers[0].Resources.Limits[vpcresourceconfig.ResourceNameIPAddress] = resource.MustParse(resourceLimit)
		pod.Spec.Containers[0].Resources.Requests[vpcresourceconfig.ResourceNameIPAddress] = resource.MustParse(resourceLimit)
	} else if sgList, cacheErr := prj.CacheHelper.GetPodSecurityGroups(pod); cacheErr != nil {
		webhookLog.Error(cacheErr, "Webhook client failed to Get or List objects from cache.")
		return admission.Denied("Webhood encountered error to Get or List object from k8s cache.")
	} else if len(sgList) > 0 {
		webhookLog.Info("Injecting resource to the first container of the pod",
			"resource name", vpcresourceconfig.ResourceNamePodENI, "resource count", resourceLimit)
		pod.Spec.Containers[0].Resources.Limits[vpcresourceconfig.ResourceNamePodENI] = resource.MustParse(resourceLimit)
		pod.Spec.Containers[0].Resources.Requests[vpcresourceconfig.ResourceNamePodENI] = resource.MustParse(resourceLimit)
	} else {
		return admission.Allowed("Pod will not be injected with resources limits.")
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		webhookLog.Error(err, "Marshalling pod failed:")
		return admission.Errored(http.StatusInternalServerError, err)
	}
	webhookLog.Info("Mutating Pod finished.",
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
	// Referring to https://t.corp.amazon.com/V167778691
	return false
}

func containerHasCustomizedLimit(pod *corev1.Pod) bool {
	// TODO: implement container limits user input
	// Referring to https://sim.amazon.com/issues/EKS-NW-424
	return false
}

// PodResourceInjector implements admission.DecoderInjector.
// A decoder will be automatically injected.

// InjectDecoder injects the decoder.
func (prj *PodResourceInjector) InjectDecoder(d *admission.Decoder) error {
	prj.decoder = d
	return nil
}
