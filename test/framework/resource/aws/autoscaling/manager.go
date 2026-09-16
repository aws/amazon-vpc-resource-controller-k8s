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
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

// ErrInstanceRefreshNotFound signals a not-yet-visible refresh (retryable),
// distinct from permanent API errors.
var ErrInstanceRefreshNotFound = errors.New("instance refresh not found")

type Manager interface {
	DescribeAutoScalingGroup(autoScalingGroupName string) ([]autoscalingtypes.AutoScalingGroup, error)
	UpdateAutoScalingGroup(asgName string, desiredSize, minSize, maxSize int32) error
	StartInstanceRefresh(ctx context.Context, asgName string, preferences *autoscalingtypes.RefreshPreferences) (string, error)
	DescribeInstanceRefresh(ctx context.Context, asgName string, instanceRefreshId string) (autoscalingtypes.InstanceRefresh, error)
	CancelInstanceRefresh(ctx context.Context, asgName string) error
}

type defaultManager struct {
	AutoScalingAPI *autoscaling.Client
}

func NewManager(cfg aws.Config) Manager {
	return &defaultManager{
		AutoScalingAPI: autoscaling.NewFromConfig(cfg),
	}
}

func (d defaultManager) DescribeAutoScalingGroup(autoScalingGroupName string) ([]autoscalingtypes.AutoScalingGroup, error) {
	describeAutoScalingGroupIp := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{autoScalingGroupName},
	}
	asg, err := d.AutoScalingAPI.DescribeAutoScalingGroups(context.TODO(), describeAutoScalingGroupIp)
	if err != nil {
		return nil, err
	}
	if len(asg.AutoScalingGroups) == 0 {
		return nil, fmt.Errorf("failed to find asg %s", autoScalingGroupName)
	}

	return asg.AutoScalingGroups, nil
}

func (d defaultManager) UpdateAutoScalingGroup(asgName string, desiredSize, minSize, maxSize int32) error {
	updateASGInput := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String(asgName),
		DesiredCapacity:      aws.Int32(desiredSize),
		MaxSize:              aws.Int32(maxSize),
		MinSize:              aws.Int32(minSize),
	}
	_, err := d.AutoScalingAPI.UpdateAutoScalingGroup(context.TODO(), updateASGInput)
	return err
}

func (d defaultManager) StartInstanceRefresh(ctx context.Context, asgName string, preferences *autoscalingtypes.RefreshPreferences) (string, error) {
	in := &autoscaling.StartInstanceRefreshInput{
		AutoScalingGroupName: aws.String(asgName),
		Preferences:          preferences,
	}
	out, err := d.AutoScalingAPI.StartInstanceRefresh(ctx, in)
	if err != nil {
		return "", err
	}
	return *out.InstanceRefreshId, nil
}

func (d defaultManager) DescribeInstanceRefresh(ctx context.Context, asgName, instanceRefreshId string) (autoscalingtypes.InstanceRefresh, error) {
	out, err := d.AutoScalingAPI.DescribeInstanceRefreshes(ctx, &autoscaling.DescribeInstanceRefreshesInput{
		AutoScalingGroupName: aws.String(asgName),
		InstanceRefreshIds:   []string{instanceRefreshId},
	})
	if err != nil {
		return autoscalingtypes.InstanceRefresh{}, err
	}
	if len(out.InstanceRefreshes) == 0 {
		return autoscalingtypes.InstanceRefresh{}, fmt.Errorf("%w: %s for asg %s", ErrInstanceRefreshNotFound, instanceRefreshId, asgName)
	}
	return out.InstanceRefreshes[0], nil
}

func (d defaultManager) CancelInstanceRefresh(ctx context.Context, asgName string) error {
	_, err := d.AutoScalingAPI.CancelInstanceRefresh(ctx, &autoscaling.CancelInstanceRefreshInput{
		AutoScalingGroupName: aws.String(asgName),
	})
	return err
}
