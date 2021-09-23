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
	"math/rand"
	"testing"
	"time"

	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node/manager"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework"
	"github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/manifest"
	configMapWrapper "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/resource/k8s/configmap"
	_ "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/utils"
	verifier "github.com/aws/amazon-vpc-resource-controller-k8s/test/framework/verify"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

var (
	frameWork             *framework.Framework
	verify                *verifier.PodVerification
	ctx                   context.Context
	err                   error
	windowsNodeList       *v1.NodeList
	defaultTestPort       int
	instanceSecurityGroup string
	configMap             *v1.ConfigMap
	observedConfigMap     *v1.ConfigMap
)

func TestWindowsVPCResourceController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Windows Integration Test Suite")
}

var _ = BeforeSuite(func() {
	frameWork = framework.New(framework.GlobalOptions)

	ctx = context.Background()
	verify = verifier.NewPodVerification(frameWork, ctx)

	By("creating the configmap with flag set to true if not exists")
	configMap = manifest.NewConfigMapBuilder().Build()
	existingConfigmap, err := frameWork.ConfigMapManager.GetConfigMap(ctx, configMap)
	if err != nil {
		configMapWrapper.CreateConfigMap(frameWork.ConfigMapManager, ctx, configMap)
	} else {
		observedConfigMap = manifest.NewConfigMapBuilder().Data(existingConfigmap.Data).Build()
		configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, configMap)
	}

	By("getting the list of Windows node")
	windowsNodeList, err = frameWork.NodeManager.GetNodesWithOS("windows")
	Expect(err).ToNot(HaveOccurred())

	By("getting the instance ID for the first node")
	Expect(len(windowsNodeList.Items)).To(BeNumerically(">", 1))
	instanceID := manager.GetNodeInstanceID(&windowsNodeList.Items[0])

	By("getting the security group associated with the instance")
	instance, err := frameWork.EC2Manager.GetInstanceDetails(instanceID)
	Expect(err).ToNot(HaveOccurred())
	instanceSecurityGroup = *instance.SecurityGroups[0].GroupId

	By("authorizing the security group ingress for HTTP traffic")
	defaultTestPort = 80
	frameWork.EC2Manager.AuthorizeSecurityGroupIngress(instanceSecurityGroup, defaultTestPort, "TCP")

	rand.Seed(time.Now().UnixNano())
})

var _ = AfterSuite(func() {

	if observedConfigMap != nil {
		configMapWrapper.UpdateConfigMap(frameWork.ConfigMapManager, ctx, observedConfigMap)
	} else {
		configMapWrapper.DeleteConfigMap(frameWork.ConfigMapManager, ctx, configMap)
	}

	By("revoking the security group ingress for HTTP traffic")
	frameWork.EC2Manager.RevokeSecurityGroupIngress(instanceSecurityGroup, defaultTestPort, "TCP")
})
