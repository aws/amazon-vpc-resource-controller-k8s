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

package ec2

import (
	"context"
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"
	"k8s.io/apimachinery/pkg/util/wait"
)

type Manager interface {
	CreateSecurityGroup(groupName string) (string, error)
	GetInstanceDetails(instanceID string) (*ec2.Instance, error)
	GetSecurityGroupID(securityGroupName string) (string, error)
	AuthorizeSecurityGroupIngress(securityGroupID string, port int, protocol string) error
	RevokeSecurityGroupIngress(securityGroupID string, port int, protocol string) error
	AuthorizeSecurityGroupEgress(securityGroupID string, port int, protocol string) error
	DeleteSecurityGroup(securityGroupID string) error
	GetENISecurityGroups(eniID string) ([]string, error)
	WaitTillTheENIIsDeleted(ctx context.Context, eniID string) error
}

func NewManager(ec2Client *ec2.EC2, vpcID string) Manager {
	return &defaultManager{ec2Client: ec2Client, vpcID: vpcID}
}

type defaultManager struct {
	ec2Client *ec2.EC2
	vpcID     string
}

func (d *defaultManager) CreateSecurityGroup(groupName string) (string, error) {
	createSecurityGroupOutput, err := d.ec2Client.CreateSecurityGroup(&ec2.CreateSecurityGroupInput{
		Description: &groupName,
		GroupName:   &groupName,
		VpcId:       &d.vpcID,
	})
	if err != nil {
		return "", err
	}

	return *createSecurityGroupOutput.GroupId, err
}

func (d *defaultManager) GetInstanceDetails(instanceID string) (*ec2.Instance, error) {
	describeInstanceOutput, err := d.ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{
		InstanceIds: aws.StringSlice([]string{instanceID}),
	})
	if err != nil {
		return nil, err
	}
	if describeInstanceOutput == nil || describeInstanceOutput.Reservations == nil ||
		len(describeInstanceOutput.Reservations) == 0 ||
		len(describeInstanceOutput.Reservations[0].Instances) == 0 {
		return nil, fmt.Errorf("couldn't find the instnace %s", instanceID)
	}
	return describeInstanceOutput.Reservations[0].Instances[0], nil
}

func (d *defaultManager) AuthorizeSecurityGroupIngress(securityGroupID string, port int,
	protocol string) error {

	_, err := d.ec2Client.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: &securityGroupID,
		IpPermissions: []*ec2.IpPermission{
			{
				FromPort:   aws.Int64(int64(port)),
				IpProtocol: aws.String(protocol),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
				ToPort:     aws.Int64(int64(port)),
			},
		},
	})
	return err
}

func (d *defaultManager) AuthorizeSecurityGroupEgress(securityGroupID string, port int,
	protocol string) error {
	_, err := d.ec2Client.AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{
		GroupId: &securityGroupID,
		IpPermissions: []*ec2.IpPermission{
			{
				FromPort:   aws.Int64(int64(port)),
				IpProtocol: aws.String(protocol),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
				ToPort:     aws.Int64(int64(port)),
			},
		},
	})
	return err
}

func (d *defaultManager) RevokeSecurityGroupIngress(securityGroupID string, port int,
	protocol string) error {
	_, err := d.ec2Client.RevokeSecurityGroupIngress(&ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String(securityGroupID),
		IpPermissions: []*ec2.IpPermission{
			{
				FromPort:   aws.Int64(int64(port)),
				IpProtocol: aws.String(protocol),
				IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
				ToPort:     aws.Int64(int64(port)),
			},
		},
	})
	return err
}

func (d *defaultManager) DeleteSecurityGroup(securityGroupID string) error {
	_, err := d.ec2Client.DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{
		GroupId: &securityGroupID,
	})
	return err
}

func (d *defaultManager) GetENISecurityGroups(eniID string) ([]string, error) {
	networkInterface, err := d.ec2Client.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{&eniID},
	})
	if err != nil {
		return nil, err
	}

	var securityGroups []string
	for _, groupIdentifier := range networkInterface.NetworkInterfaces[0].Groups {
		securityGroups = append(securityGroups, *groupIdentifier.GroupId)
	}

	return securityGroups, nil
}

func (d *defaultManager) GetSecurityGroupID(securityGroupName string) (string, error) {
	securityGroupOutput, err := d.ec2Client.DescribeSecurityGroups(&ec2.DescribeSecurityGroupsInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: aws.StringSlice([]string{d.vpcID}),
			},
			{
				Name:   aws.String("group-name"),
				Values: aws.StringSlice([]string{securityGroupName}),
			},
		},
	})
	if err != nil {
		return "", err
	}
	if securityGroupOutput == nil || securityGroupOutput.SecurityGroups == nil ||
		len(securityGroupOutput.SecurityGroups) == 0 {
		return "", fmt.Errorf("failed to find security group ID %s", securityGroupOutput)
	}

	return *securityGroupOutput.SecurityGroups[0].GroupId, nil
}

func (d *defaultManager) WaitTillTheENIIsDeleted(ctx context.Context, eniID string) error {
	return wait.PollImmediateUntil(utils.PollIntervalMedium, func() (done bool, err error) {
		_, err = d.ec2Client.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
			NetworkInterfaceIds: []*string{&eniID},
		})
		if err == nil {
			return false, nil
		}
		if err.(awserr.Error).Code() == "InvalidNetworkInterfaceID.NotFound" {
			return true, nil
		}
		return true, err

	}, ctx.Done())

}
