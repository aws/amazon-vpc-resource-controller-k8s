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
	"fmt"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/version"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// getUserAgentConfig returns the config with app specific user-agent for AWS SDK v2
func getUserAgentConfig() aws.Config {
	return aws.Config{
		AppID: fmt.Sprintf("%s/%s", AppName, version.GitVersion),
	}
}
