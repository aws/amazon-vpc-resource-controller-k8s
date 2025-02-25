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

	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	v2ec2metadata "github.com/aws/aws-sdk-go-v2/feature/ec2/imds"
)

// EC2MetadataClient defines the interface for EC2 metadata client
type EC2MetadataClient interface {
	// GetInstanceIdentityDocument returns the instance identity document from EC2 metadata
	GetInstanceIdentityDocument(ctx context.Context) (ec2metadata.EC2InstanceIdentityDocument, error)
	// Region returns the region the instance is running in
	Region(ctx context.Context) (string, error)
}

// EC2MetadataClientV1 is the implementation of EC2MetadataClient using AWS SDK v1
type EC2MetadataClientV1 struct {
	client *ec2metadata.EC2Metadata
}

// NewEC2MetadataClientV1 creates a new EC2MetadataClientV1 instance
func NewEC2MetadataClientV1(sess *session.Session) EC2MetadataClient {
	return &EC2MetadataClientV1{client: ec2metadata.New(sess)}
}

// GetInstanceIdentityDocument returns the instance identity document from EC2 metadata
func (e *EC2MetadataClientV1) GetInstanceIdentityDocument(ctx context.Context) (ec2metadata.EC2InstanceIdentityDocument, error) {
	// V1 SDK doesn't support context, so we ignore it
	return e.client.GetInstanceIdentityDocument()
}

// Region returns the region the instance is running in
func (e *EC2MetadataClientV1) Region(ctx context.Context) (string, error) {
	// V1 SDK doesn't support context, so we ignore it
	return e.client.Region()
}

// EC2MetadataClientV2 is the implementation of EC2MetadataClient using AWS SDK v2
type EC2MetadataClientV2 struct {
	client *v2ec2metadata.Client
}

// NewEC2MetadataClientV2 creates a new EC2MetadataClientV2 instance
func NewEC2MetadataClientV2(client *v2ec2metadata.Client) EC2MetadataClient {
	return &EC2MetadataClientV2{client: client}
}

// GetInstanceIdentityDocument returns the instance identity document from EC2 metadata
func (e *EC2MetadataClientV2) GetInstanceIdentityDocument(ctx context.Context) (ec2metadata.EC2InstanceIdentityDocument, error) {
	resp, err := e.client.GetInstanceIdentityDocument(ctx, &v2ec2metadata.GetInstanceIdentityDocumentInput{})
	if err != nil {
		return ec2metadata.EC2InstanceIdentityDocument{}, fmt.Errorf("failed to get instance identity document: %v", err)
	}

	// Convert v2 document to v1 document format to maintain compatibility
	return ec2metadata.EC2InstanceIdentityDocument{
		PrivateIP:        resp.PrivateIP,
		AvailabilityZone: resp.AvailabilityZone,
		Version:          resp.Version,
		Region:           resp.Region,
		InstanceID:       resp.InstanceID,
		InstanceType:     resp.InstanceType,
		AccountID:        resp.AccountID,
		PendingTime:      resp.PendingTime,
		ImageID:          resp.ImageID,
		Architecture:     resp.Architecture,
	}, nil
}

// Region returns the region the instance is running in
func (e *EC2MetadataClientV2) Region(ctx context.Context) (string, error) {
	resp, err := e.client.GetRegion(ctx, &v2ec2metadata.GetRegionInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get region from EC2 metadata: %v", err)
	}
	return resp.Region, nil
}
