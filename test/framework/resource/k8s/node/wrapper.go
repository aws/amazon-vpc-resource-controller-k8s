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

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func GetNodeAndWaitTillCapacityPresent(manager Manager, ctx context.Context, os string, expectedResource string) *v1.NodeList {

	observedNodeList := &v1.NodeList{}
	var err error
	err = wait.Poll(utils.PollIntervalShort, utils.ResourceCreationTimeout, func() (bool, error) {
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
