/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ec2

import (
	"context"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/ec2"
	"k8s.io/apimachinery/pkg/util/wait"
)

type Manager interface {
	CreateSecurityGroup(groupName string) (string, error)
	AuthorizeSecurityGroupIngress(securityGroupID string, port int) error
	AuthorizeSecurityGroupEgress(securityGroupID string, port int) error
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

func (d *defaultManager) AuthorizeSecurityGroupIngress(securityGroupID string, port int) error {
	_, err := d.ec2Client.AuthorizeSecurityGroupIngress(&ec2.AuthorizeSecurityGroupIngressInput{
		CidrIp:     aws.String("0.0.0.0/0"),
		GroupId:    &securityGroupID,
		IpProtocol: aws.String("tcp"),
		FromPort:   aws.Int64(int64(port)),
		ToPort:     aws.Int64(int64(port)),
	})
	return err
}

func (d *defaultManager) AuthorizeSecurityGroupEgress(securityGroupID string, port int) error {
	_, err := d.ec2Client.AuthorizeSecurityGroupEgress(&ec2.AuthorizeSecurityGroupEgressInput{
		CidrIp:     aws.String("0.0.0.0/0"),
		GroupId:    &securityGroupID,
		IpProtocol: aws.String("tcp"),
		FromPort:   aws.Int64(int64(port)),
		ToPort:     aws.Int64(int64(port)),
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
