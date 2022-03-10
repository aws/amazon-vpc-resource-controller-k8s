package core

import (
	"context"
	"net/http"
	"reflect"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/condition"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type NodeUpdateWebhook struct {
	decoder   *admission.Decoder
	Condition condition.Conditions
	Log       logr.Logger
}

const awsNodeUsername = "system:serviceaccount:kube-system:aws-node"

// Handle allows update request on Node on the expected fields when the request is
// coming from the aws-node Service Account. It also ensures the updates are allowed only
// when the Security Group for Pod feature is enabled.
func (a *NodeUpdateWebhook) Handle(_ context.Context, req admission.Request) admission.Response {
	// Allow all requests that are not from aws-node username
	if req.UserInfo.Username != awsNodeUsername {
		return admission.Allowed("")
	}

	// Deny all requests if SGP feature is not enabled on aws-node
	if !a.Condition.IsPodSGPEnabled() {
		return admission.Denied("SGP is not enabled, node update from aws-node not allowed")
	}

	logger := a.Log.WithValues("node", req.Name)

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
