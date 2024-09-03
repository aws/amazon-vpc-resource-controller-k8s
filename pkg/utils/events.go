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

package utils

import (
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	UnsupportedInstanceTypeReason       = "Unsupported"
	InsufficientCidrBlocksReason        = "InsufficientCidrBlocks"
	CNINodeCreatedReason                = "CNINodeCreation"
	NodeTrunkInitiatedReason            = "NodeTrunkInitiated"
	NodeTrunkFailedInitializationReason = "NodeTrunkFailedInit"
	EniConfigNameNotFoundReason         = "EniConfigNameNotFound"
	VersionNotice                       = "ControllerVersionNotice"
	BranchENICoolDownUpdateReason       = "BranchENICoolDownPeriodUpdated"
)

func SendNodeEventWithNodeName(client k8s.K8sWrapper, nodeName, reason, msg, eventType string, logger logr.Logger) {
	if node, err := client.GetNode(nodeName); err == nil {
		// set UID to node name for kubelet filter the event to node description
		node.SetUID(types.UID(nodeName))
		client.BroadcastEvent(node, reason, msg, eventType)
	} else {
		logger.Error(err, "had an error to get the node for sending unsupported event", "Node", nodeName)
	}
}

func SendNodeEventWithNodeObject(client k8s.K8sWrapper, node *v1.Node, reason, msg, eventType string, logger logr.Logger) {
	client.BroadcastEvent(node, reason, msg, eventType)
}

func SendBroadcastNodeEvent(client k8s.K8sWrapper, reason, msg, eventType string, logger logr.Logger) {
	if nodeList, err := client.ListNodes(); err == nil {
		for _, node := range nodeList.Items {
			node := node // Fix gosec G601, so we can use &node
			client.BroadcastEvent(&node, reason, msg, eventType)
		}
	} else {
		logger.Info("failed to list nodes when broadcasting node event", "Reason", reason, "Message", msg)
	}
}
