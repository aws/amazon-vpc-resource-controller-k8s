/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ec2

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
)

const (
	MetadataRetries = 15
)

// TODO: Move away from using mock

// HttpClient is used to help with testing
type HttpClient interface {
	GetInstanceIdentityDocument() (ec2metadata.EC2InstanceIdentityDocument, error)
	Region() (string, error)
}

// EC2MetadataClient to used to obtain a subset of information from EC2 IMDS
type EC2MetadataClient interface {
	GetInstanceIdentityDocument() (ec2metadata.EC2InstanceIdentityDocument, error)
	Region() (string, error)
}

type ec2MetadataClientImpl struct {
	client HttpClient
}

// New creates an ec2metadata client to retrieve metadata
func NewMetaDataClient(client HttpClient) EC2MetadataClient {
	if client == nil {
		return &ec2MetadataClientImpl{client: ec2metadata.New(session.New(), aws.NewConfig().WithMaxRetries(MetadataRetries))}
	} else {
		return &ec2MetadataClientImpl{client: client}
	}
}

// InstanceIdentityDocument returns instance identity documents
// http://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instance-identity-documents.html
func (c *ec2MetadataClientImpl) GetInstanceIdentityDocument() (ec2metadata.EC2InstanceIdentityDocument, error) {
	return c.client.GetInstanceIdentityDocument()
}

func (c *ec2MetadataClientImpl) Region() (string, error) {
	return c.client.Region()
}
