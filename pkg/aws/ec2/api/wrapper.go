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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/utils"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/version"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	smithymiddleware "github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
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
	DescribeInstances(input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error)
	CreateNetworkInterface(input *ec2.CreateNetworkInterfaceInput) (*ec2.CreateNetworkInterfaceOutput, error)
	AttachNetworkInterface(input *ec2.AttachNetworkInterfaceInput) (*ec2.AttachNetworkInterfaceOutput, error)
	DetachNetworkInterface(input *ec2.DetachNetworkInterfaceInput) (*ec2.DetachNetworkInterfaceOutput, error)
	DeleteNetworkInterface(input *ec2.DeleteNetworkInterfaceInput) (*ec2.DeleteNetworkInterfaceOutput, error)
	AssignPrivateIPAddresses(input *ec2.AssignPrivateIpAddressesInput) (*ec2.AssignPrivateIpAddressesOutput, error)
	UnassignPrivateIPAddresses(input *ec2.UnassignPrivateIpAddressesInput) (*ec2.UnassignPrivateIpAddressesOutput, error)
	DescribeNetworkInterfaces(input *ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error)
	CreateTags(input *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error)
	DescribeSubnets(input *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error)
	AssociateTrunkInterface(input *ec2.AssociateTrunkInterfaceInput) (*ec2.AssociateTrunkInterfaceOutput, error)
	DescribeTrunkInterfaceAssociations(input *ec2.DescribeTrunkInterfaceAssociationsInput) (*ec2.DescribeTrunkInterfaceAssociationsOutput, error)
	ModifyNetworkInterfaceAttribute(input *ec2.ModifyNetworkInterfaceAttributeInput) (*ec2.ModifyNetworkInterfaceAttributeOutput, error)
	CreateNetworkInterfacePermission(input *ec2.CreateNetworkInterfacePermissionInput) (*ec2.CreateNetworkInterfacePermissionOutput, error)
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
			vpccniAvailableENICnt,
			vpcrcAvailableENICnt,
			leakedENICnt,
		)

		prometheusRegistered = true
	}
}

type ec2Wrapper struct {
	log                   logr.Logger
	instanceServiceClient *ec2.Client
	userServiceClient     *ec2.Client
	accountID             string
}

// NewEC2Wrapper takes the roleARN that will be assumed to make all the EC2 API Calls, if no roleARN
// is passed then the ec2 client will be initialized with the instance's service role account.
func NewEC2Wrapper(roleARN, clusterName, region string, instanceClientQPS, instanceClientBurst,
	userClientQPS, userClientBurst int, log logr.Logger) (EC2Wrapper, error) {
	// Register the metrics
	prometheusRegister()

	ec2Wrapper := &ec2Wrapper{log: log}

	cfg, err := ec2Wrapper.getInstanceSession()
	if err != nil {
		return nil, err
	}

	// Role ARN is passed, assume the role ARN to make EC2 API Calls
	if roleARN != "" {
		// Create the instance service client with low QPS, it will be only used fro associate branch to trunk calls
		log.Info("Creating INSTANCE service client with configured QPS", "QPS", instanceClientQPS, "Burst", instanceClientBurst)
		instanceServiceClient, err := ec2Wrapper.getInstanceServiceClient(instanceClientQPS, instanceClientBurst,
			cfg)
		if err != nil {
			return nil, err
		}
		ec2Wrapper.instanceServiceClient = instanceServiceClient

		// Create the user service client with higher QPS, this will be used to make rest of the EC2 API Calls
		log.Info("Creating USER service client with configured QPS", "QPS", userClientQPS, "Burst", userClientBurst)
		userServiceClient, err := ec2Wrapper.getClientUsingAssumedRole(cfg.Region, roleARN, clusterName, region,
			userClientQPS, userClientBurst)
		if err != nil {
			return nil, err
		}
		ec2Wrapper.userServiceClient = userServiceClient
	} else {
		// Role ARN is not provided, assuming that instance service client is allowlisted for ENI branching and use
		// the instance service client as the user service client with higher QPS.
		log.Info("Creating INSTANCE service client with configured USER Service QPS", "QPS", userClientQPS, "Burst", userClientBurst)
		instanceServiceClient, err := ec2Wrapper.getInstanceServiceClient(userClientQPS,
			userClientBurst, cfg)
		if err != nil {
			return nil, err
		}
		ec2Wrapper.instanceServiceClient = instanceServiceClient
		ec2Wrapper.userServiceClient = instanceServiceClient
	}

	return ec2Wrapper, nil
}

func (e *ec2Wrapper) getInstanceSession() (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithAPIOptions([]func(stack *smithymiddleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue(AppName, version.GitVersion),
		}),
	)
	if err != nil {
		return cfg, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Get the region from the ec2 Metadata if the region is missing in the session config
	ec2Metadata := imds.NewFromConfig(cfg)
	region, err := ec2Metadata.GetRegion(context.TODO(), &imds.GetRegionInput{})
	if err != nil {
		return cfg, fmt.Errorf("failed to find the region from ec2 metadata: %v", err)
	}
	cfg.Region = region.Region

	instanceIdentity, err := ec2Metadata.GetInstanceIdentityDocument(context.TODO(), &imds.GetInstanceIdentityDocumentInput{})
	if err != nil {
		return cfg, fmt.Errorf("failed to get the instance identity document %v", err)
	}
	// Set the Account ID
	e.accountID = instanceIdentity.AccountID
	return cfg, nil
}

func (e *ec2Wrapper) getInstanceServiceClient(qps int, burst int, cfg aws.Config) (*ec2.Client, error) {
	instanceClient, err := utils.NewRateLimitedClient(qps, burst)
	if err != nil {
		return nil, fmt.Errorf("failed to create reate limited client with %d qps and %d burst: %v",
			qps, burst, err)
	}
	return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.Retryer = retry.AddWithMaxAttempts(retry.NewStandard(), MaxRetries)
		o.HTTPClient = instanceClient
	}), nil
}

func (e *ec2Wrapper) getClientUsingAssumedRole(instanceRegion, roleARN, clusterName, region string, qps, burst int) (*ec2.Client, error) {
	var providers []aws.CredentialsProvider

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(instanceRegion),
		config.WithRetryer(func() aws.Retryer {
			return retry.NewStandard(func(o *retry.StandardOptions) {
				o.MaxAttempts = MaxRetries
			})
		}),
		config.WithAPIOptions([]func(stack *smithymiddleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue(AppName, version.GitVersion),
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create a rate limited http client for the
	client, err := utils.NewRateLimitedClient(qps, burst)
	if err != nil {
		return nil, fmt.Errorf("failed to create reate limited client with %d qps and %d burst: %v", qps, burst, err)
	}
	e.log.Info("created rate limited http client", "qps", qps, "burst", burst)

	// GetPartition ID, SourceAccount and SourceARN
	roleARN = strings.Trim(roleARN, "\"")

	sourceAcct, partitionID, sourceArn, err := utils.GetSourceAcctAndArn(roleARN, region, clusterName)
	if err != nil {
		return nil, err
	}

	// Get the regional sts end point
	regionalSTSEndpoint, err := e.getRegionalStsEndpoint(partitionID, region)
	if err != nil {
		return nil, fmt.Errorf("failed to get the regional sts endpoint for region %s: %v %v",
			instanceRegion, err, partitionID)
	}

	regionalProvider := stscreds.NewAssumeRoleProvider(
		e.createSTSClient(cfg, client, regionalSTSEndpoint, sourceAcct, sourceArn),
		roleARN,
		func(o *stscreds.AssumeRoleOptions) {
			o.Duration = time.Minute * 60
			o.RoleSessionName = AppName
		},
	)
	providers = append(providers, regionalProvider)

	e.log.Info("initialized the regional providers", "roleARN", roleARN)

	cfg.Credentials = aws.NewCredentialsCache(aws.CredentialsProviderFunc(func(ctx context.Context) (aws.Credentials, error) {
		for _, provider := range providers {
			creds, err := provider.Retrieve(ctx)
			if err == nil {
				return creds, nil
			}
		}
		return aws.Credentials{}, errors.New("failed to retrieve credentials from all providers")
	}))

	return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.HTTPClient = client
	}), nil

}

func (e *ec2Wrapper) createSTSClient(
	cfg aws.Config,
	client *http.Client,
	endpoint string,
	sourceAcct, sourceArn string) *sts.Client {

	optFns := []func(*sts.Options){
		func(o *sts.Options) {
			o.Retryer = retry.AddWithMaxAttempts(retry.NewStandard(), MaxRetries)
			o.HTTPClient = client
		},
		sts.WithEndpointResolverV2(&stsResolverV2{
			endpoint: endpoint,
		}),
	}

	// only both sourceAcct and sourceArn are provided, we send extra header context
	// otherwise we fallback to the default client
	if sourceAcct != "" && sourceArn != "" {
		optFns = append(optFns, sts.WithAPIOptions(func(stack *smithymiddleware.Stack) error {
			return stack.Finalize.Add(smithymiddleware.FinalizeMiddlewareFunc("AddHeaders", func(ctx context.Context, input smithymiddleware.FinalizeInput, next smithymiddleware.FinalizeHandler) (
				output smithymiddleware.FinalizeOutput, metadata smithymiddleware.Metadata, err error) {
				req, ok := input.Request.(*smithyhttp.Request)
				if !ok {
					return output, metadata, fmt.Errorf("unrecognized transport type %T", input.Request)
				}
				req.Header.Set(SourceKey, sourceArn)
				req.Header.Set(AccountKey, sourceAcct)
				return next.HandleFinalize(ctx, input)
			}), smithymiddleware.Before)
		}))
		// TODO: change this log verbosity to 1 after we have configured clients in place for some time
		e.log.Info("Will use configured STS client with extra headers")
	} else {
		e.log.Info("Will use default STS client since empty source account or/and empty source arn", "SourceAcct", sourceAcct, "SourceArn", sourceArn)
	}

	return sts.NewFromConfig(cfg, optFns...)
}

func timeSinceMs(start time.Time) float64 {
	return float64(time.Since(start).Milliseconds())
}

func (e *ec2Wrapper) DescribeInstances(input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	start := time.Now()
	describeInstancesOutput, err := e.userServiceClient.DescribeInstances(context.TODO(), input)
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

func (e *ec2Wrapper) CreateNetworkInterface(input *ec2.CreateNetworkInterfaceInput) (*ec2.CreateNetworkInterfaceOutput, error) {
	start := time.Now()
	createNetworkInterfaceOutput, err := e.userServiceClient.CreateNetworkInterface(context.TODO(), input)
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

	return createNetworkInterfaceOutput, err
}

func (e *ec2Wrapper) AttachNetworkInterface(input *ec2.AttachNetworkInterfaceInput) (*ec2.AttachNetworkInterfaceOutput, error) {
	start := time.Now()
	attachNetworkInterfaceOutput, err := e.userServiceClient.AttachNetworkInterface(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("attach_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2AttachNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2AttachNetworkInterfaceAPIErrCnt.Inc()
	}

	return attachNetworkInterfaceOutput, err
}

func (e *ec2Wrapper) DeleteNetworkInterface(input *ec2.DeleteNetworkInterfaceInput) (*ec2.DeleteNetworkInterfaceOutput, error) {
	start := time.Now()
	deleteNetworkInterfaceOutput, err := e.userServiceClient.DeleteNetworkInterface(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("delete_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DeleteNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DeleteNetworkInterfaceAPIErrCnt.Inc()
	}

	return deleteNetworkInterfaceOutput, err
}

func (e *ec2Wrapper) DetachNetworkInterface(input *ec2.DetachNetworkInterfaceInput) (*ec2.DetachNetworkInterfaceOutput, error) {
	start := time.Now()
	detachNetworkInterfaceOutput, err := e.userServiceClient.DetachNetworkInterface(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("detach_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DetachNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DetachNetworkInterfaceAPIErrCnt.Inc()
	}

	return detachNetworkInterfaceOutput, err
}

func (e *ec2Wrapper) DescribeNetworkInterfaces(input *ec2.DescribeNetworkInterfacesInput) (*ec2.DescribeNetworkInterfacesOutput, error) {
	start := time.Now()
	describeNetworkInterfacesOutput, err := e.userServiceClient.DescribeNetworkInterfaces(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("describe_network_interface").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2DescribeNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2DescribeNetworkInterfaceAPIErrCnt.Inc()
	}

	return describeNetworkInterfacesOutput, err
}

func (e *ec2Wrapper) AssignPrivateIPAddresses(input *ec2.AssignPrivateIpAddressesInput) (*ec2.AssignPrivateIpAddressesOutput, error) {
	start := time.Now()
	assignPrivateIPAddressesOutput, err := e.userServiceClient.AssignPrivateIpAddresses(context.TODO(), input)
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

	return assignPrivateIPAddressesOutput, err
}

func (e *ec2Wrapper) UnassignPrivateIPAddresses(input *ec2.UnassignPrivateIpAddressesInput) (*ec2.UnassignPrivateIpAddressesOutput, error) {
	start := time.Now()
	unAssignPrivateIPAddressesOutput, err := e.userServiceClient.UnassignPrivateIpAddresses(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("unassign_private_ip").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2UnassignPrivateIPAddressAPICallCnt.Inc()
	if len(input.PrivateIpAddresses) != 0 {
		numUnassignedSecondaryIPAddress.Add(float64(len(input.PrivateIpAddresses)))
	} else if len(input.Ipv4Prefixes) != 0 {
		numUnassignedIPv4Prefixes.Add(float64(len(input.Ipv4Prefixes)))
	}

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2UnassignPrivateIPAddressAPIErrCnt.Inc()
	}

	return unAssignPrivateIPAddressesOutput, err
}

func (e *ec2Wrapper) CreateTags(input *ec2.CreateTagsInput) (*ec2.CreateTagsOutput, error) {
	start := time.Now()
	createTagsOutput, err := e.userServiceClient.CreateTags(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("create_tags").Observe(timeSinceMs(start))

	// Metric updates
	ec2APICallCnt.Inc()
	ec2TagNetworkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2TagNetworkInterfaceAPIErrCnt.Inc()
	}

	return createTagsOutput, err
}

func (e *ec2Wrapper) DescribeSubnets(input *ec2.DescribeSubnetsInput) (*ec2.DescribeSubnetsOutput, error) {
	start := time.Now()
	output, err := e.userServiceClient.DescribeSubnets(context.TODO(), input)
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

// DescribeTrunkInterfaceAssociations cannot be used as it's not public yet.
func (e *ec2Wrapper) DescribeTrunkInterfaceAssociations(input *ec2.DescribeTrunkInterfaceAssociationsInput) (*ec2.DescribeTrunkInterfaceAssociationsOutput, error) {
	start := time.Now()
	describeTrunkInterfaceAssociationInput, err := e.instanceServiceClient.DescribeTrunkInterfaceAssociations(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("describe_trunk_association").Observe(timeSinceMs(start))

	// Metric Update
	ec2APICallCnt.Inc()
	ec2describeTrunkInterfaceAssociationAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2describeTrunkInterfaceAssociationAPIErrCnt.Inc()
	}

	return describeTrunkInterfaceAssociationInput, err
}

func (e *ec2Wrapper) AssociateTrunkInterface(input *ec2.AssociateTrunkInterfaceInput) (*ec2.AssociateTrunkInterfaceOutput, error) {
	start := time.Now()
	associateTrunkInterfaceOutput, err := e.instanceServiceClient.AssociateTrunkInterface(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("associate_trunk_to_branch").Observe(timeSinceMs(start))

	// Metric Update
	ec2APICallCnt.Inc()
	ec2AssociateTrunkInterfaceAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2AssociateTrunkInterfaceAPIErrCnt.Inc()
	}

	return associateTrunkInterfaceOutput, err
}

func (e *ec2Wrapper) ModifyNetworkInterfaceAttribute(input *ec2.ModifyNetworkInterfaceAttributeInput) (*ec2.ModifyNetworkInterfaceAttributeOutput, error) {
	start := time.Now()
	modifyNetworkInterfaceAttributeOutput, err := e.userServiceClient.ModifyNetworkInterfaceAttribute(context.TODO(), input)
	ec2APICallLatencies.WithLabelValues("modify_network_interface_attribute").Observe(timeSinceMs(start))

	// Metric Update
	ec2APICallCnt.Inc()
	ec2modifyNetworkInterfaceAttributeAPICallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2modifyNetworkInterfaceAttributeAPIErrCnt.Inc()
	}

	return modifyNetworkInterfaceAttributeOutput, err
}

func (e *ec2Wrapper) CreateNetworkInterfacePermission(input *ec2.CreateNetworkInterfacePermissionInput) (*ec2.CreateNetworkInterfacePermissionOutput, error) {
	// Add the account ID of the instance running the controller
	input.AwsAccountId = &e.accountID
	output, err := e.userServiceClient.CreateNetworkInterfacePermission(context.TODO(), input)

	// Metric Update
	ec2APICallCnt.Inc()
	ec2CreateNetworkInterfacePermissionCallCnt.Inc()

	if err != nil {
		ec2APIErrCnt.Inc()
		ec2CreateNetworkInterfacePermissionErrCnt.Inc()
	}

	return output, err
}

// TODO: Migrate this function to v2
func (e *ec2Wrapper) getRegionalStsEndpoint(partitionID, region string) (string, error) {
	var partition *endpoints.Partition
	var stsServiceID = "sts"
	for _, p := range endpoints.DefaultPartitions() {
		if partitionID == p.ID() {
			partition = &p
			break
		}
	}
	if partition == nil {
		return "", fmt.Errorf("partition %s not valid", partitionID)
	}

	stsSvc, ok := partition.Services()[stsServiceID]
	if !ok {
		e.log.Info("STS service not found in partition, generating default endpoint.", "Partition:", partitionID)
		// Add the host of the current instances region if the service doesn't already exists in the partition
		// so we don't fail if the service is not present in the go sdk but matches the instances region.
		res, err := partition.EndpointFor(stsServiceID, region, endpoints.STSRegionalEndpointOption, endpoints.ResolveUnknownServiceOption)
		if err != nil {
			return "", fmt.Errorf("error resolving endpoint for %s in partition %s. err: %v", region, partition.ID(), err)
		}
		return res.URL, nil
	}

	res, err := stsSvc.ResolveEndpoint(region, endpoints.STSRegionalEndpointOption)
	if err != nil {
		return "", fmt.Errorf("error resolving endpoint for %s in partition %s. err: %v", region, partition.ID(), err)
	}
	return res.URL, nil
}
