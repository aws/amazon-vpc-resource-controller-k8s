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

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"

	configv2 "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	MaxRetries = 3
	AppName    = "amazon-vpc-resource-controller-k8s"
	SourceKey  = "x-amz-source-arn"
	AccountKey = "x-amz-source-account"
)

type EC2Wrapper interface {
	DescribeInstances(input *ec2v2.DescribeInstancesInput) (*ec2v2.DescribeInstancesOutput, error)
	CreateNetworkInterface(input *ec2v2.CreateNetworkInterfaceInput) (*ec2v2.CreateNetworkInterfaceOutput, error)
	AttachNetworkInterface(input *ec2v2.AttachNetworkInterfaceInput) (*ec2v2.AttachNetworkInterfaceOutput, error)
	DetachNetworkInterface(input *ec2v2.DetachNetworkInterfaceInput) (*ec2v2.DetachNetworkInterfaceOutput, error)
	DeleteNetworkInterface(input *ec2v2.DeleteNetworkInterfaceInput) (*ec2v2.DeleteNetworkInterfaceOutput, error)
	AssignPrivateIPAddresses(input *ec2v2.AssignPrivateIpAddressesInput) (*ec2v2.AssignPrivateIpAddressesOutput, error)
	UnassignPrivateIPAddresses(input *ec2v2.UnassignPrivateIpAddressesInput) (*ec2v2.UnassignPrivateIpAddressesOutput, error)
	DescribeNetworkInterfaces(input *ec2v2.DescribeNetworkInterfacesInput) (*ec2v2.DescribeNetworkInterfacesOutput, error)
	CreateTags(input *ec2v2.CreateTagsInput) (*ec2v2.CreateTagsOutput, error)
	DescribeSubnets(input *ec2v2.DescribeSubnetsInput) (*ec2v2.DescribeSubnetsOutput, error)
	AssociateTrunkInterface(input *ec2v2.AssociateTrunkInterfaceInput) (*ec2v2.AssociateTrunkInterfaceOutput, error)
	DescribeTrunkInterfaceAssociations(input *ec2v2.DescribeTrunkInterfaceAssociationsInput) (*ec2v2.DescribeTrunkInterfaceAssociationsOutput, error)
	ModifyNetworkInterfaceAttribute(input *ec2v2.ModifyNetworkInterfaceAttributeInput) (*ec2v2.ModifyNetworkInterfaceAttributeOutput, error)
	CreateNetworkInterfacePermission(input *ec2v2.CreateNetworkInterfacePermissionInput) (*ec2v2.CreateNetworkInterfacePermissionOutput, error)
}

var (
	ec2APICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_api_req_count",
			Help: "The number of calls made to ec2",
		},
	)

	ec2APIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_api_err_count",
			Help: "The number of errors encountered while interacting with ec2",
		},
	)

	ec2DescribeInstancesAPICnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_instances_api_req_count",
			Help: "The number of calls made to ec2 for describing an instance",
		},
	)

	ec2DescribeInstancesAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_instances_api_err_count",
			Help: "The number of errors encountered while describing an ec2 instance",
		},
	)

	ec2CreateNetworkInterfaceAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_create_network_interface_api_req_count",
			Help: "The number of calls made to ec2 for creating a network interface",
		},
	)

	ec2CreateNetworkInterfaceAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_create_network_interface_api_err_count",
			Help: "The number of errors encountered while creating a network interface",
		},
	)

	ec2TagNetworkInterfaceAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_tag_network_interface_api_req_count",
			Help: "The number of calls made to ec2 for tagging a network interface",
		},
	)

	ec2TagNetworkInterfaceAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_tag_network_interface_api_err_count",
			Help: "The number of errors encountered while tagging a network interface",
		},
	)

	ec2AttachNetworkInterfaceAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_attach_network_interface_api_req_count",
			Help: "The number of calls made to ec2 for attaching a network interface",
		},
	)

	ec2AttachNetworkInterfaceAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_attach_network_interface_api_err_count",
			Help: "The number of errors encountered while attaching a network interface",
		},
	)

	ec2DescribeNetworkInterfaceAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_network_interface_api_req_count",
			Help: "The number of calls made to ec2 for describing a network interface",
		},
	)

	ec2DescribeNetworkInterfaceAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_network_interface_api_err_count",
			Help: "The number of errors encountered while describing a network interface",
		},
	)

	numAssignedSecondaryIPAddress = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "num_assigned_private_ip_address",
			Help: "The number of secondary private ip addresses allocated",
		},
	)

	numAssignedIPv4Prefixes = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "num_assigned_ipv4_prefixes",
			Help: "The number of ipv4 prefixes allocated",
		},
	)

	ec2AssignPrivateIPAddressAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_assign_private_ip_api_req_count",
			Help: "The number calls made to ec2 for assigning private ip addresses on network interface",
		},
	)

	ec2AssignPrivateIPAddressAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_assign_private_ip_api_err_count",
			Help: "The number of errors encountered while assigning private ip addresses on network interface",
		},
	)

	numUnassignedSecondaryIPAddress = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "num_unassigned_private_ip_address",
			Help: "The number of secondary private ip addresses unassigned",
		},
	)

	numUnassignedIPv4Prefixes = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "num_unassigned_ipv4_prefixes",
			Help: "The number of ipv4 prefixes unassigned",
		},
	)

	ec2UnassignPrivateIPAddressAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_unassign_private_ip_api_req_count",
			Help: "The number calls made to ec2 for unassigning private ip addresses on network interface",
		},
	)

	ec2UnassignPrivateIPAddressAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_unassign_private_ip_api_err_count",
			Help: "The number of errors encountered while unassigning private ip addresses on network interface",
		},
	)

	ec2DetachNetworkInterfaceAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_detach_network_interface_api_req_count",
			Help: "The number calls made to ec2 for detaching a network interface",
		},
	)

	ec2DetachNetworkInterfaceAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_detach_network_interface_api_err_count",
			Help: "The number of errors encountered while detaching a network interface",
		},
	)

	ec2DeleteNetworkInterfaceAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_delete_network_interface_api_req_count",
			Help: "The number calls made to EC2 for deleting a network interface",
		},
	)

	ec2DeleteNetworkInterfaceAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_delete_network_interface_api_err_count",
			Help: "The number of errors encountered while deleting a network interface",
		},
	)

	ec2DescribeSubnetsAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_subnets_api_req_count",
			Help: "The number of calls made to EC2 for describing subnets",
		},
	)

	ec2DescribeSubnetsAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_subnets_api_err_count",
			Help: "The number of errors encountered while describing subnets",
		},
	)

	ec2AssociateTrunkInterfaceAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_associate_trunk_interface_api_req_count",
			Help: "The number of calls made to EC2 for associating Trunk with Branch ENI",
		},
	)

	ec2AssociateTrunkInterfaceAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_associate_trunk_interface_api_err_count",
			Help: "The number of errors encountered while disassociating Trunk with Branch ENI",
		},
	)

	ec2describeTrunkInterfaceAssociationAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_trunk_network_interface_association_api_call_count",
			Help: "The number of calls made to describe trunk network interface",
		},
	)

	ec2describeTrunkInterfaceAssociationAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_describe_trunk_network_interface_association_api_err_count",
			Help: "The number of errors encountered to describe trunk network interface",
		},
	)

	ec2modifyNetworkInterfaceAttributeAPICallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_modify_network_interface_attribute_api_call_count",
			Help: "The number of calls made to modify the network interface attribute",
		},
	)

	ec2modifyNetworkInterfaceAttributeAPIErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_modify_network_interface_attribute_api_err_count",
			Help: "The number of errors encountered when modifying the network interface attribute",
		},
	)

	ec2APICallLatencies = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name: "aws_nw_call_latency",
			Help: "AWS ENI API Calls  Operation latency in ms",
		},
		[]string{"api"},
	)

	ec2CreateNetworkInterfacePermissionCallCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_create_network_interface_permission_api_call_count",
			Help: "The number of calls made to create network interface permission",
		},
	)

	ec2CreateNetworkInterfacePermissionErrCnt = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "ec2_create_network_interface_permission_api_err_count",
			Help: "The number of errors encountered while making calls to create network interface permission",
		},
	)

	prometheusRegistered = false
)

func prometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(
			ec2APICallCnt,
			ec2APIErrCnt,
			ec2DescribeInstancesAPICnt,
			ec2DescribeInstancesAPIErrCnt,
			ec2CreateNetworkInterfaceAPICallCnt,
			ec2CreateNetworkInterfaceAPIErrCnt,
			ec2TagNetworkInterfaceAPICallCnt,
			ec2TagNetworkInterfaceAPIErrCnt,
			ec2AttachNetworkInterfaceAPICallCnt,
			ec2AttachNetworkInterfaceAPIErrCnt,
			ec2DescribeNetworkInterfaceAPICallCnt,
			ec2DescribeNetworkInterfaceAPIErrCnt,
			numAssignedSecondaryIPAddress,
			numAssignedIPv4Prefixes,
			numUnassignedSecondaryIPAddress,
			numUnassignedIPv4Prefixes,
			ec2AssignPrivateIPAddressAPICallCnt,
			ec2AssignPrivateIPAddressAPIErrCnt,
			ec2DetachNetworkInterfaceAPICallCnt,
			ec2DetachNetworkInterfaceAPIErrCnt,
			ec2DeleteNetworkInterfaceAPICallCnt,
			ec2DeleteNetworkInterfaceAPIErrCnt,
			ec2DescribeSubnetsAPICallCnt,
			ec2DescribeSubnetsAPIErrCnt,
			ec2AssociateTrunkInterfaceAPICallCnt,
			ec2AssociateTrunkInterfaceAPIErrCnt,
			ec2describeTrunkInterfaceAssociationAPICallCnt,
			ec2describeTrunkInterfaceAssociationAPIErrCnt,
			ec2modifyNetworkInterfaceAttributeAPICallCnt,
			ec2modifyNetworkInterfaceAttributeAPIErrCnt,
			ec2APICallLatencies,
			ec2CreateNetworkInterfacePermissionCallCnt,
			ec2CreateNetworkInterfacePermissionErrCnt,
		)

		prometheusRegistered = true
	}
}

type ec2Wrapper struct {
	log                 logr.Logger
	userServiceClientV2 *ec2v2.Client
	accountID           string
}

// NewEC2Wrapper takes the roleARN that will be assumed to make all the EC2 API Calls, if no roleARN
// is passed then the ec2 client will be initialized with the instance's service role account.
func NewEC2Wrapper(roleARN, clusterName, region string, instanceClientQPS, instanceClientBurst,
	userClientQPS, userClientBurst int, log logr.Logger) (EC2Wrapper, error) {
	// Register the metrics
	prometheusRegister()

	ec2Wrapper := &ec2Wrapper{log: log}

	// Load default AWS config with user agent
	userAgentCfg := getUserAgentConfig()
	cfg, err := configv2.LoadDefaultConfig(context.Background(),
		configv2.WithRegion(region),
		configv2.WithAppID(userAgentCfg.AppID))
	if err != nil {
		return nil, fmt.Errorf("unable to load SDK config: %v", err)
	}

	// Get instance metadata
	imdsClient := imds.NewFromConfig(cfg)
	if region == "" {
		regionOutput, err := imdsClient.GetRegion(context.Background(), &imds.GetRegionInput{})
		if err != nil {
			return nil, fmt.Errorf("failed to find the region from ec2 metadata: %v", err)
		}
		cfg.Region = regionOutput.Region
	}

	identity, err := imdsClient.GetInstanceIdentityDocument(context.Background(), &imds.GetInstanceIdentityDocumentInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to get the instance identity document: %v", err)
	}
	ec2Wrapper.accountID = identity.AccountID

	// Role ARN is passed, assume the role ARN to make EC2 API Calls
	if roleARN != "" {
		// Get source account and ARN
		sourceAcct, _, sourceArn, err := utils.GetSourceAcctAndArn(roleARN, region, clusterName)
		if err != nil {
			return nil, err
		}

		// Create STS client with assumed role
		stsClient := sts.NewFromConfig(cfg)
		provider := stscreds.NewAssumeRoleProvider(stsClient, roleARN, func(o *stscreds.AssumeRoleOptions) {
			o.Duration = time.Hour
			if sourceAcct != "" && sourceArn != "" {
				o.RoleSessionName = AppName
			}
		})

		// Create new config with assumed role credentials
		cfgWithRole, err := configv2.LoadDefaultConfig(context.Background(),
			configv2.WithRegion(region),
			configv2.WithCredentialsProvider(provider),
			configv2.WithAppID(userAgentCfg.AppID))
		if err != nil {
			return nil, fmt.Errorf("failed to load config with assumed role: %v", err)
		}

		// Create EC2 client with assumed role
		ec2Wrapper.userServiceClientV2 = ec2v2.NewFromConfig(cfgWithRole)
	} else {
		// Use instance role
		ec2Wrapper.userServiceClientV2 = ec2v2.NewFromConfig(cfg)
	}

	return ec2Wrapper, nil
}

func timeSinceMs(start time.Time) float64 {
	return float64(time.Since(start).Milliseconds())
}

func (e *ec2Wrapper) DescribeInstances(input *ec2v2.DescribeInstancesInput) (*ec2v2.DescribeInstancesOutput, error) {
	start := time.Now()
	describeInstancesOutput, err := e.userServiceClientV2.DescribeInstances(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("describe_instances").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DescribeInstancesAPICnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DescribeInstancesAPIErrCnt.Inc()
	}

	return describeInstancesOutput, err
}

func (e *ec2Wrapper) CreateNetworkInterface(input *ec2v2.CreateNetworkInterfaceInput) (*ec2v2.CreateNetworkInterfaceOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.CreateNetworkInterface(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("create_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2CreateNetworkInterfaceAPICallCnt.Inc()

	if input.SecondaryPrivateIpAddressCount != nil {
		numAssignedSecondaryIPAddress.Add(float64(*input.SecondaryPrivateIpAddressCount))
	}

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2CreateNetworkInterfaceAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) AttachNetworkInterface(input *ec2v2.AttachNetworkInterfaceInput) (*ec2v2.AttachNetworkInterfaceOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.AttachNetworkInterface(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("attach_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2AttachNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2AttachNetworkInterfaceAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) DeleteNetworkInterface(input *ec2v2.DeleteNetworkInterfaceInput) (*ec2v2.DeleteNetworkInterfaceOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.DeleteNetworkInterface(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("delete_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DeleteNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DeleteNetworkInterfaceAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) DetachNetworkInterface(input *ec2v2.DetachNetworkInterfaceInput) (*ec2v2.DetachNetworkInterfaceOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.DetachNetworkInterface(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("detach_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DetachNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DetachNetworkInterfaceAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) DescribeNetworkInterfaces(input *ec2v2.DescribeNetworkInterfacesInput) (*ec2v2.DescribeNetworkInterfacesOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.DescribeNetworkInterfaces(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("describe_network_interfaces").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DescribeNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DescribeNetworkInterfaceAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) AssignPrivateIPAddresses(input *ec2v2.AssignPrivateIpAddressesInput) (*ec2v2.AssignPrivateIpAddressesOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.AssignPrivateIpAddresses(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("assign_private_ip").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2AssignPrivateIPAddressAPICallCnt.Inc()

	// Since the same API AssignPrivateIPAddresses is called to either allocate a secondary IPv4 address or a IPv4 prefix,
	// the metric count needs to be distinguished as to which resource is assigned by the call
	if input.SecondaryPrivateIpAddressCount != nil && *input.SecondaryPrivateIpAddressCount != 0 {
		numAssignedSecondaryIPAddress.Add(float64(*input.SecondaryPrivateIpAddressCount))
	} else if input.Ipv4PrefixCount != nil && *input.Ipv4PrefixCount != 0 {
		numAssignedIPv4Prefixes.Add(float64(*input.Ipv4PrefixCount))
	}

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2AssignPrivateIPAddressAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) UnassignPrivateIPAddresses(input *ec2v2.UnassignPrivateIpAddressesInput) (*ec2v2.UnassignPrivateIpAddressesOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.UnassignPrivateIpAddresses(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("unassign_private_ip").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2UnassignPrivateIPAddressAPICallCnt.Inc()
	if input.PrivateIpAddresses != nil && len(input.PrivateIpAddresses) != 0 {
		numUnassignedSecondaryIPAddress.Add(float64(len(input.PrivateIpAddresses)))
	} else if input.Ipv4Prefixes != nil && len(input.Ipv4Prefixes) != 0 {
		numUnassignedIPv4Prefixes.Add(float64(len(input.Ipv4Prefixes)))
	}

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2UnassignPrivateIPAddressAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) CreateTags(input *ec2v2.CreateTagsInput) (*ec2v2.CreateTagsOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.CreateTags(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("create_tags").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2TagNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2TagNetworkInterfaceAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) DescribeSubnets(input *ec2v2.DescribeSubnetsInput) (*ec2v2.DescribeSubnetsOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.DescribeSubnets(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("describe_subnets").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DescribeSubnetsAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DescribeSubnetsAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) DescribeTrunkInterfaceAssociations(input *ec2v2.DescribeTrunkInterfaceAssociationsInput) (*ec2v2.DescribeTrunkInterfaceAssociationsOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.DescribeTrunkInterfaceAssociations(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("describe_trunk_association").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2describeTrunkInterfaceAssociationAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2describeTrunkInterfaceAssociationAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) AssociateTrunkInterface(input *ec2v2.AssociateTrunkInterfaceInput) (*ec2v2.AssociateTrunkInterfaceOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.AssociateTrunkInterface(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("associate_trunk_to_branch").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2AssociateTrunkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2AssociateTrunkInterfaceAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) ModifyNetworkInterfaceAttribute(input *ec2v2.ModifyNetworkInterfaceAttributeInput) (*ec2v2.ModifyNetworkInterfaceAttributeOutput, error) {
	start := time.Now()
	output, err := e.userServiceClientV2.ModifyNetworkInterfaceAttribute(context.Background(), input)
	ec2APICallLatencies.WithLabelValues("modify_network_interface_attribute").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2modifyNetworkInterfaceAttributeAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2modifyNetworkInterfaceAttributeAPIErrCnt.Inc()
	}

	return output, err
}

func (e *ec2Wrapper) CreateNetworkInterfacePermission(input *ec2v2.CreateNetworkInterfacePermissionInput) (*ec2v2.CreateNetworkInterfacePermissionOutput, error) {
	// Add the account ID of the instance running the controller
	input.AwsAccountId = &e.accountID
	output, err := e.userServiceClientV2.CreateNetworkInterfacePermission(context.Background(), input)

	// Metric Update
	ec2APICallCnt.Inc()
	ec2CreateNetworkInterfacePermissionCallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2CreateNetworkInterfacePermissionErrCnt.Inc()
	}

	return output, err
}
