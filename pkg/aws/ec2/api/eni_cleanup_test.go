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

package api

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	mockClusterName       = "cluster-name"
	mockClusterNameTagKey = fmt.Sprintf(config.ClusterNameTagKeyFormat, mockClusterName)

	mockNetworkInterfaceId1 = "eni-000000000000000"
	mockNetworkInterfaceId2 = "eni-000000000000001"
	mockNetworkInterfaceId3 = "eni-000000000000002"

	mockDescribeNetworkInterfaceIp = &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("status"),
				Values: []*string{aws.String(ec2.NetworkInterfaceStatusAvailable)},
			},
			{
				Name:   aws.String("tag:" + mockClusterNameTagKey),
				Values: []*string{aws.String(config.ClusterNameTagValue)},
			},
			{
				Name: aws.String("tag:" + config.NetworkInterfaceOwnerTagKey),
				Values: aws.StringSlice([]string{config.NetworkInterfaceOwnerTagValue,
					config.NetworkInterfaceOwnerVPCCNITagValue}),
			},
		},
	}
	mockDescribeInterfaceOpWith1And2 = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []*ec2.NetworkInterface{
			{NetworkInterfaceId: &mockNetworkInterfaceId1},
			{NetworkInterfaceId: &mockNetworkInterfaceId2},
		},
	}
	mockDescribeInterfaceOpWith1And3 = &ec2.DescribeNetworkInterfacesOutput{
		NetworkInterfaces: []*ec2.NetworkInterface{
			{NetworkInterfaceId: &mockNetworkInterfaceId1},
			{NetworkInterfaceId: &mockNetworkInterfaceId3},
		},
	}
)

func getMockENICleaner(ctrl *gomock.Controller) (*ENICleaner, *mock_api.MockEC2Wrapper) {
	mockEC2Wrapper := mock_api.NewMockEC2Wrapper(ctrl)
	return &ENICleaner{
		EC2Wrapper:        mockEC2Wrapper,
		availableENIs:     map[string]struct{}{},
		Log:               zap.New(zap.UseDevMode(true)),
		clusterNameTagKey: mockClusterNameTagKey,
		ctx:               context.Background(),
	}, mockEC2Wrapper
}

func TestENICleaner_cleanUpAvailableENIs(t *testing.T) {
	ctrl := gomock.NewController(t)
	eniCleaner, mockWrapper := getMockENICleaner(ctrl)

	gomock.InOrder(
		// Return network interface 1 and 2 in first cycle
		mockWrapper.EXPECT().DescribeNetworkInterfaces(mockDescribeNetworkInterfaceIp).
			Return(mockDescribeInterfaceOpWith1And2, nil),
		// Return network interface 1 and 3 in the second cycle
		mockWrapper.EXPECT().DescribeNetworkInterfaces(mockDescribeNetworkInterfaceIp).
			Return(mockDescribeInterfaceOpWith1And3, nil),
		// Expect to delete the network interface 1
		mockWrapper.EXPECT().DeleteNetworkInterface(
			&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: &mockNetworkInterfaceId1}).Return(nil, nil),
	)

	// Run 1st cycle, network interface 1 and 2 should be added to the map of available ENIs
	eniCleaner.cleanUpAvailableENIs()
	assert.True(t, reflect.DeepEqual(
		map[string]struct{}{mockNetworkInterfaceId1: {}, mockNetworkInterfaceId2: {}}, eniCleaner.availableENIs))

	// Run the second cycle, this time network interface 1 should be deleted and network interface 3 added to list
	eniCleaner.cleanUpAvailableENIs()
	assert.True(t, reflect.DeepEqual(
		map[string]struct{}{mockNetworkInterfaceId3: {}}, eniCleaner.availableENIs))
}

// TestENICleaner_StartENICleaner_Shutdown tests that ENICleaner would not start if shutdown is set to true.
func TestENICleaner_StartENICleaner_Shutdown(t *testing.T) {
	ctrl := gomock.NewController(t)
	eniCleaner, _ := getMockENICleaner(ctrl)

	eniCleaner.shutdown = true

	eniCleaner.Start(context.TODO())
}
