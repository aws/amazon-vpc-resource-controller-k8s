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
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	rcHealthz "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/healthz"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/exp/slices"

	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	ec2Errors "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/aws/errors"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
)

type ENICleaner struct {
	EC2Wrapper  EC2Wrapper
	ClusterName string
	Log         logr.Logger
	VPCID       string

	availableENIs     map[string]struct{}
	shutdown          bool
	clusterNameTagKey string
	ctx               context.Context
}

var (
	vpccniAvailableENICnt = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vpc_cni_created_available_eni_count",
			Help: "The number of available ENIs created by VPC-CNI that controller will try to delete in each cleanup cycle",
		},
	)
	vpcrcAvailableENICnt = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vpc_rc_created_available_eni_count",
			Help: "The number of available ENIs created by VPC-RC that controller will try to delete in each cleanup cycle",
		},
	)
	leakedENICnt = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "leaked_eni_count",
			Help: "The number of available ENIs that failed to be deleted by the controller in each cleanup cycle",
		},
	)
)

func (e *ENICleaner) SetupWithManager(ctx context.Context, mgr ctrl.Manager, healthzHandler *rcHealthz.HealthzHandler) error {
	e.clusterNameTagKey = fmt.Sprintf(config.ClusterNameTagKeyFormat, e.ClusterName)
	e.availableENIs = make(map[string]struct{})
	e.ctx = ctx

	healthzHandler.AddControllersHealthCheckers(
		map[string]healthz.Checker{
			"health-interface-cleaner": rcHealthz.SimplePing("interface cleanup", e.Log),
		},
	)

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
	vpcrcAvailableCount := 0
	vpccniAvailableCount := 0
	leakedENICount := 0

	describeNetworkInterfaceIp := &ec2v2.DescribeNetworkInterfacesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("status"),
				Values: []string{ec2.NetworkInterfaceStatusAvailable},
			},
			{
				Name:   aws.String("tag:" + e.clusterNameTagKey),
				Values: []string{config.ClusterNameTagValue},
			},
			{
				Name: aws.String("tag:" + config.NetworkInterfaceOwnerTagKey),
				Values: ([]string{config.NetworkInterfaceOwnerTagValue,
					config.NetworkInterfaceOwnerVPCCNITagValue}),
			},
			{
				Name:   aws.String("vpc-id"),
				Values: []string{(e.VPCID)},
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
				// Increment promethues metrics for number of leaked ENIs cleaned up
				if tagIdx := slices.IndexFunc(networkInterface.TagSet, func(tag types.Tag) bool {
					return *tag.Key == config.NetworkInterfaceOwnerTagKey
				}); tagIdx != -1 {
					switch *networkInterface.TagSet[tagIdx].Value {
					case config.NetworkInterfaceOwnerTagValue:
						vpcrcAvailableCount += 1
					case config.NetworkInterfaceOwnerVPCCNITagValue:
						vpccniAvailableCount += 1
					default:
						// We should not hit this case as we only filter for relevant tag values, log error and continue if unexpected ENIs found
						e.Log.Error(fmt.Errorf("found available ENI not created by VPC-CNI/VPC-RC"), "eniID", *networkInterface.NetworkInterfaceId)
						continue
					}
				}

				// The ENI in available state has been sitting for at least the eni clean up interval and it should
				// be removed
				_, err := e.EC2Wrapper.DeleteNetworkInterface(&ec2.DeleteNetworkInterfaceInput{
					NetworkInterfaceId: networkInterface.NetworkInterfaceId,
				})
				if err != nil {
					if !strings.Contains(err.Error(), ec2Errors.NotFoundInterfaceID) { // ignore InvalidNetworkInterfaceID.NotFound error
						// append err and continue, we will retry deletion in the next period/reconcile
						leakedENICount += 1

						e.Log.Error(err, "failed to delete the dangling network interface",
							"id", *networkInterface.NetworkInterfaceId)
					}
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

		describeNetworkInterfaceIp.NextToken = describeNetworkInterfaceOp.NextToken
	}

	// Update leaked ENI metrics
	vpcrcAvailableENICnt.Set(float64(vpcrcAvailableCount))
	vpccniAvailableENICnt.Set(float64(vpccniAvailableCount))
	leakedENICnt.Set(float64(leakedENICount))
	// Set the available ENIs to the list of ENIs seen in the current cycle
	e.availableENIs = availableENIs
}
