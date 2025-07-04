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
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/ec2/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	ec2Errors "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/go-logr/logr"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

// NetworkInterfaceManager interface allows to define the ENI filters and checks if ENI should be deleted for different callers like in the periodic cleanup routine or
// during node termination
type NetworkInterfaceManager interface {
	// If there are multiple filters then we will OR them.
	GetENITagFilters() []ec2types.Filter
	ShouldDeleteENI(eniID *string) bool
	UpdateAvailableENIsIfNeeded(eniMap *map[string]struct{})
	UpdateCleanupMetrics(vpcrcAvailableCount *int, vpccniAvailableCount *int, leakedENICount *int)
}

type ENICleaner struct {
	EC2Wrapper         api.EC2Wrapper
	Manager            NetworkInterfaceManager
	VpcId              string
	Log                logr.Logger
	ControllerDisabled bool
}

// common filters for describing network interfaces
var CommonNetworkInterfaceFilters = []ec2types.Filter{
	{
		Name:   aws.String("status"),
		Values: []string{string(ec2types.NetworkInterfaceStatusAvailable)},
	},
	{
		Name: aws.String("tag:" + config.NetworkInterfaceOwnerTagKey),
		Values: []string{
			config.NetworkInterfaceOwnerTagValue,
			config.NetworkInterfaceOwnerVPCCNITagValue,
		},
	},
}

// ClusterENICleaner periodically deletes leaked network interfaces(provisioned by the controller or VPC-CNI) in the cluster
type ClusterENICleaner struct {
	ClusterName   string
	shutdown      bool
	ctx           context.Context
	availableENIs map[string]struct{}
	*ENICleaner
}

func (e *ClusterENICleaner) SetupWithManager(ctx context.Context, mgr ctrl.Manager, healthzHandler *rcHealthz.HealthzHandler) error {
	e.ctx = ctx
	e.availableENIs = make(map[string]struct{})
	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{
			"health-interface-cleaner": rcHealthz.SimplePing("interface cleanup", e.Log),
		},
	)

	return mgr.Add(e)
}

// StartENICleaner starts the ENI Cleaner routine that cleans up dangling ENIs created by the controller
func (e *ClusterENICleaner) Start(ctx context.Context) error {
	e.Log.Info("starting eni clean up routine")

	go func() {
		<-ctx.Done()
		e.shutdown = true
	}()
	// Perform ENI cleanup after fixed time intervals till shut down variable is set to true on receiving the shutdown
	// signal
	for !e.shutdown {
		e.DeleteLeakedResources(ctx)
		time.Sleep(config.ENICleanUpInterval)
	}

	return nil
}

// DeleteLeakedResources describes all the network interfaces in available status that are created by the controller or VPC-CNI
// This is called by periodically by ClusterENICleaner which deletes available ENIs cluster-wide, and by the NodeTermination cleaner on node termination
// The available ENIs are deleted if ShouldDeleteENI is true, defined in the respective cleaners
// The function also updates metrics for the periodic cleanup routine and the node termination cleanup
func (e *ENICleaner) DeleteLeakedResources(ctx context.Context) error {
	var errors []error
	availableENIs := make(map[string]struct{})
	vpcrcAvailableCount := 0
	vpccniAvailableCount := 0
	leakedENICount := 0
	defer e.Manager.UpdateCleanupMetrics(&vpcrcAvailableCount, &vpccniAvailableCount, &leakedENICount)

	filters := append(CommonNetworkInterfaceFilters, []ec2types.Filter{
		{
			Name:   aws.String("vpc-id"),
			Values: []string{e.VpcId},
		},
	}...)

	// only apply extra filters when the controller is enabled which provides cninode resources
	var OrFilters []ec2types.Filter
	var err error
	var networkInterfaces []*ec2types.NetworkInterface
	if !e.ControllerDisabled {
		// get cleaner specific filters
		OrFilters = e.Manager.GetENITagFilters()
		for _, OrFilter := range OrFilters {
			filterCopy := append([]ec2types.Filter{}, filters...)
			filterCopy = append(filterCopy, OrFilter)

			describeNetworkInterfaceIp := &ec2.DescribeNetworkInterfacesInput{
				Filters: filterCopy,
			}

			tempNetworkInterfaces, err := e.EC2Wrapper.DescribeNetworkInterfacesPages(ctx, describeNetworkInterfaceIp)
			if err != nil {
				e.Log.Error(err, "failed to describe network interfaces, cleanup will be retried in next cycle")
				return err
			}
			networkInterfaces = append(networkInterfaces, tempNetworkInterfaces...)
		}
	} else {
		describeNetworkInterfaceIp := &ec2.DescribeNetworkInterfacesInput{
			Filters: filters,
		}
		networkInterfaces, err = e.EC2Wrapper.DescribeNetworkInterfacesPages(ctx, describeNetworkInterfaceIp)
		if err != nil {
			e.Log.Error(err, "failed to describe network interfaces, cleanup will be retried in next cycle")
			return err
		}
	}

	for _, nwInterface := range networkInterfaces {
		if e.Manager.ShouldDeleteENI(nwInterface.NetworkInterfaceId) {
			tagMap := utils.GetTagKeyValueMap(nwInterface.TagSet)
			if val, ok := tagMap[config.NetworkInterfaceOwnerTagKey]; ok {
				// Increment promethues metrics for number of leaked ENIs cleaned up
				switch val {
				case config.NetworkInterfaceOwnerTagValue:
					vpcrcAvailableCount += 1
				case config.NetworkInterfaceOwnerVPCCNITagValue:
					vpccniAvailableCount += 1
				default:
					// We should not hit this case as we only filter for relevant tag values, log error and continue if unexpected ENIs found
					e.Log.Error(fmt.Errorf("found available ENI not created by VPC-CNI/VPC-RC"), "eniID", *nwInterface.NetworkInterfaceId)
					continue
				}
			}
			_, err := e.EC2Wrapper.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
				NetworkInterfaceId: nwInterface.NetworkInterfaceId,
			})
			if err != nil {
				if !strings.Contains(err.Error(), ec2Errors.NotFoundInterfaceID) { // ignore InvalidNetworkInterfaceID.NotFound error
					// append err and continue, we will retry deletion in the next period/reconcile
					leakedENICount += 1
					errors = append(errors, fmt.Errorf("failed to delete leaked network interface %v:%v", *nwInterface.NetworkInterfaceId, err))
					e.Log.Error(err, "failed to delete the leaked network interface",
						"id", *nwInterface.NetworkInterfaceId)
				}
				continue
			}
			// It is possible for eni attachment to be nil, if it was never attached to instance
			instanceID := ""
			if nwInterface.Attachment != nil && nwInterface.Attachment.InstanceId != nil {
				instanceID = aws.ToString(nwInterface.Attachment.InstanceId)
			}
			e.Log.Info("deleted leaked ENI successfully", "eni id", *nwInterface.NetworkInterfaceId, "instance id", instanceID)
		} else {
			// Seeing the ENI for the first time, add it to the new list of available network interfaces
			availableENIs[*nwInterface.NetworkInterfaceId] = struct{}{}
			e.Log.Info("adding eni to to the map of available ENIs, will be removed if present in "+
				"next run too", "id", *nwInterface.NetworkInterfaceId)
		}
	}
	e.Manager.UpdateAvailableENIsIfNeeded(&availableENIs)
	return kerrors.NewAggregate(errors)
}

func (e *ClusterENICleaner) GetENITagFilters() []ec2types.Filter {
	return []ec2types.Filter{
		{
			Name:   aws.String("tag:" + config.VPCCNIClusterNameKey),
			Values: []string{e.ClusterName},
		},
		{
			Name:   aws.String("tag:" + fmt.Sprintf(config.VPCRCClusterNameTagKeyFormat, e.ClusterName)),
			Values: []string{config.VPCRCClusterNameTagValue},
		},
	}
}

// ShouldDeleteENI returns true if the ENI should be deleted.
func (e *ClusterENICleaner) ShouldDeleteENI(eniID *string) bool {
	if _, exists := e.availableENIs[*eniID]; exists {
		return true
	}
	return false
}

// Set the available ENIs to the list of ENIs seen in the current cycle
// This adds ENIs that should not be deleted in the current cleanup cycle to the internal cache so it can be deleted in next cycle
// This prevents the clean up routine to remove ENIs that are created by another routines and are yet not attached to
// an instance or associated with a trunk interface in the periodic cleanup routine

// Example
// 1st cycle, Describe Available NetworkInterface Result - Interface 1, Interface 2, Interface 3
// 2nd cycle, Describe Available NetworkInterface Result - Interface 2, Interface 3
// In the second cycle we can conclude that Interface 2 and 3 are leaked because they have been sitting for the time
// interval between cycle 1 and 2 and hence can be safely deleted. And we can also conclude that Interface 1 was
// created but not attached at the the time when 1st cycle ran and hence it should not be deleted.
func (e *ClusterENICleaner) UpdateAvailableENIsIfNeeded(eniMap *map[string]struct{}) {
	e.availableENIs = *eniMap
}

// Update cluster cleanup metrics for the current cleanup cycle
func (e *ClusterENICleaner) UpdateCleanupMetrics(vpcrcAvailableCount *int, vpccniAvailableCount *int, leakedENICount *int) {
	api.VpcRcAvailableClusterENICnt.Set(float64(*vpcrcAvailableCount))
	api.VpcCniAvailableClusterENICnt.Set(float64(*vpccniAvailableCount))
	api.LeakedENIClusterCleanupCnt.Set(float64(*leakedENICount))
}
