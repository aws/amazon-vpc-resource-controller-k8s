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

package autoscaling

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

type Manager interface {
	DescribeAutoScalingGroup(ctx context.Context, autoScalingGroupName string) ([]autoscalingtypes.AutoScalingGroup, error)
	UpdateAutoScalingGroup(ctx context.Context, asgName string, desiredSize *int32) error
	TerminateInstanceInAutoScalingGroup(ctx context.Context, instanceID string, shouldDecrementDesiredCapacity bool) error
}

type defaultManager struct {
	AutoScalingAPI *autoscaling.Client
}

func NewManager(cfg aws.Config) Manager {
	return &defaultManager{
		AutoScalingAPI: autoscaling.NewFromConfig(cfg),
	}
}

func (d defaultManager) DescribeAutoScalingGroup(
	ctx context.Context,
	autoScalingGroupName string,
) ([]autoscalingtypes.AutoScalingGroup, error) {
	describeAutoScalingGroupIp := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{autoScalingGroupName},
	}
	asg, err := d.AutoScalingAPI.DescribeAutoScalingGroups(ctx, describeAutoScalingGroupIp)
	if err != nil {
		return nil, err
	}
	if len(asg.AutoScalingGroups) == 0 {
		return nil, fmt.Errorf("failed to find asg %s", autoScalingGroupName)
	}

	return asg.AutoScalingGroups, nil
}

func (d defaultManager) UpdateAutoScalingGroup(
	ctx context.Context,
	asgName string,
	desiredSize *int32,
) error {
	updateASGInput := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String(asgName),
		DesiredCapacity:      desiredSize,
	}
	_, err := d.AutoScalingAPI.UpdateAutoScalingGroup(ctx, updateASGInput)
	return err
}

func (d defaultManager) TerminateInstanceInAutoScalingGroup(
	ctx context.Context,
	instanceID string,
	shouldDecrementDesiredCapacity bool,
) error {
	_, err := d.AutoScalingAPI.TerminateInstanceInAutoScalingGroup(
		ctx,
		&autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     aws.String(instanceID),
			ShouldDecrementDesiredCapacity: aws.Bool(shouldDecrementDesiredCapacity),
		},
	)
	return err
}
