// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//	http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.
package ec2api_test

import (
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEc2api(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "EC2API Suite")
}

var frameWork *framework.Framework
var nodeListLen int
var _ = BeforeSuite(func() {
	By("creating a framework")
	frameWork = framework.New(framework.GlobalOptions)
	By("verify node count before test")
	nodeList, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
	Expect(err).ToNot(HaveOccurred())
	nodeListLen = len(nodeList.Items)
	Expect(nodeListLen).To(BeNumerically(">", 1))
})

var _ = AfterSuite(func() {
	nodeList, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
	Expect(err).ToNot(HaveOccurred())
	By("verifying node count after test is unchanged")
	Expect(len(nodeList.Items)).To(Equal(nodeListLen))
})
