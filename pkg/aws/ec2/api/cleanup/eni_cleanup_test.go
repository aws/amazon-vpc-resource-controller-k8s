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

package cleanup

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	mock_api "github.com/aws/amazon-vpc-resource-controller-k8s/mocks/amazon-vcp-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

var (
	mockClusterName       = "cluster-name"
	mockNodeID            = "i-00000000000000001"
	mockClusterNameTagKey = fmt.Sprintf(config.ClusterNameTagKeyFormat, mockClusterName)

	mockNetworkInterfaceId1 = "eni-000000000000000"
	mockNetworkInterfaceId2 = "eni-000000000000001"
	mockNetworkInterfaceId3 = "eni-000000000000002"

	mockVpcId = "vpc-0000000000000000"

	mockClusterTagInput = &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("status"),
				Values: []*string{aws.String(ec2.NetworkInterfaceStatusAvailable)},
			},
			{
				Name: aws.String("tag:" + config.NetworkInterfaceOwnerTagKey),
				Values: aws.StringSlice([]string{config.NetworkInterfaceOwnerTagValue,
					config.NetworkInterfaceOwnerVPCCNITagValue}),
			},
			{
				Name:   aws.String("vpc-id"),
				Values: []*string{aws.String(mockVpcId)},
			},
			{
				Name:   aws.String("tag:" + mockClusterNameTagKey),
				Values: []*string{aws.String(config.ClusterNameTagValue)},
			},
		},
	}

	mockNodeIDTagInput = &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("status"),
				Values: []*string{aws.String(ec2.NetworkInterfaceStatusAvailable)},
			},
			{
				Name: aws.String("tag:" + config.NetworkInterfaceOwnerTagKey),
				Values: aws.StringSlice([]string{config.NetworkInterfaceOwnerTagValue,
					config.NetworkInterfaceOwnerVPCCNITagValue}),
			},
			{
				Name:   aws.String("vpc-id"),
				Values: []*string{aws.String(mockVpcId)},
			},
			{
				Name:   aws.String("tag:" + config.NetworkInterfaceNodeIDKey),
				Values: []*string{aws.String(mockNodeID)},
			},
		},
	}

	NetworkInterfacesWith1And2 = []*ec2.NetworkInterface{
		{NetworkInterfaceId: &mockNetworkInterfaceId1, Attachment: &ec2.NetworkInterfaceAttachment{InstanceId: &mockNodeID}},
		{NetworkInterfaceId: &mockNetworkInterfaceId2, Attachment: &ec2.NetworkInterfaceAttachment{InstanceId: &mockNodeID}},
	}

	NetworkInterfacesWith1And3 = []*ec2.NetworkInterface{
		{NetworkInterfaceId: &mockNetworkInterfaceId1, Attachment: &ec2.NetworkInterfaceAttachment{InstanceId: &mockNodeID}},
		{NetworkInterfaceId: &mockNetworkInterfaceId3, Attachment: &ec2.NetworkInterfaceAttachment{InstanceId: &mockNodeID}},
	}
)

func getMockClusterENICleaner(ctrl *gomock.Controller) (*ClusterENICleaner, *mock_api.MockEC2Wrapper) {
	mockEC2Wrapper := mock_api.NewMockEC2Wrapper(ctrl)
	mockclusterENICleaner := ClusterENICleaner{
		availableENIs: map[string]struct{}{},
		ctx:           context.Background(),
		ClusterName:   mockClusterName,
	}
	mockclusterENICleaner.ENICleaner = &ENICleaner{
		EC2Wrapper: mockEC2Wrapper,
		Manager:    &mockclusterENICleaner,
		Log:        zap.New(zap.UseDevMode(true)).WithName("cluster eni cleaner test"),
		VpcId:      mockVpcId,
	}
	return &mockclusterENICleaner, mockEC2Wrapper
}

func TestENICleaner_DeleteLeakedResources(t *testing.T) {
	type fields struct {
		mockEC2Wrapper    *mock_api.MockEC2Wrapper
		clusterENICleaner *ClusterENICleaner
	}
	testENICleaner_DeleteLeakedResources := []struct {
		name             string
		getENICleaner    func() (*ENICleaner, *ClusterENICleaner)
		prepare          func(f *fields)
		assertFirstCall  func(f *fields)
		assertSecondCall func(f *fields)
	}{
		{
			name: "ClusterENICleaner, verifies leaked ENIs are deleted in the periodic cleanup routine and availableENI is updated",
			getENICleaner: func() (*ENICleaner, *ClusterENICleaner) {
				mockClusterENICleaner := &ClusterENICleaner{
					ClusterName:   mockClusterName,
					ctx:           context.Background(),
					availableENIs: map[string]struct{}{},
				}
				mockClusterENICleaner.ENICleaner = &ENICleaner{
					Manager: mockClusterENICleaner,
					VpcId:   mockVpcId,
					Log:     zap.New(zap.UseDevMode(true)).WithName("cluster eni cleaner test"),
				}
				return mockClusterENICleaner.ENICleaner, mockClusterENICleaner
			},
			prepare: func(f *fields) {
				gomock.InOrder(
					// Return network interface 1 and 2 in first cycle
					f.mockEC2Wrapper.EXPECT().DescribeNetworkInterfacesPagesWithRetry(mockClusterTagInput).
						Return(NetworkInterfacesWith1And2, nil),
					// Return network interface 1 and 3 in the second cycle
					f.mockEC2Wrapper.EXPECT().DescribeNetworkInterfacesPagesWithRetry(mockClusterTagInput).
						Return(NetworkInterfacesWith1And3, nil),
					// Expect to delete the network interface 1
					f.mockEC2Wrapper.EXPECT().DeleteNetworkInterface(
						&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: &mockNetworkInterfaceId1}).Return(nil, nil),
				)

			},
			assertFirstCall: func(f *fields) {
				// After first call, network interface 1 and 2 should be added to the map of available ENIs
				assert.True(t, reflect.DeepEqual(
					map[string]struct{}{mockNetworkInterfaceId1: {}, mockNetworkInterfaceId2: {}}, f.clusterENICleaner.availableENIs))

			},
			assertSecondCall: func(f *fields) {
				// After second call, network interface 1 should be deleted and network interface 3 added to list
				assert.True(t, reflect.DeepEqual(
					map[string]struct{}{mockNetworkInterfaceId3: {}}, f.clusterENICleaner.availableENIs))
			},
		},
		{
			name: "NodeTerminationENICleaner, verifies ENIs are deleted on node termination",
			getENICleaner: func() (*ENICleaner, *ClusterENICleaner) {
				mocknodeCleaner := &NodeTerminationCleaner{
					NodeID: mockNodeID,
				}
				mocknodeCleaner.ENICleaner = &ENICleaner{
					Manager: mocknodeCleaner,
					VpcId:   mockVpcId,
					Log:     zap.New(zap.UseDevMode(true)).WithName("cluster eni cleaner test"),
				}
				return mocknodeCleaner.ENICleaner, nil
			},
			prepare: func(f *fields) {
				gomock.InOrder(

					// Return network interface 1 and 2 in first cycle, expect to call delete on both
					f.mockEC2Wrapper.EXPECT().DescribeNetworkInterfacesPagesWithRetry(mockNodeIDTagInput).
						Return(NetworkInterfacesWith1And2, nil),
					f.mockEC2Wrapper.EXPECT().DeleteNetworkInterface(
						&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: &mockNetworkInterfaceId1}).Return(nil, nil),
					f.mockEC2Wrapper.EXPECT().DeleteNetworkInterface(
						&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: &mockNetworkInterfaceId2}).Return(nil, nil),
					// Return network interface 1 and 3 in the second cycle, again expect to call delete on both
					f.mockEC2Wrapper.EXPECT().DescribeNetworkInterfacesPagesWithRetry(mockNodeIDTagInput).
						Return(NetworkInterfacesWith1And3, nil),
					f.mockEC2Wrapper.EXPECT().DeleteNetworkInterface(
						&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: &mockNetworkInterfaceId1}).Return(nil, nil),
					f.mockEC2Wrapper.EXPECT().DeleteNetworkInterface(
						&ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: &mockNetworkInterfaceId3}).Return(nil, nil),
				)
			},
		},
	}

	for _, tt := range testENICleaner_DeleteLeakedResources {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		mockEC2Wrapper := mock_api.NewMockEC2Wrapper(ctrl)
		var mockENICleaner *ENICleaner
		var mockClusterENICleaner *ClusterENICleaner
		if tt.getENICleaner != nil {
			mockENICleaner, mockClusterENICleaner = tt.getENICleaner()
		}
		mockENICleaner.EC2Wrapper = mockEC2Wrapper
		f := fields{
			mockEC2Wrapper:    mockEC2Wrapper,
			clusterENICleaner: mockClusterENICleaner,
		}
		if tt.prepare != nil {
			tt.prepare(&f)
		}

		err := mockENICleaner.DeleteLeakedResources()
		assert.NoError(t, err)
		if tt.assertFirstCall != nil {
			tt.assertFirstCall(&f)
		}

		err = mockENICleaner.DeleteLeakedResources()
		assert.NoError(t, err)
		if tt.assertSecondCall != nil {
			tt.assertSecondCall(&f)
		}
	}
}

// TestENICleaner_StartENICleaner_Shutdown tests that ENICleaner would not start if shutdown is set to true.
func TestENICleaner_StartENICleaner_Shutdown(t *testing.T) {
	ctrl := gomock.NewController(t)
	eniCleaner, _ := getMockClusterENICleaner(ctrl)

	eniCleaner.shutdown = true

	eniCleaner.Start(context.TODO())
}
