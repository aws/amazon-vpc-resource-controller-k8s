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
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// NodeTerminationCleanerto handle resource cleanup at node termination
type NodeTerminationCleaner struct {
	NodeID string
	*ENICleaner
}

func (n *NodeTerminationCleaner) GetENITagFilters() []ec2types.Filter {
	return []ec2types.Filter{
		{
			Name:   aws.String("tag:" + config.NetworkInterfaceNodeIDKey),
			Values: []string{n.NodeID},
		},
	}
}

// Return true. As the node is terminating all available ENIs need to be deleted
func (n *NodeTerminationCleaner) ShouldDeleteENI(eniID *string) bool {
	return true
}

func (n *NodeTerminationCleaner) UpdateAvailableENIsIfNeeded(eniMap *map[string]struct{}) {
	// Nothing to do for the node termination cleaner
	return
}

// Updating node termination metrics does not make much sense as it will be updated on each node deletion and does not give us much info
func (n *NodeTerminationCleaner) UpdateCleanupMetrics(vpcrcAvailableCount *int, vpccniAvailableCount *int, leakedENICount *int) {
	return
}
