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
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

type ENICleaner struct {
	EC2Wrapper  EC2Wrapper
	ClusterName string
	Log         logr.Logger

	availableENIs     map[string]struct{}
	shutdown          bool
	clusterNameTagKey string
	ctx               context.Context
}

func (e *ENICleaner) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	e.clusterNameTagKey = fmt.Sprintf(config.ClusterNameTagKeyFormat, e.ClusterName)
	e.availableENIs = make(map[string]struct{})
	e.ctx = ctx

	return mgr.Add(e)
}

// StartENICleaner starts the ENI Cleaner routine that cleans up dangling ENIs created by the controller
func (e *ENICleaner) Start(ctx context.Context) error {
	e.Log.Info("starting eni clean up routine")
	// Start routine to listen for shut down signal, on receiving the signal it set shutdown to true
	go func() {
		<-ctx.Done()
		e.shutdown = true
	}()
	// Perform ENI cleanup after fixed time intervals till shut down variable is set to true on receiving the shutdown
	// signal
	for !e.shutdown {
		e.cleanUpAvailableENIs()
		time.Sleep(config.ENICleanUpInterval)
	}

	return nil
}

// cleanUpAvailableENIs describes all the network interfaces in available status that are created by the controller,
// on seeing the a network interface for the first time, it is added to the map of available network interfaces, on
// seeing the network interface for the second time the network interface is deleted. This ensures that we are deleting
// the network interfaces that have been in available for upto the time interval between running the clean up routine.
// This prevents the clean up routine to remove ENIs that are created by another routines and are yet not attached to
// an instance or associated with a trunk interface
// Example,
// 1st cycle, Describe Available NetworkInterface Result - Interface 1, Interface 2, Interface 3
// 2nd cycle, Describe Available NetworkInterface Result - Interface 2, Interface 3
// In the second cycle we can conclude that Interface 2 and 3 are leaked because they have been sitting for the time
// interval between cycle 1 and 2 and hence can be safely deleted. And we can also conclude that Interface 1 was
// created but not attached at the the time when 1st cycle ran and hence it should not be deleted.
func (e *ENICleaner) cleanUpAvailableENIs() {
	describeNetworkInterfaceIp := &ec2.DescribeNetworkInterfacesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("status"),
				Values: []*string{aws.String(ec2.NetworkInterfaceStatusAvailable)},
			},
			{
				Name:   aws.String("tag:" + e.clusterNameTagKey),
				Values: []*string{aws.String(config.ClusterNameTagValue)},
			},
			{
				Name:   aws.String("tag:" + config.NetworkInterfaceOwnerTagKey),
				Values: []*string{aws.String(config.NetworkInterfaceOwnerTagValue)},
			},
		},
	}

	availableENIs := make(map[string]struct{})

	for {
		describeNetworkInterfaceOp, err := e.EC2Wrapper.DescribeNetworkInterfaces(describeNetworkInterfaceIp)
		if err != nil {
			e.Log.Error(err, "failed to describe network interfaces, will retry")
			return
		}

		for _, networkInterface := range describeNetworkInterfaceOp.NetworkInterfaces {
			if _, exists := e.availableENIs[*networkInterface.NetworkInterfaceId]; exists {
				// The ENI in available state has been sitting for at least the eni clean up interval and it should
				// be removed
				_, err := e.EC2Wrapper.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
					NetworkInterfaceId: networkInterface.NetworkInterfaceId,
				})
				if err != nil {
					// Log and continue, if the ENI is still present it will be cleaned up in next 2 cycles
					e.Log.Error(err, "failed to delete the dangling network interface",
						"id", *networkInterface.NetworkInterfaceId)
					continue
				}
				e.Log.Info("deleted dangling ENI successfully",
					"eni id", networkInterface.NetworkInterfaceId)
			} else {
				// Seeing the ENI for the first time, add it to the new list of available network interfaces
				availableENIs[*networkInterface.NetworkInterfaceId] = struct{}{}
				e.Log.V(1).Info("adding eni to to the map of available ENIs, will be removed if present in "+
					"next run too", "id", *networkInterface.NetworkInterfaceId)
			}
		}

		if describeNetworkInterfaceOp.NextToken == nil {
			break
		}

		describeNetworkInterfaceIp = &ec2.DescribeNetworkInterfacesInput{
			NextToken: describeNetworkInterfaceOp.NextToken,
		}
	}

	// Set the available ENIs to the list of ENIs seen in the current cycle
	e.availableENIs = availableENIs
}
