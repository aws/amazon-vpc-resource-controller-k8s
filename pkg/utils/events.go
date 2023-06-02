package utils

import (
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
)

var (
	UnsupportedInstanceTypeReason = "Unsupported"
)

func SendNodeEvent(client k8s.K8sWrapper, nodeName, reason, msg, eventType string, logger logr.Logger) {
	if node, err := client.GetNode(nodeName); err == nil {
		// set UID to node name for kubelet filter the event to node description
		node.SetUID(types.UID(nodeName))
		client.BroadcastEvent(node, reason, msg, eventType)
	} else {
		logger.Error(err, "had an error to get the node for sending unsupported event", "Node", nodeName)
	}
}
