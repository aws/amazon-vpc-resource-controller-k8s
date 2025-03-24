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
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// requires AmazonEKSVPCResourceController policy to be attached to the EKS cluster role
var _ = Describe("[LOCAL] Test IAM permissions for EC2 API calls", func() {
	var instanceID string
	var subnetID string
	var instanceType string
	var nwInterfaceID string
	var err error
	BeforeEach(func() {
		By("getting instance details")
		nodeList, err := frameWork.NodeManager.GetNodesWithOS(config.OSLinux)
		Expect(err).ToNot(HaveOccurred())
		Expect(nodeList.Items).ToNot(BeEmpty())
		instanceID = frameWork.NodeManager.GetInstanceID(&nodeList.Items[0])
		ec2Instance, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
		Expect(err).ToNot(HaveOccurred())
		subnetID = *ec2Instance.SubnetId
		instanceType = *ec2Instance.InstanceType

	})
	AfterEach(func() {
		By("deleting test interface")
		err = frameWork.EC2Manager.DeleteNetworkInterface(nwInterfaceID)
		Expect(err).ToNot(HaveOccurred())
	})
	Describe("Test DeleteNetworkInterface permission", func() {
		Context("when instance is terminated", func() {
			It("it should only delete ENIs provisioned by the controller or vpc-cni", func() {
				By("creating test ENI without eks:eni:owner tag and attach to EC2 instance")
				nwInterfaceID, err = frameWork.EC2Manager.CreateAndAttachNetworkInterface(subnetID, instanceID, instanceType)
				Expect(err).ToNot(HaveOccurred())
				By("terminating the instance and sleeping")
				err = frameWork.EC2Manager.TerminateInstances(instanceID)
				Expect(err).ToNot(HaveOccurred())
				// allow time for instance to be deleted and ENI to be available, new node to be ready
				time.Sleep(utils.ResourceCreationTimeout)
				By("verifying ENI is not deleted by controller")
				err = frameWork.EC2Manager.DescribeNetworkInterface(nwInterfaceID)
				Expect(err).ToNot(HaveOccurred())
			})
		})
	})
	Describe("Test CreateNetworkInterfacePermission permission", func() {
		var ec2Client *ec2.EC2
		var accountID string
		var wantErr bool
		JustBeforeEach(func() {
			arnSplit := strings.Split(frameWork.Options.ClusterRoleArn, ":")
			accountID = arnSplit[len(arnSplit)-2]
			By("assuming EKS cluster role")
			sess := session.Must(session.NewSession())
			creds := stscreds.NewCredentials(sess, frameWork.Options.ClusterRoleArn)
			ec2Client = ec2.New(sess, &aws.Config{Credentials: creds})
		})
		JustAfterEach(func() {
			By("creating network interface permission")
			_, err = ec2Client.CreateNetworkInterfacePermission(&ec2.CreateNetworkInterfacePermissionInput{
				AwsAccountId:       aws.String(accountID),
				NetworkInterfaceId: aws.String(nwInterfaceID),
				Permission:         aws.String(ec2.InterfacePermissionTypeInstanceAttach),
			})
			By("validating error is nil or as expected")
			Expect(err != nil).To(Equal(wantErr))
		})
		Context("CreateNetworkInterfacePermission on ENI WITH required tag eks:eni:owner=eks-vpc-resource-controller", func() {
			It("it should grant CreateNetworkInterfacePermission", func() {
				By("creating network interface")
				nwInterfaceOp, err := ec2Client.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
					SubnetId: aws.String(subnetID),
					TagSpecifications: []*ec2.TagSpecification{
						{
							ResourceType: aws.String(ec2.ResourceTypeNetworkInterface),
							Tags: []*ec2.Tag{
								{
									Key:   aws.String(config.NetworkInterfaceOwnerTagKey),
									Value: aws.String((config.NetworkInterfaceOwnerTagValue)),
								},
							},
						},
					},
					Description: aws.String("VPC-Resource-Controller integration test ENI"),
				})
				Expect(err).ToNot(HaveOccurred())
				nwInterfaceID = *nwInterfaceOp.NetworkInterface.NetworkInterfaceId
				wantErr = false
			})
		})
		Context("CreateNetworkInterfacePermission on ENI WITHOUT required tag eks:eni:owner=eks-vpc-resource-controller", func() {
			It("it should not grant CreateNetworkInterfacePermission", func() {
				By("creating network interface")
				nwInterfaceOp, err := ec2Client.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
					SubnetId:    aws.String(subnetID),
					Description: aws.String("VPC-Resource-Controller integration test ENI"),
				})
				Expect(err).ToNot(HaveOccurred())
				nwInterfaceID = *nwInterfaceOp.NetworkInterface.NetworkInterfaceId
				wantErr = true
			})
		})
	})
})
