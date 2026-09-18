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

package node

import (
	"context"
	"fmt"

	cninode "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

// GetNodeAndWaitTillCapacityPresent waits until a non-empty set of nodes is
// non-deleting, Ready, and advertises positive expectedResource capacity, then
// returns that ready set.
func GetNodeAndWaitTillCapacityPresent(manager Manager, os string, expectedResource string) *v1.NodeList {
	readyNodeList := &v1.NodeList{}
	err := wait.PollUntilContextTimeout(context.Background(), utils.PollIntervalShort, utils.ResourceOperationTimeout, true,
		func(ctx context.Context) (bool, error) {
			By("checking nodes have capacity present")
			observedNodeList, err := manager.GetNodesWithOS(os)
			Expect(err).ToNot(HaveOccurred())
			ready := readyNodesWithResource(observedNodeList, expectedResource)
			if len(observedNodeList.Items) == 0 || len(ready.Items) != len(observedNodeList.Items) {
				return false, nil
			}
			readyNodeList = ready
			return true, nil
		})
	Expect(err).ToNot(HaveOccurred())
	return readyNodeList
}

// readyNodesWithResource returns non-deleting, Ready nodes advertising a positive
// quantity of the given allocatable resource.
func readyNodesWithResource(nodes *v1.NodeList, resource string) *v1.NodeList {
	ready := &v1.NodeList{}
	for i := range nodes.Items {
		node := nodes.Items[i]
		if node.DeletionTimestamp != nil || !isNodeReady(&node) {
			continue
		}
		if q, ok := node.Status.Allocatable[v1.ResourceName(resource)]; !ok || q.CmpInt64(0) <= 0 {
			continue
		}
		ready.Items = append(ready.Items, node)
	}
	return ready
}

func isNodeReady(node *v1.Node) bool {
	for _, c := range node.Status.Conditions {
		if c.Type == v1.NodeReady {
			return c.Status == v1.ConditionTrue
		}
	}
	return false
}

// VerifyCNINode polls for an exact 1:1 name mapping between Node and CNINode objects.
func VerifyCNINode(manager Manager) error {
	By("checking CNINode set matches node set")
	err := wait.PollUntilContextTimeout(context.Background(), utils.PollIntervalShort, utils.PollTimeout, true,
		func(ctx context.Context) (bool, error) {
			cniNodeList, err := manager.GetCNINodeList()
			if err != nil {
				return false, nil
			}
			nodeList, err := manager.GetNodeList()
			if err != nil {
				return false, nil
			}
			nodeNames := lo.SliceToMap(nodeList.Items, func(n v1.Node) (string, struct{}) {
				return n.Name, struct{}{}
			})
			cniNodeNames := lo.SliceToMap(cniNodeList.Items, func(c cninode.CNINode) (string, struct{}) {
				return c.Name, struct{}{}
			})
			if len(nodeNames) != len(cniNodeNames) {
				return false, nil
			}
			for name := range nodeNames {
				if _, ok := cniNodeNames[name]; !ok {
					return false, nil
				}
			}
			return true, nil
		})
	if err != nil {
		return fmt.Errorf("CNINode set does not match node set")
	}
	return nil
}
