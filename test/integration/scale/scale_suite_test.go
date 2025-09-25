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

package scale_test

import (
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	verifier "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/verify"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var frameWork *framework.Framework
var verify *verifier.PodVerification
var ctx context.Context
var securityGroupID string
var err error
var namespace = "podsg-scale-" + utils.TestNameSpace

func TestScale(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Scale Test Suite")
}

var _ = BeforeSuite(func() {
	By("creating a framework")
	frameWork = framework.New(framework.GlobalOptions)
	ctx = context.Background()
	verify = verifier.NewPodVerification(frameWork, ctx)

	// create test namespace
	Expect(frameWork.NSManager.CreateNamespace(ctx, namespace)).To(Succeed())
	// create test security group
	securityGroupID, err = frameWork.EC2Manager.ReCreateSG(utils.ResourceNamePrefix+"sg", ctx)
	Expect(err).ToNot(HaveOccurred())
})

var _ = AfterSuite(func() {
	// delete test namespace
	Expect(frameWork.NSManager.DeleteAndWaitTillNamespaceDeleted(ctx, namespace)).To(Succeed())
	// delete test security group
	Expect(frameWork.EC2Manager.DeleteSecurityGroup(ctx, securityGroupID)).To(Succeed())
})
