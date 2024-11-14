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

package perpodsg_test

import (
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/node"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	verifier "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/verify"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

var frameWork *framework.Framework
var verify *verifier.PodVerification
var securityGroupID1 string
var securityGroupID2 string
var ctx context.Context
var err error
var nodeList *v1.NodeList

func TestPerPodGG(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Per Pod Security Group Suite")
}

var _ = BeforeSuite(func() {
	By("creating a framework")
	frameWork = framework.New(framework.GlobalOptions)
	ctx = context.Background()
	verify = verifier.NewPodVerification(frameWork, ctx)

	securityGroupID1, err = frameWork.EC2Manager.ReCreateSG(utils.ResourceNamePrefix+"sg-1", ctx)
	Expect(err).ToNot(HaveOccurred())
	securityGroupID2, err = frameWork.EC2Manager.ReCreateSG(utils.ResourceNamePrefix+"sg-2", ctx)
	Expect(err).ToNot(HaveOccurred())

	nodeList = node.GetNodeAndWaitTillCapacityPresent(frameWork.NodeManager, "linux",
		config.ResourceNamePodENI)
	err = node.VerifyCNINode(frameWork.NodeManager)
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID1)).To(Succeed())
	Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID2)).To(Succeed())
})
