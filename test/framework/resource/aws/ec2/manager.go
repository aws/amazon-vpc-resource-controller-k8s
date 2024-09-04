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

func NewManager(ec2Client *ec2.EC2, vpcID string) *Manager {
	return &Manager{ec2Client: ec2Client, vpcID: vpcID}
}

type Manager struct {
	ec2Client *ec2.EC2
	vpcID     string
}

func (d *Manager) CreateSecurityGroup(groupName string) (string, error) {
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

func (d *Manager) GetInstanceDetails(instanceID string) (*ec2.Instance, error) {
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

func (d *Manager) AuthorizeSecurityGroupIngress(securityGroupID string, port int,
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

func (d *Manager) AuthorizeSecurityGroupEgress(securityGroupID string, port int,
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

func (d *Manager) RevokeSecurityGroupIngress(securityGroupID string, port int,
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

func (d *Manager) DeleteSecurityGroup(ctx context.Context, securityGroupID string) error {
	return wait.PollUntil(utils.PollIntervalShort, func() (done bool, err error) {
		if _, err = d.ec2Client.DeleteSecurityGroup(&ec2.DeleteSecurityGroupInput{GroupId: &securityGroupID}); err != nil {
			if err.(awserr.Error).Code() == "DependencyViolation" {
				return false, nil
			}
			return false, err
		}
		return true, nil

	}, ctx.Done())
}

func (d *Manager) GetENISecurityGroups(eniID string) ([]string, error) {
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

func (d *Manager) GetSecurityGroupID(securityGroupName string) (string, error) {
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

func (d *Manager) WaitTillTheENIIsDeleted(ctx context.Context, eniID string) error {
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

func (d *Manager) UnAssignSecondaryIPv4Address(instanceID string, secondaryIPv4Address []string) error {
	describeInstanceOutput, err := d.ec2Client.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("attachment.instance-id"),
				Values: aws.StringSlice([]string{instanceID}),
			},
		},
	})
	if err != nil {
		return err
	}
	if len(describeInstanceOutput.NetworkInterfaces) == 0 {
		return fmt.Errorf("no instnace found")
	}

	_, err = d.ec2Client.UnassignPrivateIpAddresses(&ec2.UnassignPrivateIpAddressesInput{
		NetworkInterfaceId: describeInstanceOutput.NetworkInterfaces[0].NetworkInterfaceId,
		PrivateIpAddresses: aws.StringSlice(secondaryIPv4Address),
	})
	return err
}

func (d *Manager) GetPrivateIPv4AddressAndPrefix(instanceID string) ([]string, []string, error) {
	describeNetworkInterfaceOutput, err := d.ec2Client.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("attachment.instance-id"),
				Values: aws.StringSlice([]string{instanceID}),
			},
		},
	})
	if err != nil {
		return nil, nil, err
	}
	if len(describeNetworkInterfaceOutput.NetworkInterfaces) == 0 {
		return nil, nil, fmt.Errorf("no instance found")
	}

	primaryENI := describeNetworkInterfaceOutput.NetworkInterfaces[0]

	var secondaryIPAddresses []string
	if len(primaryENI.PrivateIpAddresses) > 0 {
		for _, ip := range primaryENI.PrivateIpAddresses {
			if *ip.Primary != true {
				secondaryIPAddresses = append(secondaryIPAddresses, *ip.PrivateIpAddress)
			}
		}
	}

	var ipV4Prefixes []string
	if len(describeNetworkInterfaceOutput.NetworkInterfaces[0].Ipv4Prefixes) > 0 {
		for _, prefix := range primaryENI.Ipv4Prefixes {
			ipV4Prefixes = append(ipV4Prefixes, *prefix.Ipv4Prefix)
		}
	}

	return secondaryIPAddresses, ipV4Prefixes, err
}

func (d *Manager) CreateAndAttachNetworkInterface(subnetID, instanceID, instanceType string) (string, error) {
	createENIOp, err := d.ec2Client.CreateNetworkInterface(&ec2.CreateNetworkInterfaceInput{
		SubnetId:    aws.String(subnetID),
		Description: aws.String("VPC-Resource-Controller integration test ENI"),
	})
	if err != nil {
		return "", err
	}
	nwInterfaceID := *createENIOp.NetworkInterface.NetworkInterfaceId
	// for test just use the max index - 2 (as trunk maybe attached to max index)
	indexID := vpc.Limits[instanceType].NetworkCards[0].MaximumNetworkInterfaces - 2
	_, err = d.ec2Client.AttachNetworkInterface(&ec2.AttachNetworkInterfaceInput{
		InstanceId:         aws.String(instanceID),
		NetworkInterfaceId: aws.String(nwInterfaceID),
		DeviceIndex:        aws.Int64(indexID),
	})
	return nwInterfaceID, err
}

func (d *Manager) TerminateInstances(instanceID string) error {
	_, err := d.ec2Client.TerminateInstances(&ec2.TerminateInstancesInput{
		InstanceIds: []*string{&instanceID},
	})
	return err
}

func (d *Manager) DescribeNetworkInterface(nwInterfaceID string) error {
	_, err := d.ec2Client.DescribeNetworkInterfaces(&ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []*string{&nwInterfaceID},
	})
	return err
}
func (d *Manager) DeleteNetworkInterface(nwInterfaceID string) error {
	_, err := d.ec2Client.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: aws.String(nwInterfaceID),
	})
	return err
}
func (d *Manager) ReCreateSG(securityGroupName string, ctx context.Context) (string, error) {
	groupID, err := d.GetSecurityGroupID(securityGroupName)
	// If the security group already exists, no error will be returned
	// We need to delete the security Group in this case so ingres/egress
	// rules from last run don't interfere with the current test
	if err == nil {
		if err = d.DeleteSecurityGroup(ctx, groupID); err != nil {
			return "", err
		}
	}
	// If error is not nil, then the Security Group doesn't exists, we need
	// to create new rule
	if groupID, err = d.CreateSecurityGroup(securityGroupName); err != nil {
		return "", err
	}
	return groupID, nil
}
