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

func GetNodeAndWaitTillCapacityPresent(manager Manager, os string, expectedResource string) *v1.NodeList {
	observedNodeList := &v1.NodeList{}
	var err error
	err = wait.PollUntilContextTimeout(context.Background(), utils.PollIntervalShort, utils.ResourceOperationTimeout, true,
		func(ctx context.Context) (bool, error) {
			By("checking nodes have capacity present")
			observedNodeList, err = manager.GetNodesWithOS(os)
			Expect(err).ToNot(HaveOccurred())
			for _, node := range observedNodeList.Items {
				_, found := node.Status.Allocatable[v1.ResourceName(expectedResource)]
				if !found {
					return false, nil
				}
			}
			return true, nil
		})
	Expect(err).ToNot(HaveOccurred())
	return observedNodeList
}

// VerifyCNINode checks if the number of CNINodes is equal to number of nodes in the cluster, and verifies 1:1 mapping between CNINode and Node objects
// Returns nil if count and 1:1 mapping exists, else returns error
func VerifyCNINode(manager Manager) error {
	var cniNodeList *cninode.CNINodeList
	var nodeList *v1.NodeList
	var err error
	By("checking number of CNINodes match number of nodes in the cluster")
	err = wait.PollUntilContextTimeout(context.Background(), utils.PollIntervalShort, utils.PollTimeout, true,
		func(ctx context.Context) (bool, error) {
			if cniNodeList, err = manager.GetCNINodeList(); err != nil {
				return false, nil
			}
			if nodeList, err = manager.GetNodeList(); err != nil {
				return false, nil
			}
			if len(nodeList.Items) != len(cniNodeList.Items) {
				return false, nil
			}
			return true, nil
		})
	if err != nil {
		return fmt.Errorf("number of CNINodes does not match number of nodes in the cluster")
	}
	By("checking CNINode list matches node list")
	nameMatched := true
	for _, node := range nodeList.Items {
		if !lo.ContainsBy(cniNodeList.Items, func(cniNode cninode.CNINode) bool {
			return cniNode.Name == node.Name
		}) {
			nameMatched = false
		}
	}
	if !nameMatched {
		return fmt.Errorf("CNINode list does not match node list")
	}
	return nil
}
