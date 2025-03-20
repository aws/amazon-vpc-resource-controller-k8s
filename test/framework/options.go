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

package framework

import (
	"flag"
	"os"
	"strings"

	"github.com/pkg/errors"
	"k8s.io/client-go/tools/clientcmd"
)

var GlobalOptions Options

func init() {
	GlobalOptions.BindFlags()
}

type Options struct {
	KubeConfig           string
	ClusterName          string
	AWSRegion            string
	AWSVPCID             string
	ReleasedImageVersion string
	ClusterRoleArn       string
}

func (options *Options) BindFlags() {
	flag.StringVar(&options.KubeConfig, "cluster-kubeconfig", "", "Path to kubeconfig containing embedded authinfo (required)")
	flag.StringVar(&options.ClusterName, "cluster-name", "", `Kubernetes cluster name (required)`)
	flag.StringVar(&options.AWSRegion, "aws-region", "", `AWS Region for the kubernetes cluster`)
	flag.StringVar(&options.AWSVPCID, "aws-vpc-id", "", `AWS VPC ID for the kubernetes cluster`)
	flag.StringVar(&options.ReleasedImageVersion, "latest-released-rc-image-tag", "v1.1.3", `VPC RC latest released image`)
	flag.StringVar(&options.ClusterRoleArn, "cluster-role-arn", "", "EKS Cluster role ARN")
}

func (options *Options) Validate() error {
	if len(options.KubeConfig) == 0 {
		return errors.Errorf("%s must be set!", clientcmd.RecommendedConfigPathFlag)
	}
	if len(options.ClusterName) == 0 {
		return errors.Errorf("%s must be set!", "cluster-name")
	}
	if len(options.AWSRegion) == 0 {
		return errors.Errorf("%s must be set!", "aws-region")
	}
	if len(options.AWSVPCID) == 0 {
		return errors.Errorf("%s must be set!", "aws-vpc-id")
	}
	if len(options.ReleasedImageVersion) == 0 {
		return errors.Errorf("%s must be set!", "latest-released-rc-image-tag")
	}
	dir, err := os.Executable()
	if err == nil && len(options.ClusterRoleArn) == 0 && strings.Contains(dir, "ec2api") {
		return errors.Errorf("%s must be set when running ec2api tests", "cluster-role-arn")
	}

	return nil
}
