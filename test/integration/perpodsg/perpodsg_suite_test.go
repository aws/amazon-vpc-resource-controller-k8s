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
	"time"

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

	securityGroupID1 = reCreateSGIfAlreadyExists(utils.ResourceNamePrefix + "sg-1")
	securityGroupID2 = reCreateSGIfAlreadyExists(utils.ResourceNamePrefix + "sg-2")

	nodeList = node.GetNodeAndWaitTillCapacityPresent(frameWork.NodeManager, ctx, "linux",
		config.ResourceNamePodENI)
})

var _ = AfterSuite(func() {
	By("waiting for all the ENIs to be deleted after being cooled down")
	time.Sleep(time.Second * 90)

	err := frameWork.EC2Manager.DeleteSecurityGroup(securityGroupID1)
	Expect(err).ToNot(HaveOccurred())

	frameWork.EC2Manager.DeleteSecurityGroup(securityGroupID2)
	Expect(err).ToNot(HaveOccurred())
})

func reCreateSGIfAlreadyExists(securityGroupName string) string {
	groupID, err := frameWork.EC2Manager.GetSecurityGroupID(securityGroupName)
	// If the security group already exists, no error will be returned
	// We need to delete the security Group in this case so ingres/egress
	// rules from last run don't interfere with the current test
	if err == nil {
		By("deleting the older security group" + groupID)
		err = frameWork.EC2Manager.DeleteSecurityGroup(groupID)
		Expect(err).ToNot(HaveOccurred())
	}
	// If error is not nil, then the Security Group doesn't exists, we need
	// to create new rule
	By("creating a new security group with name " + securityGroupName)
	groupID, err = frameWork.EC2Manager.CreateSecurityGroup(securityGroupName)
	Expect(err).ToNot(HaveOccurred())

	return groupID
}
