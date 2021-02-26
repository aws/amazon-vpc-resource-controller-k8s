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

package windows_test

import (
	"context"
	"testing"

	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	_ "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	_ "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/verify"
	verifier "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/verify"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var (
	frameWork *framework.Framework
	verify *verifier.PodVerification
	ctx context.Context
	err error
)

func TestWindowsVPCResourceController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Windows Integration Test Suite")
}

var _ = BeforeSuite(func() {
	frameWork = framework.New(framework.GlobalOptions)
	ctx = context.Background()
	verify = verifier.NewPodVerification(frameWork, ctx)
})
