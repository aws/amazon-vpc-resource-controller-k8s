package core

import (
	"context"
	"net/http"
	"reflect"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type NodeUpdateWebhook struct {
	decoder   *admission.Decoder
	Condition condition.Conditions
	Log       logr.Logger
	Checker   healthz.Checker
	client    k8s.K8sWrapper
}

const (
	podNameKey      string = "authentication.kubernetes.io/pod-name"
	awsNodeUsername string = "system:serviceaccount:kube-system:aws-node"
)

func NewNodeUpdateWebhook(condition condition.Conditions, log logr.Logger, client k8s.K8sWrapper, healthzHandler *rcHealthz.HealthzHandler) *NodeUpdateWebhook {
	nodeUpdateWebhook := &NodeUpdateWebhook{
		Condition: condition,
		Log:       log,
		client:    client,
	}

	// add health check on subpath for node validation webhook
	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{
			"health-node-validating-webhook": rcHealthz.SimplePing("node validating webhook", log),
		},
	)

	return nodeUpdateWebhook
}

// +kubebuilder:webhook:path=/validate-v1-node,mutating=false,matchPolicy=Equivalent,failurePolicy=ignore,groups="",resources=nodes,verbs=update,versions=v1,name=vnode.vpc.k8s.aws,sideEffects=None,admissionReviewVersions=v1

// Handle allows update request on Node on the expected fields when the request is
// coming from the aws-node Service Account. It also ensures the updates are allowed only
// when the Security Group for Pod feature is enabled.
func (a *NodeUpdateWebhook) Handle(_ context.Context, req admission.Request) admission.Response {
	// Allow all requests that are not from aws-node username
	if req.UserInfo.Username != awsNodeUsername {
		return admission.Allowed("")
	}

	logger := a.Log.WithValues("node", req.Name)

	// if for some reason the Extra map doesn't have the key or the value length is 0, we can't deny the request since their existence is not guaranteed
	if len(req.UserInfo.Extra[podNameKey]) > 0 && !a.VPCCNIUpdateItsOwnHost(req.Name, req.UserInfo.Extra[podNameKey][0]) {
		logger.Info("The request came from a aws-node whose host does not match the requested host. The request will be denied.")
		return admission.Denied("Unmatched host from the aws-node pod")
	}

	logger.Info("update request received from aws-node")

	newNode := &corev1.Node{}
	if err := a.decoder.DecodeRaw(req.Object, newNode); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}
	oldNode := &corev1.Node{}
	if err := a.decoder.DecodeRaw(req.OldObject, oldNode); err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Remove the values that we expect the aws-node is supposed to modify
	delete(oldNode.Labels, config.HasTrunkAttachedLabel)
	delete(newNode.Labels, config.HasTrunkAttachedLabel)

	delete(oldNode.Labels, config.CustomNetworkingLabel)
	delete(newNode.Labels, config.CustomNetworkingLabel)

	// The new object has the ManagedFields which is missing from older object, so remove it as well
	oldNode.ManagedFields = nil
	newNode.ManagedFields = nil

	// Required for v1.18 clusters
	oldNode.SelfLink = ""
	newNode.SelfLink = ""

	// Deny request if there's any modification in the old and new object after removing the fields
	// added by aws-node
	if !reflect.DeepEqual(*newNode, *oldNode) {
		denyMessage := "aws-node can only update limited fields on the Node Object"
		// Keep log to Debug as it prints entire object
		logger.V(1).Info("request will be denied", "old object", *oldNode, "new object", *newNode)

		logger.Info(denyMessage)
		return admission.Denied(denyMessage)
	}

	// If all validation check succeed, allow admission
	return admission.Allowed("")
}

// InjectDecoder injects the decoder.
func (a *NodeUpdateWebhook) InjectDecoder(d *admission.Decoder) error {
	a.decoder = d
	return nil
}

// Check if the request from the Node Object hosting the requesting user
func (a *NodeUpdateWebhook) VPCCNIUpdateItsOwnHost(nodeName string, podName string) bool {
	var pod *corev1.Pod
	var err error

	retry.OnError(
		retry.DefaultBackoff,
		func(err error) bool {
			// we retry on some errors from API server side
			retriable := apierrors.IsTooManyRequests(err) ||
				apierrors.IsServerTimeout(err) ||
				apierrors.IsInternalError(err) ||
				apierrors.IsServiceUnavailable(err)
			return retriable
		},
		func() error {
			pod, err = a.client.GetPod(podName, config.KubeSystemNamespace)
			return err
		},
	)
	match := pod.Spec.NodeName == nodeName
	if !match {
		a.Log.Info("Node update webhook has unmatched requested node and aws-node host name", "RequestedNode", nodeName, "AwsNodeName", pod.Spec.NodeName)
	}
	return match
}
