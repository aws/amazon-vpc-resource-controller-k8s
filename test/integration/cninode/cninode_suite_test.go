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

package cninode_test

import (
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCNINode(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CNINode Test Suite")
}

var frameWork *framework.Framework
var _ = BeforeSuite(func() {
	By("creating a framework")
	frameWork = framework.New(framework.GlobalOptions)

	By("verify CNINode count")
	err := node.VerifyCNINodeCount(frameWork.NodeManager)
	Expect(err).ToNot(HaveOccurred())
})

// Verify CNINode count before and after test remains same
var _ = AfterSuite(func() {
	By("verify CNINode count")
	err := node.VerifyCNINodeCount(frameWork.NodeManager)
	Expect(err).ToNot(HaveOccurred())
})
